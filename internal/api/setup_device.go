package api

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/hakanor/proxypilot/internal/certpage"
	"github.com/hakanor/proxypilot/internal/clients"
)

func (s *Server) deviceSetupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/devices/setup", s.handleDeviceSetup)
	mux.HandleFunc("GET /api/v1/devices/qr.png", s.handleQR)
	mux.HandleFunc("GET /api/v1/devices/clients", s.handleClients)
	mux.HandleFunc("PUT /api/v1/devices/clients/{ip}", s.handleRenameClient)
	mux.HandleFunc("PUT /api/v1/devices/lan", s.handleSetLAN)
}

// DeviceSetup is what a phone needs to reach the proxy (also `proxypilot setup device --json`).
type DeviceSetup struct {
	ProxyAddr string   `json:"proxy_addr"`
	Port      string   `json:"port"`
	LANIPs    []string `json:"lan_ips"`
	// Reachable is false when the proxy only listens on loopback: phones cannot connect.
	Reachable bool `json:"reachable"`
	// PageURL opens the certificate page directly (before the phone's proxy is set).
	PageURL string `json:"page_url,omitempty"`
	// Firewall is "on", "off" or "unknown" (macOS application firewall).
	Firewall string   `json:"firewall"`
	Warnings []string `json:"warnings,omitempty"`
	MagicURL string   `json:"magic_url"`
	// LANFixed: the listen address itself is on the network (--proxy-listen), so the
	// UI switch can't turn it off.
	LANFixed bool `json:"lan_fixed"`
}

// DeviceSetupFor computes reachability for a proxy listen address.
func DeviceSetupFor(proxyAddr string) DeviceSetup {
	host, port, _ := net.SplitHostPort(proxyAddr)
	ips := certpage.LANAddrs()
	ip := net.ParseIP(host)
	reachable := host == "" || (ip != nil && ip.IsUnspecified())
	if !reachable && ip != nil {
		for _, l := range ips {
			if l == host {
				reachable = true
			}
		}
	}
	d := DeviceSetup{ProxyAddr: proxyAddr, Port: port, LANIPs: ips, Reachable: reachable, Firewall: firewallState(), MagicURL: "http://" + certpage.Host}
	if d.LANIPs == nil {
		d.LANIPs = []string{}
	}
	if len(ips) > 0 {
		d.PageURL = "http://" + net.JoinHostPort(ips[0], port) + "/"
	}
	if !reachable {
		d.Warnings = append(d.Warnings, "listening_loopback")
	}
	if len(ips) == 0 {
		d.Warnings = append(d.Warnings, "no_lan")
	}
	if d.Firewall == "on" {
		d.Warnings = append(d.Warnings, "firewall")
	}
	return d
}

// firewallState reads the macOS application firewall setting (read-only).
func firewallState() string {
	if runtime.GOOS != "darwin" {
		return "unknown"
	}
	out, err := exec.Command("/usr/libexec/ApplicationFirewall/socketfilterfw", "--getglobalstate").Output()
	if err != nil {
		return "unknown"
	}
	s := strings.ToLower(string(out))
	switch {
	case strings.Contains(s, "enabled"):
		return "on"
	case strings.Contains(s, "disabled"):
		return "off"
	}
	return "unknown"
}

func (s *Server) handleDeviceSetup(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.deviceSetup())
}

func (s *Server) deviceSetup() DeviceSetup {
	d := DeviceSetupFor(s.deps.Status().ProxyAddr)
	if s.deps.LAN != nil {
		_, d.LANFixed = s.deps.LAN.LANState()
	}
	return d
}

// handleSetLAN turns listening on the network on or off: {"enabled": true}.
func (s *Server) handleSetLAN(w http.ResponseWriter, r *http.Request) {
	if s.deps.LAN == nil {
		writeError(w, http.StatusNotImplemented, "unsupported", "the proxy can't switch network access")
		return
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.deps.LAN.SetLAN(in.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, "listen_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.deviceSetup())
}

// handleQR renders ?text= as a PNG QR code (for the Setup page).
func (s *Server) handleQR(w http.ResponseWriter, r *http.Request) {
	text := r.URL.Query().Get("text")
	if text == "" || len(text) > 512 {
		writeError(w, http.StatusBadRequest, "bad_request", "text is required (max 512 chars)")
		return
	}
	png, err := qrcode.Encode(text, qrcode.Medium, 320)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

func (s *Server) handleClients(w http.ResponseWriter, _ *http.Request) {
	if s.deps.Clients == nil {
		writeJSON(w, http.StatusOK, map[string]any{"clients": []clients.Client{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": s.deps.Clients.List()})
}

// handleRenameClient: {"name": "Hakan's iPhone"}; "" restores the guessed name.
func (s *Server) handleRenameClient(w http.ResponseWriter, r *http.Request) {
	if s.deps.Clients == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "client tracking unavailable")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", `expected {"name": "..."}`)
		return
	}
	c, err := s.deps.Clients.Rename(r.PathValue("ip"), body.Name)
	if errors.Is(err, clients.ErrInvalidIP) {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}
