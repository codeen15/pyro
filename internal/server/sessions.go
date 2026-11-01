package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/pyrohq/pyro/pkg/engine"
	"github.com/pyrohq/pyro/pkg/engine/model"
	"github.com/pyrohq/pyro/pkg/engine/protocol"
	"github.com/pyrohq/pyro/pkg/engine/vars"
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

	sess protocol.Session
	vars *vars.Set

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
}

func (ls *liveSession) state() sessionState {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return sessionState{ls.ID, ls.Path, ls.Kind, ls.Info, ls.OpenedAt, ls.closed, ls.err, append([]messageView(nil), ls.log...)}
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
	bases := s.session.apply(in.Vars, slug)
	// The handshake must not be tied to this HTTP request's lifetime.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	res := s.cfg.Engine.Connect(ctx, in)
	s.session.record(in.Vars, slug, bases)
	if res.Error != "" {
		writeJSON(w, http.StatusOK, openResponse{ConnectResult: res})
		return
	}
	ls := &liveSession{
		ID: newID(), Path: body.Path, Kind: in.Request.Kind, Info: res.Session.Info(), OpenedAt: time.Now(),
		ConnectMS: float64(time.Since(start).Microseconds()) / 1000, sess: res.Session, vars: in.Vars,
	}
	s.sessions.mu.Lock()
	s.sessions.byID[ls.ID] = ls
	s.sessions.mu.Unlock()
	go s.pump(ls)
	st := ls.state()
	writeJSON(w, http.StatusOK, openResponse{ConnectResult: res, Session: &st})
}

// pump forwards received messages to the log and to event subscribers.
func (s *Server) pump(ls *liveSession) {
	topic := "session:" + ls.ID
	for m := range ls.sess.Messages() {
		s.hub.publish(topic, "message", ls.record(m))
	}
	ls.mu.Lock()
	ls.closed = true
	if err := ls.sess.Err(); err != nil {
		ls.err = err.Error()
	}
	errMsg := ls.err
	ls.mu.Unlock()
	s.hub.publish(topic, "closed", map[string]string{"error": errMsg})
}

func (s *Server) lookupSession(w http.ResponseWriter, r *http.Request) *liveSession {
	s.sessions.mu.Lock()
	ls := s.sessions.byID[chi.URLParam(r, "id")]
	s.sessions.mu.Unlock()
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
	v := ls.record(m)
	s.hub.publish("session:"+ls.ID, "message", v)
	writeJSON(w, http.StatusOK, map[string]any{"message": v, "unresolved": unresolved})
}

func (s *Server) closeSession(w http.ResponseWriter, r *http.Request) {
	ls := s.lookupSession(w, r)
	if ls == nil {
		return
	}
	_ = ls.sess.Close()
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
	return in, strings.SplitN(path, "/", 2)[0], true
}
