package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/hakanor/proxypilot/internal/cdp"
)

func (s *Server) chromeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/chrome", s.handleChromeStatus)
	mux.HandleFunc("POST /api/v1/chrome/launch", s.handleChromeLaunch)
	mux.HandleFunc("POST /api/v1/chrome/attach", s.handleChromeAttach)
	mux.HandleFunc("POST /api/v1/chrome/detach", s.handleChromeDetach)
	mux.HandleFunc("PUT /api/v1/chrome/tabs/{id}", s.handleChromeTab)
}

func (s *Server) chromeAvailable(w http.ResponseWriter) bool {
	if s.deps.Chrome == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "Chrome support unavailable")
		return false
	}
	return true
}

func (s *Server) handleChromeStatus(w http.ResponseWriter, _ *http.Request) {
	if !s.chromeAvailable(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.deps.Chrome.Status())
}

func (s *Server) handleChromeLaunch(w http.ResponseWriter, r *http.Request) {
	if !s.chromeAvailable(w) {
		return
	}
	var in struct {
		URL string `json:"url"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in)
	st, err := s.deps.Chrome.Launch(r.Context(), in.URL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "chrome_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleChromeAttach(w http.ResponseWriter, r *http.Request) {
	if !s.chromeAvailable(w) {
		return
	}
	in := struct {
		Addr string `json:"addr"`
	}{Addr: "127.0.0.1:9222"}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in)
	st, err := s.deps.Chrome.Attach(r.Context(), in.Addr)
	if err != nil {
		writeError(w, http.StatusBadGateway, "chrome_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleChromeDetach(w http.ResponseWriter, _ *http.Request) {
	if !s.chromeAvailable(w) {
		return
	}
	s.deps.Chrome.Detach()
	writeJSON(w, http.StatusOK, s.deps.Chrome.Status())
}

// handleChromeTab: {"watch": false} stops capturing a tab.
func (s *Server) handleChromeTab(w http.ResponseWriter, r *http.Request) {
	if !s.chromeAvailable(w) {
		return
	}
	var in struct {
		Watch *bool `json:"watch"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&in); err != nil || in.Watch == nil {
		writeError(w, http.StatusBadRequest, "bad_request", `expected {"watch": true|false}`)
		return
	}
	t, err := s.deps.Chrome.SetWatch(r.Context(), r.PathValue("id"), *in.Watch)
	if err == cdp.ErrNotConnected {
		writeError(w, http.StatusConflict, "not_connected", err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}
