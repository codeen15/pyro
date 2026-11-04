package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/codeen15/pyro/pkg/engine"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol"
	"github.com/codeen15/pyro/pkg/engine/vars"
)

// maxLog bounds the timeline kept per session for UI reloads.
const maxLog = 2000

type liveSession struct {
	ID        string               `json:"id"`
	Path      string               `json:"path,omitempty"`
	Kind      model.Kind           `json:"kind"`
	Info      protocol.SessionInfo `json:"info"`
	OpenedAt  time.Time            `json:"openedAt"`
	ConnectMS float64              `json:"connectMs"`

	// Connection is the name socket steps use to reach this session.
	Connection string `json:"connection"`

	sess  protocol.Session
	conn  *engine.Conn
	vars  *vars.Set
	owner string // the user who opened it (team server)

	mu     sync.Mutex
	log    []messageView
	closed bool
	err    string
}

// messageView adds the ack round trip in milliseconds for the UI.
type messageView struct {
	protocol.Message
	RTTMS float64 `json:"rttMs,omitempty"`
}

func view(m protocol.Message) messageView {
	return messageView{Message: m, RTTMS: float64(m.RTT.Microseconds()) / 1000}
}

type sessionState struct {
	ID       string        `json:"id"`
	Path     string        `json:"path,omitempty"`
	Kind     model.Kind    `json:"kind"`
	Info     any           `json:"info"`
	OpenedAt time.Time     `json:"openedAt"`
	Closed   bool          `json:"closed"`
	Error    string        `json:"error,omitempty"`
	Messages []messageView `json:"messages"`
	// Connection is the name socket steps use for this session.
	Connection string `json:"connection"`
}

func (ls *liveSession) state() sessionState {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return sessionState{ls.ID, ls.Path, ls.Kind, ls.Info, ls.OpenedAt, ls.closed, ls.err, append([]messageView{}, ls.log...), ls.Connection}
}

func (ls *liveSession) record(m protocol.Message) messageView {
	v := view(m)
	ls.mu.Lock()
	ls.log = append(ls.log, v)
	if len(ls.log) > maxLog {
		ls.log = ls.log[len(ls.log)-maxLog:]
	}
	ls.mu.Unlock()
	return v
}

