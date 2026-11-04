package team

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/codeen15/pyro/internal/server"
	"github.com/codeen15/pyro/internal/store"
	"github.com/codeen15/pyro/internal/version"
)

// CookieName is the session cookie.
const CookieName = "pyro_session"

// CSRFHeader must accompany every state-changing request. Browsers can't add
// custom headers to cross-site form posts, and the server never answers CORS
// preflights, so its presence proves the request came from Pyro's own pages.
const CSRFHeader = "X-Pyro-CSRF"

// Signup policies.
const (
	SignupInviteOnly = "invite-only"
	SignupOpen       = "open"
	SignupDomains    = "domains"
)

type Config struct {
	// DataDir holds pyro.db and projects/<id>/.
	DataDir string
	Store   *Store
	// PublicURL is the address people open, e.g. https://pyro.example.com.
	// It sets the OAuth 2.0 redirect URI and marks cookies Secure for https.
	PublicURL string
	// Signup is who may create an account: invite-only (default), open, or
	// domains (emails in SignupDomains). The first account is always allowed.
	Signup        string
	SignupDomains []string
	// Project returns the server settings (engine, history, plugins…) for a
	// project folder.
	Project func(ws *store.Workspace, projectID string) server.Config
	// MaxLoads caps load tests running at once across all projects (0 = 2).
	MaxLoads int
	// IdleClose closes a project's server after this long unused (0 = 30m).
	IdleClose time.Duration
	// Mailer sends invite emails (nil: invites are links to copy).
	Mailer Mailer
	UI     fs.FS
	Logger *slog.Logger
}

// Server is the team server's HTTP front: sign-in, workspaces, projects, and
// each project's API (handed to that project's internal/server.Server).
type Server struct {
	cfg       Config
	st        *Store
	log       *slog.Logger
	loadSlots chan struct{}
	byEmail   *limiter
	byIP      *limiter

	mu       sync.Mutex
	projects map[string]*openProject
	stop     chan struct{}
}

type openProject struct {
	srv     *server.Server
	handler http.Handler
	used    time.Time
}

func New(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Signup == "" {
		cfg.Signup = SignupInviteOnly
	}
	if cfg.MaxLoads <= 0 {
		cfg.MaxLoads = 2
	}
	if cfg.IdleClose <= 0 {
		cfg.IdleClose = 30 * time.Minute
	}
	cfg.PublicURL = strings.TrimSuffix(cfg.PublicURL, "/")
	s := &Server{
		cfg: cfg, st: cfg.Store, log: cfg.Logger,
		loadSlots: make(chan struct{}, cfg.MaxLoads),
		byEmail:   newLimiter(10, 15*time.Minute),
		byIP:      newLimiter(50, 15*time.Minute),
		projects:  map[string]*openProject{},
		stop:      make(chan struct{}),
	}
	go s.closeIdle()
	return s
}

// Close shuts every open project down.
func (s *Server) Close() {
	close(s.stop)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, p := range s.projects {
		p.srv.Close()
		delete(s.projects, id)
	}
}

func (s *Server) closeIdle() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
		}
		s.mu.Lock()
		for id, p := range s.projects {
			if time.Since(p.used) > s.cfg.IdleClose && !p.srv.Busy() {
				p.srv.Close()
				delete(s.projects, id)
			}
		}
		s.mu.Unlock()
	}
}

func (s *Server) projectDir(id string) string {
	return filepath.Join(s.cfg.DataDir, "projects", id)
}

