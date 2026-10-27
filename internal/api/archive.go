package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/hakanor/proxypilot/internal/archive"
)

func (s *Server) archiveRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/export/har", s.handleExportHAR)
	mux.HandleFunc("GET /api/v1/export/session", s.handleExportSession)
	// POST variants take {"ids": [...], "redact": bool} for selections too long for a URL.
	mux.HandleFunc("POST /api/v1/export/har", s.handleExportHAR)
	mux.HandleFunc("POST /api/v1/export/session", s.handleExportSession)
	mux.HandleFunc("POST /api/v1/import", s.handleImport)
}

// exportOptions reads list filters; redaction is on unless redact=false.
func (s *Server) exportOptions(w http.ResponseWriter, r *http.Request) (archive.ExportOptions, bool) {
	f, err := parseFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return archive.ExportOptions{}, false
	}
	o := archive.ExportOptions{Filter: f}
	redactOn := true
	if v := r.URL.Query().Get("redact"); v != "" {
		redactOn, _ = strconv.ParseBool(v)
	}
	if r.Method == http.MethodPost {
		var body struct {
			IDs    []string `json:"ids"`
			Redact *bool    `json:"redact"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "expected {\"ids\": [...], \"redact\": true}")
			return o, false
		}
		if len(body.IDs) > archive.MaxFlows {
			writeError(w, http.StatusBadRequest, "bad_request", "too many ids")
			return o, false
		}
		o.Filter.IDs = body.IDs
		if body.Redact != nil {
			redactOn = *body.Redact
		}
	}
	if redactOn {
		o.Redact = s.redactor()
	}
	return o, true
}

func stamp() string { return time.Now().Format("2006-01-02-150405") }

func (s *Server) handleExportHAR(w http.ResponseWriter, r *http.Request) {
	o, ok := s.exportOptions(w, r)
	if !ok {
		return
	}
	var buf bytes.Buffer // build first so a failure is still a JSON error
	if _, err := archive.ExportHAR(r.Context(), s.deps.Store, &buf, o); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="proxypilot-`+stamp()+`.har"`)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) handleExportSession(w http.ResponseWriter, r *http.Request) {
	o, ok := s.exportOptions(w, r)
	if !ok {
		return
	}
	var buf bytes.Buffer
	if _, err := archive.SaveSession(r.Context(), s.deps.Store, &buf, o); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="proxypilot-`+stamp()+`.proxypilot"`)
	_, _ = w.Write(buf.Bytes())
}

// handleImport accepts a HAR (JSON) or a .proxypilot session (zip) as the request body.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(io.LimitReader(r.Body, 2<<30))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	var res archive.ImportResult
	if bytes.HasPrefix(b, []byte("PK\x03\x04")) {
		res, err = archive.OpenSession(r.Context(), s.deps.Store, bytes.NewReader(b), int64(len(b)))
	} else {
		res, err = archive.ImportHAR(r.Context(), s.deps.Store, bytes.NewReader(b))
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_import", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