type sessionRegistry struct {
	mu   sync.Mutex
	byID map[string]*liveSession
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type openBody struct {
	Path        string         `json:"path,omitempty"`
	Request     *model.Request `json:"request,omitempty"`
	Environment string         `json:"environment,omitempty"`
}

type openResponse struct {
	*engine.ConnectResult
	Session *sessionState `json:"session,omitempty"`
}

func (s *Server) openSession(w http.ResponseWriter, r *http.Request) {
	var body openBody
	if !decode(w, r, &body) {
		return
	}
	in, slug, ok := s.prepare(w, body.Path, body.Request, body.Environment)
	if !ok {
		return
	}
	bases := s.sessionFor(r).apply(in.Vars, slug)
	// The handshake must not be tied to this HTTP request's lifetime.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	res := s.cfg.Engine.Connect(ctx, in)
	s.sessionFor(r).record(in.Vars, slug, bases)
	if res.Error != "" {
		writeJSON(w, http.StatusOK, openResponse{ConnectResult: res})
		return
	}
	name := res.Sent.Connection
	if name == "" {
		name = model.DefaultConnection
	}
	ls := &liveSession{
		owner: principal(r).ID, ID: newID(), Path: body.Path, Kind: in.Request.Kind, Info: res.Session.Info(), OpenedAt: time.Now(),
		ConnectMS: float64(time.Since(start).Microseconds()) / 1000, sess: res.Session, vars: in.Vars, Connection: name,
	}
	topic := "session:" + ls.ID
	// The connection reads the session: the console log and live view see
	// every message, and socket steps (send/ping/close requests) reach it by
	// name through the user's connections.
	ls.conn = engine.NewConn(name, res.Session, func(m protocol.Message) {
		s.hub.publish(topic, "message", ls.record(m))
	})
	s.sessions.mu.Lock()
	s.sessions.byID[ls.ID] = ls
	s.sessions.mu.Unlock()
	s.connsFor(r).Put(name, ls.conn)
	go s.watch(ls)
	st := ls.state()
	writeJSON(w, http.StatusOK, openResponse{ConnectResult: res, Session: &st})
}

// watch tells the console when the connection ends.
func (s *Server) watch(ls *liveSession) {
	<-ls.conn.Done()
	ls.mu.Lock()
	ls.closed = true
	if err := ls.conn.Err(); err != nil {
		ls.err = err.Error()
	}
	errMsg := ls.err
	ls.mu.Unlock()
	s.hub.publish("session:"+ls.ID, "closed", map[string]string{"error": errMsg})
}

// connsFor returns the user's named connections in this project.
func (s *Server) connsFor(r *http.Request) *engine.Conns {
	return s.connsOf(principal(r).ID)
}

func (s *Server) connsOf(user string) *engine.Conns {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	cs := s.connsByUser[user]
	if cs == nil {
		cs = engine.NewConns()
		s.connsByUser[user] = cs
	}
	return cs
}

// sessionByConn finds the user's open console for a connection name.
func (s *Server) sessionByConn(user, name string) *liveSession {
	s.sessions.mu.Lock()
	defer s.sessions.mu.Unlock()
	for _, ls := range s.sessions.byID {
		if ls.owner == user && ls.Connection == name {
			ls.mu.Lock()
			closed := ls.closed
			ls.mu.Unlock()
			if !closed {
				return ls
			}
		}
	}
	return nil
}

func (s *Server) lookupSession(w http.ResponseWriter, r *http.Request) *liveSession {
	s.sessions.mu.Lock()
	ls := s.sessions.byID[chi.URLParam(r, "id")]
	s.sessions.mu.Unlock()
	if ls != nil && ls.owner != principal(r).ID {
		ls = nil // another member's session
	}
	if ls == nil {
		writeError(w, http.StatusNotFound, "session not found (it may have been closed)")
	}
	return ls
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	if ls := s.lookupSession(w, r); ls != nil {
		writeJSON(w, http.StatusOK, ls.state())
	}
}

func (s *Server) sendSession(w http.ResponseWriter, r *http.Request) {
	ls := s.lookupSession(w, r)
	if ls == nil {
		return
	}
	var out protocol.Outgoing
	if !decode(w, r, &out) {
		return
	}
	resolved, unresolved := engine.ResolveOutgoing(out, ls.vars)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	m, err := ls.sess.Send(ctx, resolved)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	// Working the console by hand: later socket steps wait for new messages.
	ls.conn.MarkRead()
	v := ls.record(m)
	s.hub.publish("session:"+ls.ID, "message", v)
	writeJSON(w, http.StatusOK, map[string]any{"message": v, "unresolved": unresolved})
}

func (s *Server) closeSession(w http.ResponseWriter, r *http.Request) {
	ls := s.lookupSession(w, r)
	if ls == nil {
		return
	}
	if s.connsFor(r).Get(ls.Connection) == ls.conn {
		_ = s.connsFor(r).Close(ls.Connection, 0, "")
	} else {
		_ = ls.sess.Close()
	}
	s.sessions.mu.Lock()
	delete(s.sessions.byID, ls.ID)
	s.sessions.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) closeAllSessions() {
	s.sessions.mu.Lock()
	all := make([]*liveSession, 0, len(s.sessions.byID))
	for _, ls := range s.sessions.byID {
		all = append(all, ls)
	}
	s.sessions.byID = map[string]*liveSession{}
	s.sessions.mu.Unlock()
	for _, ls := range all {
		_ = ls.sess.Close()
	}
	s.sessionMu.Lock()
	conns := s.connsByUser
	s.connsByUser = map[string]*engine.Conns{}
	s.sessionMu.Unlock()
	for _, cs := range conns {
		cs.CloseAll()
	}
}

// prepare loads the request, its chain and variables for a send or session.
func (s *Server) prepare(w http.ResponseWriter, path string, req *model.Request, envName string) (engine.SendInput, string, bool) {
	in := engine.SendInput{Request: req, Path: path}
	if path == "" && req == nil {
		writeError(w, http.StatusBadRequest, "path or request is required")
		return in, "", false
	}
	if path != "" {
		chain, err := s.ws.Chain(path)
		if err != nil {
			s.fail(w, err)
			return in, "", false
		}
		in.Chain = chain
		if in.Request == nil {
			if in.Request, err = s.ws.ReadRequest(path); err != nil {
				s.fail(w, err)
				return in, "", false
			}
		}
	}
	var env *model.Environment
	if envName != "" {
		var err error
		if env, err = s.ws.ReadEnvironment(envName); err != nil {
			s.fail(w, err)
			return in, "", false
		}
		env.Name = envName
	}
	in.Vars = engine.NewVars(env, in.Chain)
	in.Vars.Globals = s.ws.GlobalVars()
	return in, strings.SplitN(path, "/", 2)[0], true
}

// openConn is one of the user's open connections, for the editor.
type openConn struct {
	Name      string     `json:"name"`
	Kind      model.Kind `json:"kind,omitempty"`
	SessionID string     `json:"sessionId,omitempty"` // the console showing it
	Path      string     `json:"path,omitempty"`      // the connect request
	URL       string     `json:"url,omitempty"`
}

// listConnections lists the user's open connections (socket steps reach
// them by name; a connect request's console picks its own back up).
func (s *Server) listConnections(w http.ResponseWriter, r *http.Request) {
	user := principal(r).ID
	out := []openConn{}
	for _, name := range s.connsOf(user).Names() {
		oc := openConn{Name: name}
		if ls := s.sessionByConn(user, name); ls != nil {
			oc.Kind, oc.SessionID, oc.Path, oc.URL = ls.Kind, ls.ID, ls.Path, ls.Info.URL
		}
		out = append(out, oc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, out)
}