// project opens (or reuses) a project's server.
func (s *Server) project(p *ProjectInfo) (*openProject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if op := s.projects[p.ID]; op != nil {
		op.used = time.Now()
		return op, nil
	}
	dir := s.projectDir(p.ID)
	var ws *store.Workspace
	var err error
	if store.Exists(dir) {
		ws, err = store.Open(dir)
	} else {
		ws, err = store.Init(dir, p.Name)
	}
	if err != nil {
		return nil, err
	}
	cfg := server.Config{}
	if s.cfg.Project != nil {
		cfg = s.cfg.Project(ws, p.ID)
	}
	cfg.Workspace, cfg.Key, cfg.Team, cfg.AnyHost, cfg.Token, cfg.UI = ws, "project:"+p.ID, true, true, "", nil
	cfg.LoadSlots = s.loadSlots
	if cfg.Logger == nil {
		cfg.Logger = s.log
	}
	if s.cfg.PublicURL != "" {
		cfg.CallbackURL = s.cfg.PublicURL + "/oauth2/p/" + p.ID + "/callback"
	}
	projectID := p.ID
	cfg.OnChange = func(who server.Principal, c server.Change) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.st.RecordProject(ctx, who.ID, projectID, c.Action, c.Target); err != nil {
			s.log.Warn("recording activity failed", "err", err)
		}
	}
	srv := server.New(cfg)
	op := &openProject{srv: srv, handler: srv.Handler(), used: time.Now()}
	s.projects[p.ID] = op
	return op, nil
}

func (s *Server) closeProject(id string) {
	s.mu.Lock()
	op := s.projects[id]
	delete(s.projects, id)
	s.mu.Unlock()
	if op != nil {
		op.srv.Close()
	}
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer, s.csrf)

	r.Get("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version.Version, "mode": "team"})
	})
	r.Get("/api/v1/auth/config", s.authConfig)
	r.Post("/api/v1/auth/signup", s.signup)
	r.Post("/api/v1/auth/login", s.login)
	r.Post("/api/v1/auth/logout", s.logout)
	r.Get("/api/v1/invites/{token}", s.previewInvite)

	r.Group(func(r chi.Router) {
		r.Use(s.requireUser)
		r.Get("/api/v1/me", s.me)
		r.Patch("/api/v1/me", s.patchMe)
		r.Post("/api/v1/me/password", s.changePassword)
		r.Get("/api/v1/workspaces", s.listWorkspaces)
		r.Post("/api/v1/workspaces", s.createWorkspace)
		r.Patch("/api/v1/workspaces/{ws}", s.renameWorkspace)
		r.Delete("/api/v1/workspaces/{ws}", s.deleteWorkspace)
		r.Post("/api/v1/workspaces/{ws}/projects", s.createProject)
		r.Post("/api/v1/workspaces/{ws}/projects/import", s.importProject)
		r.Get("/api/v1/workspaces/{ws}/members", s.listMembers)
		r.Patch("/api/v1/workspaces/{ws}/members/{user}", s.setMemberRole)
		r.Delete("/api/v1/workspaces/{ws}/members/{user}", s.removeMember)
		r.Get("/api/v1/workspaces/{ws}/invites", s.listInvites)
		r.Post("/api/v1/workspaces/{ws}/invites", s.createInvite)
		r.Delete("/api/v1/workspaces/{ws}/invites/{id}", s.revokeInvite)
		r.Get("/api/v1/workspaces/{ws}/activity", s.activity)
		r.Post("/api/v1/invites/{token}/accept", s.acceptInvite)
		r.Post("/api/v1/projects/{pid}/move", s.moveProject)
		r.Post("/api/v1/projects/{pid}/transfer", s.transfer)
		r.Get("/api/v1/projects/{pid}/archive", s.archive)
		r.Get("/api/v1/projects/{pid}", s.getProject)
		r.Patch("/api/v1/projects/{pid}", s.renameProject)
		r.Delete("/api/v1/projects/{pid}", s.deleteProject)
		r.HandleFunc("/api/v1/projects/{pid}/*", s.proxy)
		r.Get("/oauth2/p/{pid}/callback", s.proxy)
	})
	r.HandleFunc("/api/v1/*", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})
	if s.cfg.UI != nil {
		r.Handle("/*", server.SPA(s.cfg.UI))
	}
	return r
}

