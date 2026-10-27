package api

import (
	"github.com/hakanor/proxypilot/internal/cdp"

	"github.com/hakanor/proxypilot/internal/repeat"

	"github.com/hakanor/proxypilot/internal/clients"

	"github.com/hakanor/proxypilot/internal/breakpoints"

	"github.com/hakanor/proxypilot/internal/rules"

	"github.com/hakanor/proxypilot/internal/devices"

	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hakanor/proxypilot/internal/ca"
	"github.com/hakanor/proxypilot/internal/events"
	"github.com/hakanor/proxypilot/internal/proxy"
	"github.com/hakanor/proxypilot/internal/redact"
	"github.com/hakanor/proxypilot/internal/store"
)

const cookieName = "proxypilot_token"

// Deps are the daemon components the API exposes.
type Deps struct {
	Store       *store.Store
	Bus         *events.Bus
	CA          *ca.RootCA
	Policy      *proxy.SSLPolicy
	Token       string
	Log         *slog.Logger
	Status      func() StatusInfo    // live daemon status
	Devices     *devices.Service     // nil: device setup endpoints return 501
	Rules       *rules.Engine        // nil: rule endpoints return 501
	Breakpoints *breakpoints.Manager // nil: breakpoint endpoints return 501
	Clients     *clients.Tracker     // device names and HTTPS state
	Repeater    *repeat.Repeater     // its Proxy is set once the proxy exists
	Chrome      *cdp.Manager         // Chrome DevTools capture
	DataDir     string               // where Map Local copies are written
	// Recorder pauses and resumes recording (nil: always recording).
	Recorder interface {
		Recording() bool
		SetRecording(bool)
	}
	// LAN opens the proxy to phones on the network while it runs (nil: not switchable).
	LAN interface {
		LANState() (on, fixed bool)
		SetLAN(on bool) error
	}
	// NoCache is the "No caching" switch (nil: not available).
	NoCache interface {
		Enabled() bool
		SetEnabled(bool)
	}
	// Throttle slows traffic down to a network profile (nil: not available).
	Throttle interface {
		Settings() proxy.ThrottleSettings
		Set(proxy.ThrottleSettings)
	}
	// AppIcon renders a client app's icon as PNG (nil or error: 404, the UI falls back).
	AppIcon func(app string) ([]byte, error)
	Redact  *redact.Redactor // nil = redact.Default
	Version string
}

type Options struct {
	Addr string
	// UnsafeRemote allows a non-loopback bind (--unsafe-remote-api).
	UnsafeRemote bool
}

// StatusInfo is GET /api/v1/status (and `proxypilot status --json`).
type StatusInfo struct {
	Running            bool      `json:"running"`
	PID                int       `json:"pid"`
	ProxyAddr          string    `json:"proxy_addr"`
	APIAddr            string    `json:"api_addr"`
	StartedAt          time.Time `json:"started_at"`
	UptimeSeconds      int64     `json:"uptime_seconds"`
	Flows              int       `json:"flows"`
	ActiveRules        int       `json:"active_rules"`
	PendingBreakpoints int       `json:"pending_breakpoints"`
	CAFingerprint      string    `json:"ca_fingerprint"`
	Session            string    `json:"session"`
	Subscribers        int       `json:"stream_subscribers"`
	Version            string    `json:"version"`
	Recording          bool      `json:"recording"`
	MaxEntries         int       `json:"max_entries"` // store retention cap (0 = unlimited)
	DataDir            string    `json:"data_dir"`
	NoCaching          bool      `json:"no_caching"`         // clients can't use their caches (toolbar switch)
	Throttle           string    `json:"throttle,omitempty"` // active throttle profile, "" when off
}

type Server struct {
	deps Deps
	opts Options
	log  *slog.Logger
	http *http.Server
	ln   net.Listener

	allowedHosts   map[string]bool
	allowedOrigins map[string]bool
}

// ErrRemoteBind is returned when the API would listen beyond loopback.
var ErrRemoteBind = errors.New("control API must listen on 127.0.0.1 or ::1 (use --unsafe-remote-api to override)")

func New(deps Deps, opts Options) *Server {
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Server{deps: deps, opts: opts, log: deps.Log}
}

