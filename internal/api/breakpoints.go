package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hakanor/proxypilot/internal/breakpoints"
	"github.com/hakanor/proxypilot/internal/store"
)

func (s *Server) breakpointRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/breakpoints", s.handleListBreakpoints)
	mux.HandleFunc("GET /api/v1/breakpoints/wait", s.handleWaitBreakpoint)
	mux.HandleFunc("GET /api/v1/breakpoints/{id}", s.handleGetBreakpoint)
	mux.HandleFunc("POST /api/v1/breakpoints/{id}/resume", s.handleResumeBreakpoint)
	mux.HandleFunc("POST /api/v1/breakpoints/{id}/extend", s.handleExtendBreakpoint)
	mux.HandleFunc("POST /api/v1/breakpoints/{id}/take-over", s.handleTakeOverBreakpoint)
}

func (s *Server) bpAvailable(w http.ResponseWriter) bool {
	if s.deps.Breakpoints == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "breakpoints unavailable")
		return false
	}
	return true
}

func bpError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, breakpoints.ErrAlreadyResolved):
		writeError(w, http.StatusConflict, "already_resolved", err.Error())
	case errors.Is(err, breakpoints.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, breakpoints.ErrInvalid):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

// redactPaused masks secrets in a copy when the client asks for it (CLI/MCP default).
func (s *Server) redactPaused(r *http.Request, p *breakpoints.Paused) *breakpoints.Paused {
	if !wantRedact(r) || p == nil {
		return p
	}
	rd := s.redactor()
	c := *p
	mask := func(hs []breakpoints.Header) []breakpoints.Header {
		out := make([]breakpoints.Header, len(hs))
		for i, h := range hs {
			out[i] = breakpoints.Header{Name: h.Name, Value: rd.HeaderValue(h.Name, h.Value)}
		}
		return out
	}
	c.Request.URL = rd.URL(c.Request.URL)
	c.Request.Headers = mask(c.Request.Headers)
	c.Request.Body = string(rd.Body([]byte(c.Request.Body), headerValue(c.Request.Headers, "Content-Type")))
	if c.Response != nil {
		resp := *c.Response
		resp.Headers = mask(resp.Headers)
		resp.Body = string(rd.Body([]byte(resp.Body), headerValue(resp.Headers, "Content-Type")))
		c.Response = &resp
	}
	return &c
}

func headerValue(hs []breakpoints.Header, name string) string {
	for _, h := range hs {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

func (s *Server) handleListBreakpoints(w http.ResponseWriter, r *http.Request) {
	if !s.bpAvailable(w) {
		return
	}
	list := s.deps.Breakpoints.List()
	for i, p := range list {
		list[i] = s.redactPaused(r, p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"breakpoints": list})
}

func (s *Server) handleGetBreakpoint(w http.ResponseWriter, r *http.Request) {
	if !s.bpAvailable(w) {
		return
	}
	p, err := s.deps.Breakpoints.Get(r.PathValue("id"))
	if err != nil {
		bpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.redactPaused(r, p))
}

// handleWaitBreakpoint long-polls: ?timeout=60s&url=<substring or * pattern>&phase=request|response.
// 200 with the paused flow, 408 {"error":{"code":"timeout"}} when nothing paused in time.
func (s *Server) handleWaitBreakpoint(w http.ResponseWriter, r *http.Request) {
	if !s.bpAvailable(w) {
		return
	}
	q := r.URL.Query()
	timeout := 60 * time.Second
	if v := q.Get("timeout"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "timeout must be a duration like 60s")
			return
		}
		timeout = min(d, 10*time.Minute)
	}
	var urlMatch func(string) bool
	if u := q.Get("url"); u != "" {
		if strings.Contains(u, "*") {
			urlMatch = store.MatchHost(u) // wildcard over the whole URL
		} else {
			urlMatch = func(s string) bool { return strings.Contains(s, u) }
		}
	}
	phase := q.Get("phase")
	ctx, cancel := contextWithTimeout(r, timeout)
	defer cancel()
	p, err := s.deps.Breakpoints.Wait(ctx, func(p *breakpoints.Paused) bool {
		return (urlMatch == nil || urlMatch(p.Request.URL)) && (phase == "" || p.Phase == phase)
	})
	if err != nil {
		writeError(w, http.StatusRequestTimeout, "timeout", "no breakpoint was hit within "+timeout.String())
		return
	}
	writeJSON(w, http.StatusOK, s.redactPaused(r, p))
}

func (s *Server) handleResumeBreakpoint(w http.ResponseWriter, r *http.Request) {
	if !s.bpAvailable(w) {
		return
	}
	var res breakpoints.Resolution
	dec := json.NewDecoder(io.LimitReader(r.Body, breakpoints.MaxBody*2))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&res); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", `expected {"action":"continue|modify|abort", "request":{…}|"response":{…}}: `+err.Error())
		return
	}
	res.TimedOut = false
	if res.By == "" {
		res.By = "user"
	}
	if err := s.deps.Breakpoints.Resume(r.PathValue("id"), res); err != nil {
		bpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resumed": true, "id": r.PathValue("id"), "action": res.Action})
}

// handleExtendBreakpoint: {"seconds": 30} pushes the deadline back.
func (s *Server) handleExtendBreakpoint(w http.ResponseWriter, r *http.Request) {
	if !s.bpAvailable(w) {
		return
	}
	body := struct {
		Seconds int `json:"seconds"`
	}{Seconds: 30}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&body)
	if body.Seconds <= 0 || body.Seconds > 1800 {
		writeError(w, http.StatusBadRequest, "bad_request", "seconds must be 1..1800")
		return
	}
	p, err := s.deps.Breakpoints.Extend(r.PathValue("id"), time.Duration(body.Seconds)*time.Second)
	if err != nil {
		bpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.redactPaused(r, p))
}

func (s *Server) handleTakeOverBreakpoint(w http.ResponseWriter, r *http.Request) {
	if !s.bpAvailable(w) {
		return
	}
	p, err := s.deps.Breakpoints.TakeOver(r.PathValue("id"), "user")
	if err != nil {
		bpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.redactPaused(r, p))
}

func contextWithTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}