// csrf refuses state-changing requests without the CSRF header or from
// another origin.
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.Header.Get(CSRFHeader) == "" {
				writeError(w, http.StatusForbidden, "missing "+CSRFHeader+" header")
				return
			}
			if o := r.Header.Get("Origin"); o != "" {
				if u, err := url.Parse(o); err != nil || !strings.EqualFold(u.Host, r.Host) {
					writeError(w, http.StatusForbidden, "cross-origin request refused")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

type userKey struct{}

type authed struct {
	user    *User
	session string // hash
}

func current(r *http.Request) authed {
	a, _ := r.Context().Value(userKey{}).(authed)
	return a
}

func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "sign in to continue")
			return
		}
		u, h, err := s.st.SessionUser(r.Context(), c.Value)
		if err != nil {
			if !errors.Is(err, ErrNotFound) {
				s.log.Error("session lookup failed", "err", err)
			}
			writeError(w, http.StatusUnauthorized, "your session has ended; sign in again")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, authed{u, h})))
	})
}

func (s *Server) secureCookies(r *http.Request) bool {
	return r.TLS != nil || strings.HasPrefix(s.cfg.PublicURL, "https://")
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, u *User) error {
	tok, err := s.st.CreateSession(r.Context(), u.ID, clientIP(r), r.UserAgent())
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: tok, Path: "/", HttpOnly: true, Secure: s.secureCookies(r),
		SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds()),
	})
	return nil
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// signupAllowed applies the signup policy to an email address.
func (s *Server) signupAllowed(ctx context.Context, email string) (bool, error) {
	n, err := s.st.CountUsers(ctx)
	if err != nil || n == 0 {
		return n == 0, err
	}
	switch s.cfg.Signup {
	case SignupOpen:
		return true, nil
	case SignupDomains:
		at := strings.LastIndexByte(email, '@')
		if at < 0 {
			return false, nil
		}
		domain := strings.ToLower(email[at+1:])
		for _, d := range s.cfg.SignupDomains {
			if strings.EqualFold(strings.TrimPrefix(d, "@"), domain) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (s *Server) authConfig(w http.ResponseWriter, r *http.Request) {
	n, err := s.st.CountUsers(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	signedIn := false
	if c, err := r.Cookie(CookieName); err == nil {
		_, _, err := s.st.SessionUser(r.Context(), c.Value)
		signedIn = err == nil
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":          "team",
		"signedIn":      signedIn,
		"firstUser":     n == 0,
		"signup":        n == 0 || s.cfg.Signup != SignupInviteOnly,
		"signupPolicy":  s.cfg.Signup,
		"signupDomains": s.cfg.SignupDomains,
		"email":         s.cfg.Mailer != nil,
		"version":       version.Version,
	})
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
		// Invite is an invite token: it allows sign-up whatever the policy,
		// and the new account joins its workspace.
		Invite string `json:"invite,omitempty"`
	}
	if !decode(w, r, &body) {
		return
	}
	ok, err := s.signupAllowed(r.Context(), body.Email)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !ok && body.Invite != "" {
		inv, err := s.st.InviteByToken(r.Context(), body.Invite)
		if err != nil {
			s.inviteFail(w, err)
			return
		}
		if inv.Email != "" && !strings.EqualFold(inv.Email, strings.TrimSpace(body.Email)) {
			writeError(w, http.StatusForbidden, "this invite is for "+inv.Email)
			return
		}
		ok = true
	}
	if !ok {
		msg := "sign-up is by invitation: ask a workspace owner for an invite link"
		if s.cfg.Signup == SignupDomains {
			msg = "sign-up is limited to " + strings.Join(s.cfg.SignupDomains, ", ") + " email addresses"
		}
		writeError(w, http.StatusForbidden, msg)
		return
	}
	u, err := s.st.CreateUser(r.Context(), NewUser{Email: body.Email, Name: body.Name, Password: body.Password})
	if err != nil {
		s.fail(w, err)
		return
	}
	if body.Invite != "" {
		if p, err := s.st.AcceptInvite(r.Context(), body.Invite, u); err != nil {
			s.log.Warn("accepting invite at sign-up failed", "err", err)
		} else if err := s.st.Record(r.Context(), u.ID, p.WorkspaceID, "", "joined as "+p.Role, ""); err != nil {
			s.log.Warn("recording activity failed", "err", err)
		}
	}
	if err := s.setSession(w, r, u); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.meView(r.Context(), u))
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	email, ip := strings.ToLower(strings.TrimSpace(body.Email)), clientIP(r)
	if s.byEmail.blocked(email) || s.byIP.blocked(ip) {
		writeError(w, http.StatusTooManyRequests, "too many failed sign-ins; wait 15 minutes and try again")
		return
	}
	u, err := s.st.Authenticate(r.Context(), email, body.Password)
	if err != nil {
		s.byEmail.fail(email)
		s.byIP.fail(ip)
		// 400, not 401: 401 means "your session ended" to the UI.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.byEmail.reset(email)
	if err := s.setSession(w, r, u); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.meView(r.Context(), u))
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		_ = s.st.DeleteSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", HttpOnly: true, Secure: s.secureCookies(r), SameSite: http.SameSiteLaxMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

type meView struct {
	Mode       string      `json:"mode"`
	User       *User       `json:"user"`
	Workspaces []Workspace `json:"workspaces"`
}

func (s *Server) meView(ctx context.Context, u *User) meView {
	ws, err := s.st.Workspaces(ctx, u.ID)
	if err != nil {
		s.log.Error("listing workspaces failed", "err", err)
		ws = []Workspace{}
	}
	return meView{Mode: "team", User: u, Workspaces: ws}
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.meView(r.Context(), current(r).user))
}

func (s *Server) patchMe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	u := current(r).user
	if err := s.st.SetName(r.Context(), u.ID, body.Name); err != nil {
		s.fail(w, err)
		return
	}
	u.Name = strings.TrimSpace(body.Name)
	writeJSON(w, http.StatusOK, s.meView(r.Context(), u))
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	a := current(r)
	if a.user.Provider != "local" {
		writeError(w, http.StatusBadRequest, "this account signs in through "+a.user.Provider+"; change the password there")
		return
	}
	if !verifyPassword(a.user.pwHash, body.Current) {
		writeError(w, http.StatusForbidden, "the current password is wrong")
		return
	}
	if err := s.st.SetPassword(r.Context(), a.user.ID, body.Password, a.session); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	ws, err := s.st.Workspaces(r.Context(), current(r).user.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ws)
}

