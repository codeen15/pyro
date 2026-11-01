package server

import (
	"net/http"

	"github.com/codeen15/pyro/pkg/engine/protocol/httpdriver"
)

// The interactive sender's cookie jar: cookies servers set while you use
// the UI, kept in memory while pyro serve runs. Collection runs and load
// tests use their own jars (one per VU).

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
