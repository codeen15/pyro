package server

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/pyrohq/pyro/pkg/engine"
	"github.com/pyrohq/pyro/pkg/engine/importer/postman"
	"github.com/pyrohq/pyro/pkg/engine/model"
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
	if body.Path == "" && body.Request == nil {
		writeError(w, http.StatusBadRequest, "path or request is required")
		return
	}
	in := engine.SendInput{Request: body.Request, Runtime: body.Runtime}
	if body.Path != "" {
		chain, err := s.ws.Chain(body.Path)
		if err != nil {
			s.fail(w, err)
			return
		}
		in.Chain = chain
		if in.Request == nil {
			if in.Request, err = s.ws.ReadRequest(body.Path); err != nil {
				s.fail(w, err)
				return
			}
		}
	}
	if body.Environment != "" {
		env, err := s.ws.ReadEnvironment(body.Environment)
		if err != nil {
			s.fail(w, err)
			return
		}
		in.Environment = env
	}

	res := s.cfg.Engine.Send(r.Context(), in)
	out := sendResponse{Result: res}
	if s.cfg.History != nil && res.Sent != nil {
		id, err := s.cfg.History.Add(r.Context(), s.ws.Root, body.Path, in.Request.Name, res)
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
	list, err := s.cfg.History.List(r.Context(), s.ws.Root, limit)
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
	raw, err := s.cfg.History.Get(r.Context(), s.ws.Root, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func (s *Server) clearHistory(w http.ResponseWriter, r *http.Request) {
	if s.cfg.History != nil {
		if err := s.cfg.History.Clear(r.Context(), s.ws.Root); err != nil {
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
	bundle, err := postman.Parse(data)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	rep, err := s.ws.Import(bundle)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rep)
}
