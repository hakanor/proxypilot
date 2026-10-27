package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/hakanor/proxypilot/internal/devices/execx"
	"github.com/hakanor/proxypilot/internal/devices/macproxy"
	"github.com/hakanor/proxypilot/internal/devices/simctl"
)

func (s *Server) deviceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/devices/simulators", s.handleSimulators)
	mux.HandleFunc("POST /api/v1/devices/simulators/install-ca", s.handleInstallCA)
	mux.HandleFunc("POST /api/v1/devices/simulators/{udid}/install-ca", s.handleInstallCA)
	mux.HandleFunc("GET /api/v1/system-proxy", s.handleSystemProxy)
	mux.HandleFunc("PUT /api/v1/system-proxy", s.handlePutSystemProxy)
	mux.HandleFunc("GET /api/v1/uninstall", s.handleUninstallPlan)
	mux.HandleFunc("POST /api/v1/uninstall", s.handleUninstall)
}

func (s *Server) devicesAvailable(w http.ResponseWriter) bool {
	if s.deps.Devices == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "device setup unavailable")
		return false
	}
	return true
}

func deviceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, simctl.ErrNoXcode):
		writeError(w, http.StatusFailedDependency, "no_xcode", err.Error())
	case errors.Is(err, macproxy.ErrNotMac):
		writeError(w, http.StatusNotImplemented, "not_mac", err.Error())
	default:
		writeError(w, http.StatusBadGateway, "command_failed", err.Error())
	}
}

func (s *Server) handleSimulators(w http.ResponseWriter, r *http.Request) {
	if !s.devicesAvailable(w) {
		return
	}
	d := s.deps.Devices
	list, err := d.Sim.List(r.Context())
	if err != nil {
		deviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"simulators":      list,
		"ca_path":         d.CA.CertPath(),
		"install_command": simctl.InstallCommand("<udid>", d.CA.CertPath()),
	})
}

// handleInstallCA: /{udid}/install-ca for one simulator, /install-ca for every booted one.
func (s *Server) handleInstallCA(w http.ResponseWriter, r *http.Request) {
	if !s.devicesAvailable(w) {
		return
	}
	var udids []string
	if u := r.PathValue("udid"); u != "" {
		udids = []string{u}
	}
	res, err := s.deps.Devices.InstallCA(r.Context(), udids...)
	if err != nil {
		deviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": res})
}

func (s *Server) proxyTarget() string { return s.deps.Status().ProxyAddr }

func quoteAll(cmds [][]string) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = execx.Quote(c[0], c[1:]...)
	}
	return out
}

func (s *Server) systemProxyBody(r *http.Request) (map[string]any, error) {
	m := s.deps.Devices.Proxy
	st, err := m.Status(r.Context(), s.proxyTarget())
	if err != nil {
		return nil, err
	}
	names := make([]string, len(st.Services))
	for i, svc := range st.Services {
		names[i] = svc.Name
	}
	body := map[string]any{"status": st, "on_commands": quoteAll(m.Commands(names, s.proxyTarget(), s.opts.Addr))}
	if cmds, err := m.RestoreCommands(); err == nil {
		body["off_commands"] = quoteAll(cmds)
	}
	return body, nil
}

func (s *Server) handleSystemProxy(w http.ResponseWriter, r *http.Request) {
	if !s.devicesAvailable(w) {
		return
	}
	body, err := s.systemProxyBody(r)
	if err != nil {
		deviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// handlePutSystemProxy: {"enabled": true} points the Mac at the proxy, false restores the backup.
func (s *Server) handlePutSystemProxy(w http.ResponseWriter, r *http.Request) {
	if !s.devicesAvailable(w) {
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&req); err != nil || req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "bad_request", `expected {"enabled": true|false}`)
		return
	}
	m := s.deps.Devices.Proxy
	var err error
	if *req.Enabled {
		_, err = m.On(r.Context(), s.proxyTarget(), s.opts.Addr)
	} else {
		err = m.Off(r.Context())
	}
	switch {
	case errors.Is(err, macproxy.ErrNeedsAdmin):
		body, _ := s.systemProxyBody(r)
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error":   map[string]string{"code": "needs_admin", "message": err.Error()},
			"details": body,
		})
		return
	case errors.Is(err, macproxy.ErrNoBackup):
		writeError(w, http.StatusConflict, "not_managed", err.Error())
		return
	case err != nil:
		deviceError(w, err)
		return
	}
	s.handleSystemProxy(w, r)
}

func (s *Server) handleUninstallPlan(w http.ResponseWriter, r *http.Request) {
	if !s.devicesAvailable(w) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"steps": s.deps.Devices.Plan(r.Context())})
}

// handleUninstall runs the steps that need no admin rights (system proxy, simulators).
func (s *Server) handleUninstall(w http.ResponseWriter, r *http.Request) {
	if !s.devicesAvailable(w) {
		return
	}
	res := s.deps.Devices.Clean(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"results": res, "steps": s.deps.Devices.Plan(r.Context())})
}
