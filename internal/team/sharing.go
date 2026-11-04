package team

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/codeen15/pyro/internal/server"
	"github.com/codeen15/pyro/internal/store"
)

// Sharing: members, invites, activity, moving projects, copying collections
// between projects and project archives.

// SMTP sends invite emails; without it, invites are links to copy.
type SMTP struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	// Password defaults to $PYRO_SMTP_PASSWORD (keep it out of files).
	Password string `yaml:"password"`
	From     string `yaml:"from"`
}

// Mailer sends one plain-text email.
type Mailer interface {
	Send(to, subject, body string) error
}

// NewMailer returns an SMTP mailer, or nil when SMTP isn't configured.
func NewMailer(c SMTP) Mailer {
	if c.Host == "" || c.From == "" {
		return nil
	}
	if c.Port == 0 {
		c.Port = 587
	}
	if c.Password == "" {
		c.Password = os.Getenv("PYRO_SMTP_PASSWORD")
	}
	return smtpMailer(c)
}

type smtpMailer SMTP

func (m smtpMailer) Send(to, subject, body string) error {
	var auth smtp.Auth
	if m.Username != "" {
		auth = smtp.PlainAuth("", m.Username, m.Password, m.Host)
	}
	clean := func(s string) string { return strings.NewReplacer("\r", "", "\n", "").Replace(s) }
	msg := "From: " + clean(m.From) + "\r\nTo: " + clean(to) + "\r\nSubject: " + clean(subject) +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body
	// smtp.SendMail upgrades to TLS when the server offers STARTTLS.
	return smtp.SendMail(fmt.Sprintf("%s:%d", m.Host, m.Port), auth, m.From, []string{to}, []byte(msg))
}

func (s *Server) baseURL(r *http.Request) string {
	if s.cfg.PublicURL != "" {
		return s.cfg.PublicURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) record(r *http.Request, wsID, projectID, action, target string) {
	if err := s.st.Record(r.Context(), current(r).user.ID, wsID, projectID, action, target); err != nil {
		s.log.Warn("recording activity failed", "err", err)
	}
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "ws")
	if !s.needRole(w, r, id, server.RoleViewer) {
		return
	}
	m, err := s.st.Members(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) setMemberRole(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Role string `json:"role"`
	}
	if !decode(w, r, &body) {
		return
	}
	wsID, userID := chi.URLParam(r, "ws"), chi.URLParam(r, "user")
	if !s.needRole(w, r, wsID, server.RoleOwner) {
		return
	}
	if err := s.st.SetRole(r.Context(), wsID, userID, server.ParseRole(body.Role)); err != nil {
		s.fail(w, err)
		return
	}
	s.record(r, wsID, "", "changed a role to "+body.Role, s.userName(r.Context(), userID))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) userName(ctx context.Context, id string) string {
	if u, err := s.st.UserByID(ctx, id); err == nil {
		return u.Name
	}
	return ""
}

