package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/hakanor/proxypilot/internal/proxy"
)

func (s *Server) throttleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/throttle", s.handleGetThrottle)
	mux.HandleFunc("PUT /api/v1/throttle", s.handlePutThrottle)
}

// ThrottleInfo is GET /api/v1/throttle: the current settings and the presets.
type ThrottleInfo struct {
	proxy.ThrottleSettings
	Profiles []proxy.ThrottleProfile `json:"profiles"`
}

func (s *Server) handleGetThrottle(w http.ResponseWriter, _ *http.Request) {
	if s.deps.Throttle == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "throttling unavailable")
		return
	}
	writeJSON(w, http.StatusOK, ThrottleInfo{s.deps.Throttle.Settings(), proxy.ThrottleProfiles})
}

// handlePutThrottle: {"enabled": true, "name": "3g", "hosts": ["*.example.com"]} uses a
// preset; name "custom" takes down_kbps, up_kbps and latency_ms. {"enabled": false} turns it off.
func (s *Server) handlePutThrottle(w http.ResponseWriter, r *http.Request) {
	if s.deps.Throttle == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "throttling unavailable")
		return
	}
	var in proxy.ThrottleSettings
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	if in.Enabled {
		switch p, ok := proxy.LookupThrottleProfile(in.Name); {
		case ok:
			in.ThrottleProfile = p
		case in.Name == "custom" || in.Name == "":
			in.Name = "custom"
			if in.DownKbps < 0 || in.UpKbps < 0 || in.LatencyMs < 0 || (in.DownKbps == 0 && in.UpKbps == 0 && in.LatencyMs == 0) {
				writeError(w, http.StatusBadRequest, "bad_request", "custom throttle needs down_kbps, up_kbps or latency_ms (all >= 0)")
				return
			}
		default:
			writeError(w, http.StatusBadRequest, "bad_request", "unknown profile "+in.Name+" (edge, 3g, lte, bad-wifi, custom)")
			return
		}
	} else if prev := s.deps.Throttle.Settings(); in.Name == "" {
		in.ThrottleProfile, in.Hosts = prev.ThrottleProfile, prev.Hosts // off keeps the last choice for next time
	}
	hosts := in.Hosts[:0]
	for _, h := range in.Hosts {
		if h = strings.TrimSpace(h); h != "" {
			hosts = append(hosts, h)
		}
	}
	in.Hosts = hosts
	s.deps.Throttle.Set(in)
	writeJSON(w, http.StatusOK, ThrottleInfo{s.deps.Throttle.Settings(), proxy.ThrottleProfiles})
}
