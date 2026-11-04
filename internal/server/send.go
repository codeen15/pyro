package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/codeen15/pyro/pkg/engine"
	exportpm "github.com/codeen15/pyro/pkg/engine/exporter/postman"
	"github.com/codeen15/pyro/pkg/engine/importer"
	"github.com/codeen15/pyro/pkg/engine/importer/auto"
	"github.com/codeen15/pyro/pkg/engine/model"
)

const maxImportBytes = 50 << 20

func (s *Server) putCollection(w http.ResponseWriter, r *http.Request) {
	var c model.Collection
	if !decode(w, r, &c) {
		return
	}
	if err := s.ws.WriteCollection(chi.URLParam(r, "slug"), &c); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) deleteEnvironment(w http.ResponseWriter, r *http.Request) {
	if err := s.ws.DeleteEnvironment(chi.URLParam(r, "name")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type sendBody struct {
	// Path identifies a saved request; it also selects the collection and
	// folders whose variables and auth apply.
	Path string `json:"path,omitempty"`
	// Request, when set, is sent instead of the saved copy (unsaved edits).
	Request     *model.Request    `json:"request,omitempty"`
	Environment string            `json:"environment,omitempty"`
	Runtime     map[string]string `json:"runtime,omitempty"`
}

type sendResponse struct {
	*engine.Result
	HistoryID int64 `json:"historyId,omitempty"`
}

func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	var body sendBody
	if !decode(w, r, &body) {
		return
	}
	in, slug, ok := s.prepare(w, body.Path, body.Request, body.Environment)
	if !ok {
		return
	}
	bases := s.sessionFor(r).apply(in.Vars, slug)
	for k, v := range body.Runtime {
		in.Vars.Runtime[k] = v
	}
	socket := in.Request.Kind == model.KindWebSocket || in.Request.Kind == model.KindSocketIO
	if socket {
		if in.Request.SocketAction() == model.ActionConnect {
			writeError(w, http.StatusBadRequest, "connect requests open from the editor's Connect button (POST /sessions)")
			return
		}
		in.Conns = s.connsFor(r)
	}

	res := s.cfg.Engine.Send(r.Context(), in)
	s.sessionFor(r).record(in.Vars, slug, bases)
	if socket && res.Socket != nil {
		// Show what the step sent in its connection's console (received
		// messages are logged as they arrive).
		if ls := s.sessionByConn(principal(r).ID, res.Socket.Connection); ls != nil && !res.Socket.Own {
			for _, m := range res.Socket.Sent {
				s.hub.publish("session:"+ls.ID, "message", ls.record(m))
			}
		}
	}
	out := sendResponse{Result: res}
	if s.cfg.History != nil && res.Sent != nil && !socket {
		id, err := s.cfg.History.Add(r.Context(), s.userKey(r), body.Path, in.Request.Name, res)
		if err != nil {
			s.log.Warn("recording history failed", "err", err)
		}
		out.HistoryID = id
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listHistory(w http.ResponseWriter, r *http.Request) {
	if s.cfg.History == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.cfg.History.List(r.Context(), s.userKey(r), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getHistory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || s.cfg.History == nil {
		writeError(w, http.StatusNotFound, "history entry not found")
		return
	}
	raw, err := s.cfg.History.Get(r.Context(), s.userKey(r), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func (s *Server) clearHistory(w http.ResponseWriter, r *http.Request) {
	if s.cfg.History != nil {
		if err := s.cfg.History.Clear(r.Context(), s.userKey(r)); err != nil {
			s.fail(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// importFile accepts a Postman collection or environment export as the raw
// request body and writes it into the workspace.
func (s *Server) importFile(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImportBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "import file is larger than 50 MB")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	bundle, err := auto.Parse(r.URL.Query().Get("filename"), data)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	var rep *importer.Report
	if into := r.URL.Query().Get("into"); into != "" {
		rep, err = s.ws.ImportInto(into, bundle)
	} else {
		rep, err = s.ws.Import(bundle)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rep)
}

type exportView struct {
	Filename string           `json:"filename"`
	Content  json.RawMessage  `json:"content"`
	Report   *importer.Report `json:"report,omitempty"`
}

// exportCollection returns a collection as Postman v2.1 JSON.
func (s *Server) exportCollection(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	b, err := s.ws.Snapshot(slug)
	if err != nil {
		s.fail(w, err)
		return
	}
	out, rep, err := exportpm.Export(b)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, exportView{Filename: slug + ".postman_collection.json", Content: out, Report: rep})
}

// exportEnvironment returns an environment as Postman environment JSON.
func (s *Server) exportEnvironment(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	env, err := s.ws.ReadEnvironment(name)
	if err != nil {
		s.fail(w, err)
		return
	}
	if env.Name == "" {
		env.Name = name
	}
	out, err := exportpm.ExportEnvironment(env)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, exportView{Filename: name + ".postman_environment.json", Content: out})
}
