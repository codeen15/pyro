package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/codeen15/pyro/pkg/engine/protocol/httpdriver"
)

// The interactive sender's cookie jar: cookies servers set while you use
// the UI, saved per workspace in ~/.pyro/pyro.db so they survive restarts.
// Collection runs and load tests use their own jars (one per VU).

func (s *Server) jar(w http.ResponseWriter) *httpdriver.Jar {
	if s.cfg.Engine == nil || s.cfg.Engine.HTTP == nil || s.cfg.Engine.HTTP.Jar == nil {
		writeError(w, http.StatusServiceUnavailable, "no cookie jar")
		return nil
	}
	return s.cfg.Engine.HTTP.Jar
}

func (s *Server) listCookies(w http.ResponseWriter, r *http.Request) {
	if j := s.jar(w); j != nil {
		writeJSON(w, http.StatusOK, j.List())
	}
}

func (s *Server) putCookie(w http.ResponseWriter, r *http.Request) {
	j := s.jar(w)
	if j == nil {
		return
	}
	var c httpdriver.Cookie
	if !decode(w, r, &c) {
		return
	}
	if c.Domain == "" || c.Name == "" {
		writeError(w, http.StatusBadRequest, "domain and name are required")
		return
	}
	j.Put(c)
	writeJSON(w, http.StatusOK, j.List())
}

// deleteCookies removes one cookie (domain, path, name), a domain's
// cookies (domain only) or all of them (no parameters).
func (s *Server) deleteCookies(w http.ResponseWriter, r *http.Request) {
	j := s.jar(w)
	if j == nil {
		return
	}
	q := r.URL.Query()
	n := j.Delete(q.Get("domain"), q.Get("path"), q.Get("name"))
	writeJSON(w, http.StatusOK, map[string]int{"deleted": n})
}

// persistCookies loads the workspace's saved cookies into the interactive
// jar and saves them again (debounced) whenever they change.
func (s *Server) persistCookies() {
	h, eng, ws := s.cfg.History, s.cfg.Engine, s.ws
	if h == nil || eng == nil || eng.HTTP == nil || eng.HTTP.Jar == nil || ws == nil {
		return
	}
	jar := eng.HTTP.Jar
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if data, err := h.LoadCookies(ctx, ws.Root); err != nil {
		s.log.Warn("loading saved cookies failed", "err", err)
	} else if data != nil {
		var saved []httpdriver.Cookie
		if err := json.Unmarshal(data, &saved); err == nil {
			jar.Load(saved)
		}
	}
	s.cookieSave = func() {
		data, err := json.Marshal(jar.List())
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.SaveCookies(ctx, ws.Root, data); err != nil {
			s.log.Warn("saving cookies failed", "err", err)
		}
	}
	jar.OnChange = func() {
		s.cookieMu.Lock()
		defer s.cookieMu.Unlock()
		if s.cookieTimer == nil {
			s.cookieTimer = time.AfterFunc(500*time.Millisecond, func() {
				s.cookieMu.Lock()
				s.cookieTimer = nil
				s.cookieMu.Unlock()
				s.cookieSave()
			})
		}
	}
}

// flushCookies saves a pending change now (on shutdown).
func (s *Server) flushCookies() {
	s.cookieMu.Lock()
	t := s.cookieTimer
	s.cookieTimer = nil
	s.cookieMu.Unlock()
	if t != nil && t.Stop() && s.cookieSave != nil {
		s.cookieSave()
	}
}