// Listen binds the API. Loopback only unless UnsafeRemote.
func (s *Server) Listen() error {
	host, _, err := net.SplitHostPort(s.opts.Addr)
	if err != nil {
		return err
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	loopback := host == "localhost" || (ip != nil && ip.IsLoopback())
	if !loopback {
		if !s.opts.UnsafeRemote {
			return ErrRemoteBind
		}
		s.log.Warn("!!! control API is reachable from the network; anyone with the token can read and modify your traffic", "addr", s.opts.Addr)
	}
	ln, err := net.Listen("tcp", s.opts.Addr)
	if err != nil {
		return err
	}
	s.ln = ln
	s.computeAllowed(ln.Addr().(*net.TCPAddr).Port)
	return nil
}

// Addr is the bound address.
func (s *Server) Addr() net.Addr { return s.ln.Addr() }

func (s *Server) computeAllowed(port int) {
	p := strconv.Itoa(port)
	s.allowedHosts = map[string]bool{
		"127.0.0.1:" + p: true,
		"localhost:" + p: true,
		"[::1]:" + p:     true,
	}
	s.allowedOrigins = map[string]bool{}
	for h := range s.allowedHosts {
		s.allowedOrigins["http://"+h] = true
	}
	if s.opts.UnsafeRemote {
		s.allowedHosts[s.opts.Addr] = true
		s.allowedOrigins["http://"+s.opts.Addr] = true
	}
}

// IsAPIAddress reports whether host:port points at this API (the proxy skips recording it).
// SetLAN wires the network-access switch once the proxy exists (it is built after the API).
func (s *Server) SetLAN(l interface {
	LANState() (on, fixed bool)
	SetLAN(on bool) error
}) {
	s.deps.LAN = l
}

func (s *Server) IsAPIAddress(hostport string) bool {
	return s.allowedHosts[strings.ToLower(hostport)]
}

// Serve runs until ctx is done.
func (s *Server) Serve(ctx context.Context) error {
	s.http = &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelDebug),
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.http.Shutdown(sctx)
	}()
	s.log.Info("control API listening", "addr", s.ln.Addr().String())
	err := s.http.Serve(s.ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Handler is the full handler chain (exported for tests).
func (s *Server) Handler() http.Handler {
	if s.allowedHosts == nil { // tests that skip Listen
		s.computeAllowed(0)
	}
	mux := http.NewServeMux()
	s.routes(mux)
	return s.secure(mux)
}

// secure applies, in order: DNS-rebinding (Host) check, no CORS, CSRF (Origin)
// check, token exchange for the UI, and authentication for /api/.
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.allowedHosts[strings.ToLower(r.Host)] {
			writeError(w, http.StatusMisdirectedRequest, "bad_host", "Host header must be 127.0.0.1, localhost or [::1] with the API port")
			return
		}
		if r.Method == http.MethodOptions {
			writeError(w, http.StatusForbidden, "cors_disabled", "CORS is disabled")
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" && !s.allowedOrigins[strings.ToLower(origin)] {
			writeError(w, http.StatusForbidden, "bad_origin", "cross-origin requests are not allowed")
			return
		}

		// UI entry: /?token=... sets the cookie and redirects to a clean URL.
		if tok := r.URL.Query().Get("token"); tok != "" && !strings.HasPrefix(r.URL.Path, "/api/") {
			if !s.tokenOK(tok) {
				writeError(w, http.StatusUnauthorized, "unauthorized", "invalid token")
				return
			}
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			q := r.URL.Query()
			q.Del("token")
			u := *r.URL
			u.RawQuery = q.Encode()
			http.Redirect(w, r, u.RequestURI(), http.StatusFound)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/") {
			bearer, viaHeader := bearerToken(r)
			cookieTok := ""
			if c, err := r.Cookie(cookieName); err == nil {
				cookieTok = c.Value
			}
			switch {
			case viaHeader && s.tokenOK(bearer):
			case cookieTok != "" && s.tokenOK(cookieTok):
				// Cookie auth for state-changing requests needs a same-origin browser.
				if !safeMethod(r.Method) && origin == "" {
					writeError(w, http.StatusForbidden, "csrf", "state-changing requests without Origin need a bearer token")
					return
				}
			default:
				writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid token (Authorization: Bearer <token>)")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if tok, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(tok), true
	}
	return "", false
}

func (s *Server) tokenOK(tok string) bool {
	return s.deps.Token != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(s.deps.Token)) == 1
}

// --- responses ---

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: msg}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func (s *Server) redactor() *redact.Redactor {
	if s.deps.Redact != nil {
		return s.deps.Redact
	}
	return redact.Default
}
