// Package certpage is the phone-first certificate install page the proxy serves to
// direct requests (http://<lan-ip>:9090/) and to http://proxypilot.cert through the proxy.
package certpage

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/hakanor/proxypilot/internal/brand"
	"html/template"
	"net"
	"net/http"
	"strings"

	"github.com/hakanor/proxypilot/internal/ca"
	"github.com/hakanor/proxypilot/internal/clients"
)

// Host is the magic host name the proxy answers itself.
const Host = "proxypilot.cert"

//go:embed page.html locales/*.json
var files embed.FS

// guideJS is the animated phone setup slideshow, built from web/src/certguide
// (`cd web && npm run build`).
//
//go:embed guide/guide.js
var guideJS []byte

type Handler struct {
	CA      *ca.RootCA
	Clients *clients.Tracker
	// ProxyAddr returns the listening proxy address (host:port).
	ProxyAddr func() string

	tmpl *template.Template
	en   map[string]any
}

func New(root *ca.RootCA, tracker *clients.Tracker, proxyAddr func() string) *Handler {
	h := &Handler{CA: root, Clients: tracker, ProxyAddr: proxyAddr}
	b, _ := files.ReadFile("locales/en.json")
	_ = json.Unmarshal(b, &h.en)
	h.tmpl = template.Must(template.New("page.html").Funcs(template.FuncMap{"inc": func(i int) int { return i + 1 }}).ParseFS(files, "page.html"))
	return h
}

// Platform guesses ios, android or other from a User-Agent.
func Platform(ua string) string {
	l := strings.ToLower(ua)
	switch {
	case strings.Contains(l, "iphone"), strings.Contains(l, "ipad"), strings.Contains(l, "ipod"):
		return "ios"
	case strings.Contains(l, "android"):
		return "android"
	}
	return "other"
}

// viaProxy reports whether r came through the proxy (absolute URL to the magic host).
func viaProxy(r *http.Request) bool {
	h := r.URL.Hostname()
	if h == "" {
		h, _, _ = net.SplitHostPort(r.Host)
	}
	return strings.EqualFold(h, Host) || strings.EqualFold(r.Host, Host)
}

func (h *Handler) str(k string) string {
	s, _ := h.en[k].(string)
	return s
}

func (h *Handler) list(k string, repl *strings.Replacer) []string {
	raw, _ := h.en[k].([]any)
	out := make([]string, len(raw))
	for i, v := range raw {
		s, _ := v.(string)
		out[i] = repl.Replace(s)
	}
	return out
}

// lanHost picks the address the phone should use: the one it reached us on, else a LAN IP.
func (h *Handler) lanHost(r *http.Request) (string, string) {
	addr := h.ProxyAddr()
	_, port, _ := net.SplitHostPort(addr)
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	if host == "" || strings.EqualFold(host, Host) || net.ParseIP(host) == nil || net.ParseIP(host).IsLoopback() {
		if ips := LANAddrs(); len(ips) > 0 {
			host = ips[0]
		}
	}
	return host, port
}

func (h *Handler) status(r *http.Request) map[string]any {
	c, _ := h.Clients.Get(r.RemoteAddr)
	proxied := viaProxy(r) || c.Requests > 0
	return map[string]any{"proxied": proxied, "tls": c.TLS, "rejected_host": c.RejectedHost, "name": c.Name}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.URL.Path {
	case "/status":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(h.status(r))
	case "/ca.pem":
		download(w, "application/x-pem-file", "proxypilot-ca.pem", h.CA.ExportPEM())
	case "/ca.crt", "/ca.der", "/ca.cer":
		download(w, "application/x-x509-ca-cert", "proxypilot-ca.crt", h.CA.ExportDER())
	case "/ca.mobileconfig":
		download(w, "application/x-apple-aspen-config", "ProxyPilot.mobileconfig", MobileConfig(h.CA))
	case "/icon.svg", "/favicon.ico":
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write(brand.IconSVG)
	case "/guide.js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(guideJS)
	case "/", "/index.html":
		h.page(w, r)
	default:
		http.NotFound(w, r)
	}
}

