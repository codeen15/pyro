package team

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/codeen15/pyro/internal/version"
)

// Personal access tokens, password resets and the admin API.

type providerView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Server) providerList() []providerView {
	out := []providerView{}
	for _, p := range s.cfg.OIDC {
		out = append(out, providerView{p.ID, p.Name})
	}
	return out
}

func (s *Server) listAPITokens(w http.ResponseWriter, r *http.Request) {
	t, err := s.st.APITokens(r.Context(), current(r).user.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) createAPIToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Days int    `json:"days,omitempty"` // 0 = never expires
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Days < 0 || body.Days > 3650 {
		writeError(w, http.StatusBadRequest, "days must be between 0 (never) and 3650")
		return
	}
	tok, t, err := s.st.CreateAPIToken(r.Context(), current(r).user.ID, body.Name, time.Duration(body.Days)*24*time.Hour)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"token": tok, "info": t})
}

func (s *Server) revokeAPIToken(w http.ResponseWriter, r *http.Request) {
	if err := s.st.RevokeAPIToken(r.Context(), current(r).user.ID, chi.URLParam(r, "id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

const (
	resetLinkTTL  = 24 * time.Hour // made by an administrator
	resetEmailTTL = time.Hour      // asked for by email
)

// forgotPassword emails a reset link. It always answers 204, so it can't be
// used to find out which addresses have accounts.
func (s *Server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &body) {
		return
	}
	if s.cfg.Mailer == nil {
		writeError(w, http.StatusNotFound, "this server can't send email; ask an administrator to reset your password")
		return
	}
	email, ip := strings.ToLower(strings.TrimSpace(body.Email)), clientIP(r)
	if s.byEmail.blocked("forgot:"+email) || s.byIP.blocked("forgot:"+ip) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.byEmail.fail("forgot:" + email) // counts requests, not failures
	s.byIP.fail("forgot:" + ip)
	if u, err := s.st.UserByEmail(r.Context(), email); err == nil && u.Provider == "local" && !u.Disabled {
		if tok, err := s.st.CreateReset(r.Context(), u.ID, resetEmailTTL); err == nil {
			link := s.baseURL(r) + "/reset/" + tok
			text := fmt.Sprintf("Someone asked to reset the password for your Pyro account (%s).\n\nOpen this link within an hour to choose a new one:\n%s\n\nIf it wasn't you, ignore this email; your password stays the same.\n", u.Email, link)
			if err := s.cfg.Mailer.Send(u.Email, "Reset your Pyro password", text); err != nil {
				s.log.Warn("sending reset email failed", "err", err)
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) previewReset(w http.ResponseWriter, r *http.Request) {
	email, err := s.st.ResetEmail(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		s.resetFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"email": email})
}

func (s *Server) resetFail(w http.ResponseWriter, err error) {
	if errors.Is(err, errResetGone) {
		writeError(w, http.StatusGone, err.Error())
		return
	}
	s.fail(w, err)
}

// resetPassword sets a new password from a reset link and signs in.
func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	u, err := s.st.UseReset(r.Context(), body.Token, body.Password)
	if err != nil {
		s.resetFail(w, err)
		return
	}
	if err := s.setSession(w, r, u); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.meView(r.Context(), u))
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !current(r).user.Admin {
			writeError(w, http.StatusForbidden, "only a server administrator can do this")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	u, err := s.st.Users(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// adminUpdateUser grants or removes administration and disables accounts.
// The server always keeps one active administrator.
func (s *Server) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Admin    *bool `json:"admin,omitempty"`
		Disabled *bool `json:"disabled,omitempty"`
	}
	if !decode(w, r, &body) {
		return
	}
	id := chi.URLParam(r, "id")
	target, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	losing := (body.Admin != nil && !*body.Admin) || (body.Disabled != nil && *body.Disabled)
	if losing && target.Admin && !target.Disabled {
		n, err := s.st.otherActiveAdmins(r.Context(), id)
		if err != nil {
			s.fail(w, err)
			return
		}
		if n == 0 {
			writeError(w, http.StatusBadRequest, errLastAdmin.Error())
			return
		}
	}
	if body.Admin != nil {
		if err := s.st.SetAdmin(r.Context(), id, *body.Admin); err != nil {
			s.fail(w, err)
			return
		}
	}
	if body.Disabled != nil {
		if err := s.st.SetDisabled(r.Context(), id, *body.Disabled); err != nil {
			s.fail(w, err)
			return
		}
	}
	u, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// adminResetLink makes a one-time password link for a local account, and
// emails it when the server can.
func (s *Server) adminResetLink(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	u, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	tok, err := s.st.CreateReset(r.Context(), id, resetLinkTTL)
	if err != nil {
		s.fail(w, err)
		return
	}
	link := s.baseURL(r) + "/reset/" + tok
	out := map[string]any{"url": link, "expires": time.Now().Add(resetLinkTTL), "emailed": false}
	if s.cfg.Mailer != nil {
		text := fmt.Sprintf("A Pyro administrator made a link for you to set a new password (%s).\n\nOpen it within a day:\n%s\n", u.Email, link)
		if err := s.cfg.Mailer.Send(u.Email, "Set your Pyro password", text); err == nil {
			out["emailed"] = true
		} else {
			s.log.Warn("sending reset email failed", "err", err)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, out)
}

// adminSettings shows how the server is set up (nothing secret).
func (s *Server) adminSettings(w http.ResponseWriter, r *http.Request) {
	providers := []map[string]any{}
	for _, p := range s.cfg.OIDC {
		providers = append(providers, map[string]any{"id": p.ID, "name": p.Name, "issuer": p.Issuer, "allowedDomains": p.AllowedDomains, "adminGroup": p.AdminGroup != ""})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":       version.Version,
		"publicURL":     s.cfg.PublicURL,
		"database":      s.cfg.DatabaseKind,
		"signupPolicy":  s.cfg.Signup,
		"signupDomains": s.cfg.SignupDomains,
		"maxLoads":      s.cfg.MaxLoads,
		"email":         s.cfg.Mailer != nil,
		"ldap":          s.cfg.LDAPURL,
		"oidc":          providers,
	})
}
