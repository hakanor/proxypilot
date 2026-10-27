package api

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"

	"github.com/hakanor/proxypilot/internal/certpage"
	"github.com/hakanor/proxypilot/internal/devices/android"
)

func (s *Server) androidRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/devices/android", s.handleAndroidList)
	mux.HandleFunc("POST /api/v1/devices/android/setup", s.handleAndroidSetup)
	mux.HandleFunc("POST /api/v1/devices/android/remove", s.handleAndroidRemove)
}

func androidError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, android.ErrNoADB):
		writeError(w, http.StatusFailedDependency, "no_adb", err.Error())
	case errors.Is(err, android.ErrNoDevice):
		writeError(w, http.StatusNotFound, "no_device", err.Error())
	case errors.Is(err, android.ErrMany):
		writeError(w, http.StatusBadRequest, "pick_device", err.Error())
	case errors.Is(err, android.ErrNoRoot):
		writeError(w, http.StatusConflict, "no_root", err.Error())
	default:
		writeError(w, http.StatusBadGateway, "adb_failed", err.Error())
	}
}

func (s *Server) proxyPort() (string, string) {
	host, port, _ := net.SplitHostPort(s.deps.Status().ProxyAddr)
	return host, port
}

func (s *Server) handleAndroidList(w http.ResponseWriter, r *http.Request) {
	if !s.devicesAvailable(w) {
		return
	}
	dev := s.deps.Devices
	notes := map[string]string{"network_security_config": android.DevNote, "flutter": android.FlutterNote}
	a, err := dev.Android()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"adb": "", "error": err.Error(), "devices": []android.Device{}, "notes": notes})
		return
	}
	_, port := s.proxyPort()
	list, err := a.Devices(r.Context(), port, dev.AndroidHash())
	if err != nil {
		androidError(w, err)
		return
	}
	if list == nil {
		list = []android.Device{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"adb": a.Path, "devices": list, "notes": notes, "port": port})
}

func (s *Server) handleAndroidSetup(w http.ResponseWriter, r *http.Request) {
	if !s.devicesAvailable(w) {
		return
	}
	var req struct {
		Serial string `json:"serial"`
		Mode   string `json:"mode"`
		CA     string `json:"ca"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	a, err := s.deps.Devices.Android()
	if err != nil {
		androidError(w, err)
		return
	}
	host, port := s.proxyPort()
	if req.Mode == android.ModeWiFi && (host == "127.0.0.1" || host == "localhost") {
		writeError(w, http.StatusBadRequest, "listening_loopback", "wifi mode needs the proxy on all interfaces (--proxy-listen 0.0.0.0:"+port+")")
		return
	}
	o := s.deps.Devices.AndroidOptions(android.SetupOptions{Serial: req.Serial, Mode: req.Mode, CA: req.CA, Port: port})
	if ips := certpage.LANAddrs(); len(ips) > 0 {
		o.LANIP = ips[0]
	}
	res, err := a.Setup(r.Context(), o)
	if err != nil {
		androidError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleAndroidRemove(w http.ResponseWriter, r *http.Request) {
	if !s.devicesAvailable(w) {
		return
	}
	var req struct {
		Serial string `json:"serial"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&req)
	a, err := s.deps.Devices.Android()
	if err != nil {
		androidError(w, err)
		return
	}
	_, port := s.proxyPort()
	res, err := a.Remove(r.Context(), req.Serial, port)
	if err != nil {
		androidError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
