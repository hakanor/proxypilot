package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/hakanor/proxypilot/internal/repeat"
	"github.com/hakanor/proxypilot/internal/store"
)

func (s *Server) repeatRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/flows/{id}/repeat", s.handleRepeat)
	mux.HandleFunc("GET /api/v1/flows/{id}/request", s.handleEditableRequest)
	mux.HandleFunc("GET /api/v1/flows/{id}/curl", s.handleFlowCurl)
	mux.HandleFunc("POST /api/v1/compose", s.handleCompose)
	mux.HandleFunc("POST /api/v1/curl/parse", s.handleParseCurl)
}

func (s *Server) repeatAvailable(w http.ResponseWriter) bool {
	if s.deps.Repeater == nil || s.deps.Repeater.Proxy == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "repeat unavailable")
		return false
	}
	return true
}

func repeatError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "flow not found")
	case errors.Is(err, repeat.ErrLimit):
		writeError(w, http.StatusBadRequest, "limit", err.Error())
	default:
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	}
}

// RepeatRequest is the body of POST /flows/{id}/repeat.
type RepeatRequest struct {
	repeat.Edits
	repeat.Options
}

func (s *Server) handleRepeat(w http.ResponseWriter, r *http.Request) {
	if !s.repeatAvailable(w) {
		return
	}
	var in RepeatRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON: "+err.Error())
			return
		}
	}
	rp := s.deps.Repeater
	id := r.PathValue("id")
	base, err := rp.FromFlow(r.Context(), id)
	if err != nil {
		repeatError(w, err)
		return
	}
	req, err := in.Edits.Apply(base)
	if err != nil {
		repeatError(w, err)
		return
	}
	sum, err := rp.Send(r.Context(), req, in.Options, map[string]string{"repeat_of": id})
	if err != nil {
		repeatError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

// handleEditableRequest returns a flow's request in composer form (never redacted:
// it is meant to be edited and sent again).
func (s *Server) handleEditableRequest(w http.ResponseWriter, r *http.Request) {
	if !s.repeatAvailable(w) {
		return
	}
	req, err := s.deps.Repeater.FromFlow(r.Context(), r.PathValue("id"))
	if err != nil {
		repeatError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

// handleFlowCurl: ?redact=true masks secrets (for sharing).
func (s *Server) handleFlowCurl(w http.ResponseWriter, r *http.Request) {
	if !s.repeatAvailable(w) {
		return
	}
	req, err := s.deps.Repeater.FromFlow(r.Context(), r.PathValue("id"))
	if err != nil {
		repeatError(w, err)
		return
	}
	if wantRedact(r) {
		rd := s.redactor()
		req.URL = rd.URL(req.URL)
		req.Headers = rd.Headers(req.Headers)
		ct := ""
		for _, h := range req.Headers {
			if http.CanonicalHeaderKey(h.Name) == "Content-Type" {
				ct = h.Value
			}
		}
		req.Body = string(rd.Body([]byte(req.Body), ct))
	}
	cmd, binary := repeat.Curl(req)
	writeJSON(w, http.StatusOK, map[string]any{"curl": cmd, "binary_body": binary})
}

// ComposeRequest is the body of POST /compose.
type ComposeRequest struct {
	repeat.Request
	repeat.Options
	// Curl, when set, is parsed instead of the request fields.
	Curl string `json:"curl,omitempty"`
}

func (s *Server) handleCompose(w http.ResponseWriter, r *http.Request) {
	if !s.repeatAvailable(w) {
		return
	}
	var in ComposeRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON: "+err.Error())
		return
	}
	req := in.Request
	if in.Curl != "" {
		var err error
		if req, err = repeat.ParseCurl(in.Curl); err != nil {
			repeatError(w, err)
			return
		}
	}
	sum, err := s.deps.Repeater.Send(r.Context(), req, in.Options, nil)
	if err != nil {
		repeatError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func (s *Server) handleParseCurl(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Curl string `json:"curl"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	req, err := repeat.ParseCurl(in.Curl)
	if err != nil {
		repeatError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}