func (s *Server) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	ws, err := s.st.CreateWorkspace(r.Context(), current(r).user.ID, body.Name)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ws)
}

// needRole checks the user's role in a workspace; non-members get 404 so
// workspace IDs aren't confirmed to outsiders.
func (s *Server) needRole(w http.ResponseWriter, r *http.Request, wsID string, need server.Role) bool {
	role, err := s.st.Role(r.Context(), wsID, current(r).user.ID)
	if err != nil {
		s.fail(w, err)
		return false
	}
	if role == server.RoleNone {
		writeError(w, http.StatusNotFound, "workspace not found")
		return false
	}
	if role < need {
		writeError(w, http.StatusForbidden, "your role in this workspace doesn't allow this (needs "+need.String()+")")
		return false
	}
	return true
}

func (s *Server) renameWorkspace(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	id := chi.URLParam(r, "ws")
	if !s.needRole(w, r, id, server.RoleOwner) {
		return
	}
	if err := s.st.RenameWorkspace(r.Context(), id, body.Name); err != nil {
		s.fail(w, err)
		return
	}
	s.record(r, id, "", "renamed the workspace", strings.TrimSpace(body.Name))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteWorkspace(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "ws")
	if !s.needRole(w, r, id, server.RoleOwner) {
		return
	}
	if err := s.st.DeleteWorkspace(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	wsID := chi.URLParam(r, "ws")
	if !s.needRole(w, r, wsID, server.RoleEditor) {
		return
	}
	p, err := s.st.CreateProject(r.Context(), wsID, current(r).user.ID, body.Name)
	if err != nil {
		s.fail(w, err)
		return
	}
	if _, err := store.Init(s.projectDir(p.ID), p.Name); err != nil {
		_ = s.st.DeleteProject(r.Context(), p.ID)
		s.fail(w, err)
		return
	}
	s.record(r, wsID, p.ID, "created project", p.Name)
	writeJSON(w, http.StatusCreated, p)
}

// projectFor loads a project the user can see (404 otherwise).
func (s *Server) projectFor(w http.ResponseWriter, r *http.Request) (*ProjectInfo, server.Role, bool) {
	p, role, err := s.st.ProjectFor(r.Context(), chi.URLParam(r, "pid"), current(r).user.ID)
	if err == nil && role == server.RoleNone {
		err = ErrNotFound
	}
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
		} else {
			s.fail(w, err)
		}
		return nil, role, false
	}
	return p, role, true
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	if p, _, ok := s.projectFor(w, r); ok {
		writeJSON(w, http.StatusOK, p)
	}
}