// removeMember removes someone (owners) or lets a member leave (themself).
func (s *Server) removeMember(w http.ResponseWriter, r *http.Request) {
	wsID, userID := chi.URLParam(r, "ws"), chi.URLParam(r, "user")
	self := userID == current(r).user.ID
	need := server.RoleOwner
	if self {
		need = server.RoleViewer
	}
	if !s.needRole(w, r, wsID, need) {
		return
	}
	name := s.userName(r.Context(), userID)
	if err := s.st.RemoveMember(r.Context(), wsID, userID); err != nil {
		s.fail(w, err)
		return
	}
	if self {
		s.record(r, wsID, "", "left the workspace", "")
	} else {
		s.record(r, wsID, "", "removed a member", name)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listInvites(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "ws")
	if !s.needRole(w, r, id, server.RoleOwner) {
		return
	}
	inv, err := s.st.Invites(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, inv)
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email   string `json:"email,omitempty"`
		Role    string `json:"role"`
		Days    int    `json:"days,omitempty"`
		MaxUses int    `json:"maxUses,omitempty"`
	}
	if !decode(w, r, &body) {
		return
	}
	wsID := chi.URLParam(r, "ws")
	if !s.needRole(w, r, wsID, server.RoleOwner) {
		return
	}
	me := current(r).user
	tok, inv, err := s.st.CreateInvite(r.Context(), wsID, me.ID, InviteOptions{
		Email: body.Email, Role: server.ParseRole(body.Role), TTL: time.Duration(body.Days) * 24 * time.Hour, MaxUses: body.MaxUses,
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	link := s.baseURL(r) + "/invite/" + tok
	out := map[string]any{"invite": inv, "url": link, "emailed": false}
	if inv.Email != "" && s.cfg.Mailer != nil {
		wsName, _, _ := s.st.WorkspaceName(r.Context(), wsID)
		text := fmt.Sprintf("%s invited you to the Pyro workspace %q as %s.\n\nOpen this link to join (it expires on %s):\n%s\n",
			me.Name, wsName, inv.Role, inv.Expires.Format("2 January 2006"), link)
		if err := s.cfg.Mailer.Send(inv.Email, "Join "+wsName+" on Pyro", text); err != nil {
			s.log.Warn("sending invite email failed", "err", err)
			out["emailError"] = "the email couldn't be sent; copy the link instead"
		} else {
			out["emailed"] = true
		}
	}
	who := inv.Email
	if who == "" {
		who = "invite link"
	}
	s.record(r, wsID, "", "invited as "+inv.Role, who)
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "ws")
	if !s.needRole(w, r, wsID, server.RoleOwner) {
		return
	}
	if err := s.st.RevokeInvite(r.Context(), wsID, chi.URLParam(r, "id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// previewInvite needs no sign-in: the token is the secret.
func (s *Server) previewInvite(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.InviteByToken(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		s.inviteFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) inviteFail(w http.ResponseWriter, err error) {
	if errors.Is(err, errInviteGone) {
		writeError(w, http.StatusGone, err.Error())
		return
	}
	s.fail(w, err)
}

func (s *Server) acceptInvite(w http.ResponseWriter, r *http.Request) {
	u := current(r).user
	p, err := s.st.AcceptInvite(r.Context(), chi.URLParam(r, "token"), u)
	if err != nil {
		s.inviteFail(w, err)
		return
	}
	s.record(r, p.WorkspaceID, "", "joined as "+p.Role, "")
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) activity(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "ws")
	if !s.needRole(w, r, id, server.RoleViewer) {
		return
	}
	a, err := s.st.ActivityLog(r.Context(), id, 200)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// moveProject puts a project in another workspace (owner of both).
func (s *Server) moveProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Workspace string `json:"workspace"`
	}
	if !decode(w, r, &body) {
		return
	}
	p, role, ok := s.projectFor(w, r)
	if !ok {
		return
	}
	if role < server.RoleOwner {
		writeError(w, http.StatusForbidden, "only owners of both workspaces can move a project")
		return
	}
	if !s.needRole(w, r, body.Workspace, server.RoleOwner) {
		return
	}
	if err := s.st.MoveProject(r.Context(), p.ID, body.Workspace); err != nil {
		s.fail(w, err)
		return
	}
	to, _, _ := s.st.WorkspaceName(r.Context(), body.Workspace)
	s.record(r, p.Workspace, p.ID, "moved project to "+to, p.Name)
	s.record(r, body.Workspace, p.ID, "moved project in from "+p.WorkspaceName, p.Name)
	w.WriteHeader(http.StatusNoContent)
}

// transfer copies or moves a collection to another project.
func (s *Server) transfer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Collection   string `json:"collection"`
		Project      string `json:"project"`
		Mode         string `json:"mode"` // copy | move
		Environments bool   `json:"environments,omitempty"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Mode != "copy" && body.Mode != "move" {
		writeError(w, http.StatusBadRequest, "mode must be copy or move")
		return
	}
	src, srcRole, ok := s.projectFor(w, r)
	if !ok {
		return
	}
	if body.Mode == "move" && srcRole < server.RoleEditor {
		writeError(w, http.StatusForbidden, "moving a collection out needs the editor role here")
		return
	}
	if body.Mode == "move" && body.Project == src.ID {
		writeError(w, http.StatusBadRequest, "pick another project to move it to")
		return
	}
	dst, dstRole, err := s.st.ProjectFor(r.Context(), body.Project, current(r).user.ID)
	if err == nil && dstRole == server.RoleNone {
		err = ErrNotFound
	}
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "target project not found")
		} else {
			s.fail(w, err)
		}
		return
	}
	if dstRole < server.RoleEditor {
		writeError(w, http.StatusForbidden, "adding to "+dst.Name+" needs the editor role there")
		return
	}
	srcOP, err := s.project(src)
	if err != nil {
		s.fail(w, err)
		return
	}
	dstOP, err := s.project(dst)
	if err != nil {
		s.fail(w, err)
		return
	}
	srcWS, dstWS := srcOP.srv.Workspace(), dstOP.srv.Workspace()
	slug, err := store.CopyCollection(srcWS, body.Collection, dstWS, body.Environments)
	if err != nil {
		s.failStore(w, err)
		return
	}
	u := current(r).user
	if body.Mode == "move" {
		if err := srcWS.DeleteCollection(body.Collection); err != nil {
			s.failStore(w, err)
			return
		}
		srcOP.srv.Changed(server.Change{By: u.ID, Name: u.Name, Action: "moved collection to " + dst.Name, Target: body.Collection})
		s.record(r, src.Workspace, src.ID, "moved collection to "+dst.Name, body.Collection)
	}
	verb := map[string]string{"copy": "copied collection from ", "move": "moved collection from "}[body.Mode]
	dstOP.srv.Changed(server.Change{By: u.ID, Name: u.Name, Action: verb + src.Name, Target: slug})
	s.record(r, dst.Workspace, dst.ID, verb+src.Name, slug)
	writeJSON(w, http.StatusOK, map[string]string{"slug": slug, "project": dst.ID})
}

// failStore maps store errors.
func (s *Server) failStore(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrInvalidPath):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrExists):
		writeError(w, http.StatusConflict, err.Error())
	default:
		s.fail(w, err)
	}
}

// archive downloads a project folder as a zip.
func (s *Server) archive(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.projectFor(w, r)
	if !ok {
		return
	}
	op, err := s.project(p)
	if err != nil {
		s.fail(w, err)
		return
	}
	var buf bytes.Buffer
	if err := op.srv.Workspace().Zip(&buf); err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, fileName(p.Name)))
	_, _ = w.Write(buf.Bytes())
}

func fileName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`"\/:*?<>|`, r) {
			return '-'
		}
		return r
	}, s)
	if s == "" {
		return "project"
	}
	return s
}

// importProject creates a project from a zipped workspace folder.
func (s *Server) importProject(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "ws")
	if !s.needRole(w, r, wsID, server.RoleEditor) {
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 200<<20))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "the archive is too large (200 MB at most)")
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		name = "Imported project"
	}
	p, err := s.st.CreateProject(r.Context(), wsID, current(r).user.ID, name)
	if err != nil {
		s.fail(w, err)
		return
	}
	if _, err := store.Unzip(data, s.projectDir(p.ID), name); err != nil {
		_ = s.st.DeleteProject(r.Context(), p.ID)
		s.failStore(w, err)
		return
	}
	s.record(r, wsID, p.ID, "uploaded project", p.Name)
	writeJSON(w, http.StatusCreated, p)
}