func download(w http.ResponseWriter, ct, name string, b []byte) {
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write(b)
}

func (h *Handler) page(w http.ResponseWriter, r *http.Request) {
	host, port := h.lanHost(r)
	repl := strings.NewReplacer("{host}", host, "{port}", port, "{addr}", net.JoinHostPort(host, port), "{cn}", h.CA.Certificate().Subject.CommonName, "{fp}", h.CA.Fingerprint())
	js := map[string]string{}
	for _, k := range []string{"checkProxy", "checkProxyTodo", "checkTrust", "checkTrustTodo", "checkTrustRejected"} {
		js[k] = repl.Replace(h.str(k))
	}
	t := map[string]string{}
	for k := range h.en {
		t[k] = repl.Replace(h.str(k))
	}
	data := map[string]any{
		"T":          t,
		"ProxyAddr":  net.JoinHostPort(host, port),
		"Host":       host,
		"Port":       port,
		"ProxyFirst": t["proxyFirst"],
		"Footer":     t["footer"],
		"Platform":   Platform(r.UserAgent()),
		"Steps":      map[string][]string{"ios": h.list("iosSteps", repl), "android": h.list("androidSteps", repl), "other": h.list("otherSteps", repl)},
		"JS":         js,
		"Initial":    h.status(r),
	}
	var buf bytes.Buffer
	if err := h.tmpl.Execute(&buf, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self'")
	_, _ = w.Write(buf.Bytes())
}

// uuid derives a stable UUID from the CA so reinstalling replaces the same profile.
func uuid(seed []byte, salt string) string {
	s := sha256.Sum256(append(append([]byte{}, seed...), salt...))
	return fmt.Sprintf("%X-%X-%X-%X-%X", s[0:4], s[4:6], s[6:8], s[8:10], s[10:16])
}

// MobileConfig is an unsigned iOS configuration profile with the root CA.
func MobileConfig(root *ca.RootCA) []byte {
	der := root.ExportDER()
	cn := root.Certificate().Subject.CommonName
	b64 := base64.StdEncoding.EncodeToString(der)
	var lines []string
	for len(b64) > 64 {
		lines = append(lines, b64[:64])
		b64 = b64[64:]
	}
	lines = append(lines, b64)
	esc := template.HTMLEscapeString
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadCertificateFileName</key>
			<string>proxypilot-ca.cer</string>
			<key>PayloadContent</key>
			<data>
			` + strings.Join(lines, "\n\t\t\t") + `
			</data>
			<key>PayloadDescription</key>
			<string>Adds the ProxyPilot root certificate</string>
			<key>PayloadDisplayName</key>
			<string>` + esc(cn) + `</string>
			<key>PayloadIdentifier</key>
			<string>dev.proxypilot.ca.cert</string>
			<key>PayloadType</key>
			<string>com.apple.security.root</string>
			<key>PayloadUUID</key>
			<string>` + uuid(der, "cert") + `</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
		</dict>
	</array>
	<key>PayloadDescription</key>
	<string>Lets ProxyPilot decrypt HTTPS traffic from this device for debugging. Remove it when you are done.</string>
	<key>PayloadDisplayName</key>
	<string>` + esc(cn) + `</string>
	<key>PayloadIdentifier</key>
	<string>dev.proxypilot.ca</string>
	<key>PayloadRemovalDisallowed</key>
	<false/>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>` + uuid(der, "profile") + `</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>
`)
}

// LANAddrs lists this machine's private IPv4 addresses (Wi-Fi / Ethernet first).
func LANAddrs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var primary, other []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || !ipn.IP.IsPrivate() {
				continue
			}
			if strings.HasPrefix(ifc.Name, "en") {
				primary = append(primary, ipn.IP.String())
			} else {
				other = append(other, ipn.IP.String())
			}
		}
	}
	return append(primary, other...)
}
