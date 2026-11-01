package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/pyrohq/pyro/pkg/engine/plugin"
)

const maxPluginArchive = 256 << 20

func (s *Server) plugins(w http.ResponseWriter) *plugin.Manager {
	if s.cfg.Engine == nil || s.cfg.Engine.Plugins == nil {
		writeError(w, http.StatusServiceUnavailable, "plugins are not available (the plugin directory couldn't be opened)")
		return nil
	}
	return s.cfg.Engine.Plugins
}

func (s *Server) listPlugins(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"plugins": []plugin.Info{}, "available": false, "indexConfigured": s.cfg.PluginIndex != ""}
	if s.cfg.Engine != nil && s.cfg.Engine.Plugins != nil {
		out["plugins"] = s.cfg.Engine.Plugins.List()
		out["available"] = true
		out["dir"] = s.cfg.Engine.Plugins.Dir()
	}
	writeJSON(w, http.StatusOK, out)
}

// installPlugin takes a plugin archive (.pyro-plugin / .zip) as the body.
// The plugin is installed but needs approval before it can run.
func (s *Server) installPlugin(w http.ResponseWriter, r *http.Request) {
	m := s.plugins(w)
	if m == nil {
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPluginArchive))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "plugin archive is larger than 256 MB")
		return
	}
	info, err := m.InstallArchive(data, false)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) pluginAction(w http.ResponseWriter, r *http.Request, f func(*plugin.Manager, string) (plugin.Info, error)) {
	m := s.plugins(w)
	if m == nil {
		return
	}
	info, err := f(m, chi.URLParam(r, "name"))
	if errors.Is(err, plugin.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) approvePlugin(w http.ResponseWriter, r *http.Request) {
	s.pluginAction(w, r, func(m *plugin.Manager, name string) (plugin.Info, error) { return m.Approve(name) })
}

func (s *Server) enablePlugin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &body) {
		return
	}
	s.pluginAction(w, r, func(m *plugin.Manager, name string) (plugin.Info, error) { return m.SetEnabled(name, body.Enabled) })
}

func (s *Server) configurePlugin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Config map[string]string `json:"config"`
	}
	if !decode(w, r, &body) {
		return
	}
	s.pluginAction(w, r, func(m *plugin.Manager, name string) (plugin.Info, error) { return m.SetConfig(name, body.Config) })
}

func (s *Server) removePlugin(w http.ResponseWriter, r *http.Request) {
	m := s.plugins(w)
	if m == nil {
		return
	}
	if err := m.Remove(chi.URLParam(r, "name")); errors.Is(err, plugin.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	} else if err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) searchPluginIndex(w http.ResponseWriter, r *http.Request) {
	if s.cfg.PluginIndex == "" {
		writeError(w, http.StatusNotFound, "no plugin index configured (set PYRO_PLUGIN_INDEX before starting pyro serve)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	idx, err := plugin.FetchIndex(ctx, s.cfg.PluginIndex)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, idx.Search(r.URL.Query().Get("q")))
}

func (s *Server) installFromIndex(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		Version string `json:"version,omitempty"`
	}
	if !decode(w, r, &body) {
		return
	}
	m := s.plugins(w)
	if m == nil {
		return
	}
	if s.cfg.PluginIndex == "" {
		writeError(w, http.StatusNotFound, "no plugin index configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	idx, err := plugin.FetchIndex(ctx, s.cfg.PluginIndex)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	e, ok := idx.Find(body.Name, body.Version)
	if !ok {
		writeError(w, http.StatusNotFound, "the index has no plugin "+body.Name)
		return
	}
	data, err := idx.Download(ctx, e)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	info, err := m.InstallArchive(data, false)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, info)
}
