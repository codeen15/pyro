package server

import (
	"errors"
	"io"
	"net/http"

	"github.com/codeen15/pyro/internal/store"
	"github.com/codeen15/pyro/pkg/engine/model"
)

// maxDataFile bounds an uploaded data file.
const maxDataFile = 64 << 20

func (s *Server) listDataFiles(w http.ResponseWriter, r *http.Request) {
	files, err := s.ws.ListDataFiles()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, files)
}

// putDataFile stores the raw request body as data/<path>.
func (s *Server) putDataFile(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDataFile))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "data files are limited to 64 MB")
		return
	}
	p, err := s.ws.WriteDataFile(reqPath(r), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"path": p, "size": len(body)})
}

func (s *Server) previewData(w http.ResponseWriter, r *http.Request) {
	var body struct {
		File  string `json:"file"`
		Limit int    `json:"limit"`
		model.DataFormat
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Limit <= 0 || body.Limit > 100 {
		body.Limit = 5
	}
	t, total, err := s.ws.PreviewData(body.File, body.DataFormat, body.Limit)
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrInvalidPath):
		s.fail(w, err)
		return
	case err != nil:
		// A parse problem (no header, bad delimiter): the user's to fix.
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"columns": t.Columns, "variables": t.Variables, "rows": t.Rows, "total": total})
}
