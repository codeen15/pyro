package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/codeen15/pyro/pkg/engine/docs"
	"github.com/codeen15/pyro/pkg/engine/importer"
	"github.com/codeen15/pyro/pkg/engine/mock"
	"github.com/codeen15/pyro/pkg/engine/vars"
)

// Mock servers started from the UI, one per collection, on loopback.

type runningMock struct {
	slug string
	url  string
	srv  *http.Server
	mock *mock.Server

	mu   sync.Mutex
	hits []mock.Hit // newest last, at most 200
}

type mockRegistry struct {
	mu    sync.Mutex
	bySlg map[string]*runningMock
}

type mockView struct {
	Collection string       `json:"collection"`
	Running    bool         `json:"running"`
	URL        string       `json:"url,omitempty"`
	Routes     []mock.Route `json:"routes,omitempty"`
	Hits       []mock.Hit   `json:"hits,omitempty"`
}

// mockSource reads a collection for the mock: its requests and the
// variables (globals, then collection) that resolve a leading {{baseUrl}}.
func (s *Server) mockSource(slug string) func() (*importer.Bundle, map[string]string, error) {
	return func() (*importer.Bundle, map[string]string, error) {
		b, err := s.ws.Snapshot(slug)
		if err != nil {
			return nil, nil, err
		}
		v := s.ws.GlobalVars()
		for k, val := range vars.Map(b.Collection.Variables) {
			v[k] = val
		}
		return b, v, nil
	}
}

func (s *Server) listMocks(w http.ResponseWriter, r *http.Request) {
	s.mocks.mu.Lock()
	out := make([]mockView, 0, len(s.mocks.bySlg))
	for _, m := range s.mocks.bySlg {
		out = append(out, mockView{Collection: m.slug, Running: true, URL: m.url})
	}
	s.mocks.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Collection < out[j].Collection })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) startMock(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Collection string `json:"collection"`
		Port       int    `json:"port,omitempty"`
		DelayMS    int    `json:"delayMs,omitempty"`
	}
	if !decode(w, r, &body) {
		return
	}
	if _, err := s.ws.ReadCollection(body.Collection); err != nil {
		s.fail(w, err)
		return
	}
	s.mocks.mu.Lock()
	defer s.mocks.mu.Unlock()
	if m := s.mocks.bySlg[body.Collection]; m != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a mock for this collection is already running", "url": m.url})
		return
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", body.Port))
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	m := &runningMock{slug: body.Collection, url: "http://" + ln.Addr().String()}
	m.mock = &mock.Server{Source: s.mockSource(body.Collection), Delay: time.Duration(body.DelayMS) * time.Millisecond, OnHit: func(h mock.Hit) {
		m.mu.Lock()
		m.hits = append(m.hits, h)
		if len(m.hits) > 200 {
			m.hits = m.hits[len(m.hits)-200:]
		}
		m.mu.Unlock()
	}}
	m.srv = &http.Server{Handler: m.mock, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := m.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Warn("mock server stopped", "collection", m.slug, "err", err)
		}
	}()
	s.mocks.bySlg[body.Collection] = m
	writeJSON(w, http.StatusCreated, mockView{Collection: m.slug, Running: true, URL: m.url})
}

func (s *Server) getMock(w http.ResponseWriter, r *http.Request) {
	s.mocks.mu.Lock()
	m := s.mocks.bySlg[chi.URLParam(r, "slug")]
	s.mocks.mu.Unlock()
	if m == nil {
		writeJSON(w, http.StatusOK, mockView{Collection: chi.URLParam(r, "slug")})
		return
	}
	routes, err := m.mock.Routes()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	m.mu.Lock()
	hits := append([]mock.Hit(nil), m.hits...)
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, mockView{Collection: m.slug, Running: true, URL: m.url, Routes: routes, Hits: hits})
}

func (s *Server) stopMock(w http.ResponseWriter, r *http.Request) {
	s.mocks.mu.Lock()
	m := s.mocks.bySlg[chi.URLParam(r, "slug")]
	delete(s.mocks.bySlg, chi.URLParam(r, "slug"))
	s.mocks.mu.Unlock()
	if m == nil {
		writeError(w, http.StatusNotFound, "no mock running for this collection")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	m.srv.Shutdown(ctx)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) closeMocks() {
	s.mocks.mu.Lock()
	defer s.mocks.mu.Unlock()
	for slug, m := range s.mocks.bySlg {
		m.srv.Close()
		delete(s.mocks.bySlg, slug)
	}
}

// collectionDocs renders the collection's HTML API reference.
func (s *Server) collectionDocs(w http.ResponseWriter, r *http.Request) {
	b, err := s.ws.Snapshot(chi.URLParam(r, "slug"))
	if err != nil {
		s.fail(w, err)
		return
	}
	page, err := docs.Render(b)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.URL.Query().Get("download") != "" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+chi.URLParam(r, "slug")+`-api.html"`)
	}
	w.Write(page)
}