func (s *Server) renameProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	p, role, ok := s.projectFor(w, r)
	if !ok {
		return
	}
	if role < server.RoleOwner {
		writeError(w, http.StatusForbidden, "only workspace owners can rename projects")
		return
	}
	if err := s.st.RenameProject(r.Context(), p.ID, body.Name); err != nil {
		s.fail(w, err)
		return
	}
	s.record(r, p.Workspace, p.ID, "renamed project from "+p.Name, strings.TrimSpace(body.Name))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	p, role, ok := s.projectFor(w, r)
	if !ok {
		return
	}
	if role < server.RoleOwner {
		writeError(w, http.StatusForbidden, "only workspace owners can delete projects")
		return
	}
	s.closeProject(p.ID)
	if err := s.st.DeleteProject(r.Context(), p.ID); err != nil {
		s.fail(w, err)
		return
	}
	s.record(r, p.Workspace, "", "deleted project", p.Name)
	if err := os.RemoveAll(s.projectDir(p.ID)); err != nil {
		s.log.Warn("removing project folder failed", "project", p.ID, "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// proxy hands /api/v1/projects/{pid}/X to the project's server as /api/v1/X,
// acting as the signed-in user with their workspace role.
func (s *Server) proxy(w http.ResponseWriter, r *http.Request) {
	p, role, ok := s.projectFor(w, r)
	if !ok {
		return
	}
	op, err := s.project(p)
	if err != nil {
		s.fail(w, err)
		return
	}
	u := current(r).user
	path := "/oauth2/callback"
	if !strings.HasPrefix(r.URL.Path, "/oauth2/") {
		path = "/api/v1/" + chi.URLParam(r, "*")
	}
	// The project routes the request itself, so drop this router's context.
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, nil)
	r2 := r.WithContext(server.WithPrincipal(ctx, server.Principal{ID: u.ID, Name: u.Name, Role: role, Admin: u.Admin}))
	u2 := *r.URL
	u2.Path, u2.RawPath = path, ""
	if raw := r.URL.RawPath; raw != "" && !strings.HasPrefix(r.URL.Path, "/oauth2/") {
		// Keep escaped request paths (e.g. %2F in a name) escaped.
		if i := strings.Index(raw, "/"+p.ID+"/"); i >= 0 {
			u2.RawPath = "/api/v1/" + raw[i+len(p.ID)+2:]
		}
	}
	r2.URL = &u2
	op.handler.ServeHTTP(w, r2)
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrInvalid):
		writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": "))
	case errors.Is(err, ErrExists):
		writeError(w, http.StatusConflict, strings.TrimPrefix(err.Error(), ErrExists.Error()+": "))
	default:
		s.log.Error("request failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

const maxBodyBytes = 1 << 20

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
