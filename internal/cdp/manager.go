package cdp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/hakanor/proxypilot/internal/events"
	"github.com/hakanor/proxypilot/internal/graphql"
	"github.com/hakanor/proxypilot/internal/rules"
	"github.com/hakanor/proxypilot/internal/store"
)

// ChromeState is published as "chrome.state" and returned by Status.
const EventState = "chrome.state"

type Tab struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Watching bool   `json:"watching"`
	Requests int    `json:"requests"`
	session  string
}

type Status struct {
	Connected bool   `json:"connected"`
	Launched  bool   `json:"launched"` // started by ProxyPilot (vs attached)
	Endpoint  string `json:"endpoint,omitempty"`
	Browser   string `json:"browser,omitempty"`
	Tabs      []Tab  `json:"tabs"`
	Error     string `json:"error,omitempty"`
}

type Manager struct {
	Store   *store.Store
	Bus     *events.Bus
	Rules   *rules.Engine
	Log     *slog.Logger
	DataDir string
	// Binary overrides the Chrome executable.
	Binary string
	// Headless launches without a window (tests, CI).
	Headless bool

	mu       sync.Mutex
	conn     *Conn
	cmd      *exec.Cmd
	endpoint string
	browser  string
	tabs     map[string]*Tab // target id
	sessions map[string]*Tab // session id
	flows    map[string]*pending
	matched  map[string]map[string]string // session+networkId → tags from the Fetch stage
	lastErr  string
	cancel   context.CancelFunc
}

type pending struct {
	flow    *store.Flow
	tab     *Tab
	started float64 // CDP monotonic seconds
	ws      bool
}

var ErrNotConnected = errors.New("not connected to Chrome (use `proxypilot chrome launch` or `attach`)")

func (m *Manager) log() *slog.Logger {
	if m.Log == nil {
		return slog.Default()
	}
	return m.Log
}

// FindChrome returns a Chrome/Chromium executable.
func FindChrome(override string) (string, error) {
	cands := []string{override}
	switch runtime.GOOS {
	case "darwin":
		cands = append(cands,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge")
	default:
		for _, n := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge"} {
			if p, err := exec.LookPath(n); err == nil {
				cands = append(cands, p)
			}
		}
	}
	for _, c := range cands {
		if c == "" {
			continue
		}
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c, nil
		}
	}
	return "", errors.New("Chrome not found; install it or set the path with --chrome")
}

// Launch starts Chrome with a separate ProxyPilot profile and connects to it.
// Chrome does not allow remote debugging on your main profile.
func (m *Manager) Launch(ctx context.Context, startURL string) (Status, error) {
	m.Detach()
	bin, err := FindChrome(m.Binary)
	if err != nil {
		return m.Status(), err
	}
	profile := filepath.Join(m.DataDir, "chrome-profile")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return m.Status(), err
	}
	portFile := filepath.Join(profile, "DevToolsActivePort")
	_ = os.Remove(portFile)
	args := []string{"--remote-debugging-port=0", "--user-data-dir=" + profile,
		"--no-first-run", "--no-default-browser-check",
		"--no-proxy-server", // CDP already captures it; a system proxy would record everything twice
	}
	if m.Headless {
		args = append(args, "--headless=new")
	}
	// Start blank and navigate once the tab is attached, so its first requests are captured.
	cmd := exec.Command(bin, append(args, "about:blank")...)
	if err := cmd.Start(); err != nil {
		return m.Status(), fmt.Errorf("start %s: %w", bin, err)
	}
	go func() { _ = cmd.Wait() }()
	// Chrome writes the chosen port and browser path once DevTools is listening.
	var port, path string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if f, err := os.Open(portFile); err == nil {
			sc := bufio.NewScanner(f)
			if sc.Scan() {
				port = strings.TrimSpace(sc.Text())
			}
			if sc.Scan() {
				path = strings.TrimSpace(sc.Text())
			}
			f.Close()
			if port != "" && path != "" {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if port == "" || path == "" {
		_ = cmd.Process.Kill()
		return m.Status(), errors.New("Chrome did not open its DevTools port in 15s (is another Chrome using this profile?)")
	}
	addr := net.JoinHostPort("127.0.0.1", port)
	if err := m.connect(ctx, addr, "ws://"+addr+path); err != nil {
		_ = cmd.Process.Kill()
		return m.Status(), err
	}
	m.mu.Lock()
	m.cmd = cmd
	m.mu.Unlock()
	if startURL != "" && startURL != "about:blank" {
		if err := m.Navigate(ctx, startURL); err != nil {
			m.log().Warn("chrome navigate", "err", err)
		}
	}
	return m.Status(), nil
}

// Navigate opens url in the first watched tab (waiting briefly for one to attach).
func (m *Manager) Navigate(ctx context.Context, u string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.mu.Lock()
		conn := m.conn
		var session string
		for _, t := range m.tabs {
			if t.Watching {
				session = t.session
				break
			}
		}
		m.mu.Unlock()
		if conn == nil {
			return ErrNotConnected
		}
		if session != "" {
			return conn.Call(ctx, session, "Page.navigate", map[string]string{"url": u}, nil)
		}
		if time.Now().After(deadline) {
			return errors.New("no tab attached")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Attach connects to an already running Chrome started with --remote-debugging-port.
func (m *Manager) Attach(ctx context.Context, addr string) (Status, error) {
	m.Detach()
	if !strings.Contains(addr, ":") {
		addr = "127.0.0.1:" + addr
	}
	ws, err := BrowserWS(ctx, addr)
	if err != nil {
		return m.Status(), err
	}
	return m.Status(), m.connect(ctx, addr, ws)
}

func (m *Manager) connect(ctx context.Context, addr, wsURL string) error {
	conn, err := Dial(ctx, wsURL, m.onEvent)
	if err != nil {
		return fmt.Errorf("connect to Chrome: %w", err)
	}
	var v struct {
		Product string `json:"product"`
	}
	_ = conn.Call(ctx, "", "Browser.getVersion", nil, &v)
	rctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.conn, m.endpoint, m.browser, m.lastErr, m.cancel = conn, addr, v.Product, "", cancel
	m.tabs, m.sessions, m.flows, m.matched = map[string]*Tab{}, map[string]*Tab{}, map[string]*pending{}, map[string]map[string]string{}
	m.mu.Unlock()

	if err := conn.Call(ctx, "", "Target.setDiscoverTargets", map[string]any{"discover": true}, nil); err != nil {
		conn.Close()
		return err
	}
	// Attach to existing and new tabs; they wait until we enabled Network and Fetch.
	if err := conn.Call(ctx, "", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true}, nil); err != nil {
		conn.Close()
		return err
	}
	var targets struct {
		TargetInfos []targetInfo `json:"targetInfos"`
	}
	_ = conn.Call(ctx, "", "Target.getTargets", nil, &targets)
	for _, t := range targets.TargetInfos {
		if t.Type == "page" && !t.Attached {
			_ = conn.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": t.TargetID, "flatten": true}, nil)
		}
	}
	go m.watchRules(rctx)
	go func() {
		select {
		case <-conn.Done():
			m.closed(conn)
		case <-rctx.Done():
		}
	}()
	m.publishState()
	m.log().Info("chrome connected", "endpoint", addr, "browser", v.Product)
	return nil
}

// closed cleans up after Chrome went away: in-flight flows are marked failed.
func (m *Manager) closed(conn *Conn) {
	m.mu.Lock()
	if m.conn != conn {
		m.mu.Unlock()
		return
	}
	left := make([]*pending, 0, len(m.flows))
	for _, p := range m.flows {
		left = append(left, p)
	}
	m.conn, m.cmd, m.tabs, m.sessions, m.flows = nil, nil, map[string]*Tab{}, map[string]*Tab{}, map[string]*pending{}
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	for _, p := range left {
		p.flow.State, p.flow.ErrorKind, p.flow.ErrorMsg = store.StateFailed, "client_aborted", "Chrome closed"
		m.save(p.flow, events.FlowFailed)
	}
	m.publishState()
	m.log().Info("chrome disconnected")
}

// Detach closes the connection (a launched Chrome stays open).
func (m *Manager) Detach() {
	m.mu.Lock()
	conn := m.conn
	m.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
		<-conn.Done()
		m.closed(conn)
	}
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Status{Connected: m.conn != nil, Launched: m.cmd != nil, Endpoint: m.endpoint, Browser: m.browser, Error: m.lastErr, Tabs: []Tab{}}
	if m.conn == nil {
		s.Endpoint, s.Browser = "", ""
	}
	for _, t := range m.tabs {
		s.Tabs = append(s.Tabs, *t)
	}
	sort.Slice(s.Tabs, func(i, j int) bool { return s.Tabs[i].ID < s.Tabs[j].ID })
	return s
}

func (m *Manager) publishState() {
	if m.Bus != nil {
		m.Bus.Publish(EventState, m.Status())
	}
}

// SetWatch turns capture on or off for one tab.
func (m *Manager) SetWatch(ctx context.Context, tabID string, watch bool) (Tab, error) {
	m.mu.Lock()
	conn, t := m.conn, m.tabs[tabID]
	m.mu.Unlock()
	if conn == nil {
		return Tab{}, ErrNotConnected
	}
	if t == nil {
		return Tab{}, fmt.Errorf("tab %s not found", tabID)
	}
	if watch {
		m.enable(ctx, conn, t.session)
	} else {
		_ = conn.Call(ctx, t.session, "Fetch.disable", nil, nil)
		_ = conn.Call(ctx, t.session, "Network.disable", nil, nil)
	}
	m.mu.Lock()
	t.Watching = watch
	c := *t
	m.mu.Unlock()
	m.publishState()
	return c, nil
}

func (m *Manager) enable(ctx context.Context, conn *Conn, session string) {
	_ = conn.Call(ctx, session, "Network.enable", map[string]any{"maxPostDataSize": 1 << 20}, nil)
	m.applyFetch(ctx, conn, session)
}

// applyFetch (re)configures which requests Chrome pauses for the rules engine.
func (m *Manager) applyFetch(ctx context.Context, conn *Conn, session string) {
	if m.Rules == nil {
		return
	}
	req, resp := m.Rules.Patterns()
	if len(req) == 0 && len(resp) == 0 {
		_ = conn.Call(ctx, session, "Fetch.disable", nil, nil)
		return
	}
	var patterns []map[string]string
	for _, p := range req {
		patterns = append(patterns, map[string]string{"urlPattern": p, "requestStage": "Request"})
	}
	for _, p := range resp {
		patterns = append(patterns, map[string]string{"urlPattern": p, "requestStage": "Response"})
	}
	if err := conn.Call(ctx, session, "Fetch.enable", map[string]any{"patterns": patterns}, nil); err != nil {
		m.log().Warn("chrome Fetch.enable failed", "err", err)
	}
}

// watchRules refreshes Fetch patterns on every tab when rules change.
func (m *Manager) watchRules(ctx context.Context) {
	if m.Bus == nil {
		return
	}
	sub := m.Bus.Subscribe(func(e events.Event) bool { return e.Type == events.RuleChanged }, 16)
	defer sub.Cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.C:
			m.mu.Lock()
			conn := m.conn
			var sessions []string
			for _, t := range m.tabs {
				if t.Watching {
					sessions = append(sessions, t.session)
				}
			}
			m.mu.Unlock()
			for _, s := range sessions {
				m.applyFetch(ctx, conn, s)
			}
		}
	}
}

type targetInfo struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Attached bool   `json:"attached"`
}

// onEvent runs on the websocket read goroutine: anything that calls back into
// Chrome must run on its own goroutine.
func (m *Manager) onEvent(method, session string, params json.RawMessage) {
	switch method {
	case "Target.attachedToTarget":
		var p struct {
			SessionID  string     `json:"sessionId"`
			TargetInfo targetInfo `json:"targetInfo"`
			Waiting    bool       `json:"waitingForDebugger"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		go m.attached(p.SessionID, p.TargetInfo, p.Waiting)
	case "Target.targetInfoChanged":
		var p struct {
			TargetInfo targetInfo `json:"targetInfo"`
		}
		if json.Unmarshal(params, &p) == nil {
			m.mu.Lock()
			if t := m.tabs[p.TargetInfo.TargetID]; t != nil {
				t.Title, t.URL = p.TargetInfo.Title, p.TargetInfo.URL
			}
			m.mu.Unlock()
		}
	case "Target.detachedFromTarget":
		var p struct {
			SessionID string `json:"sessionId"`
			TargetID  string `json:"targetId"`
		}
		if json.Unmarshal(params, &p) == nil {
			m.mu.Lock()
			if t := m.sessions[p.SessionID]; t != nil {
				delete(m.tabs, t.ID)
			}
			delete(m.sessions, p.SessionID)
			m.mu.Unlock()
			m.publishState()
		}
	case "Fetch.requestPaused":
		m.mu.Lock()
		conn, t := m.conn, m.sessions[session]
		m.mu.Unlock()
		if conn != nil {
			go m.paused(conn, session, t, params)
		}
	default:
		if strings.HasPrefix(method, "Network.") {
			m.network(method, session, params)
		}
	}
}

func (m *Manager) attached(session string, ti targetInfo, waiting bool) {
	m.mu.Lock()
	conn := m.conn
	m.mu.Unlock()
	if conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if ti.Type != "page" {
		// Not a tab (worker, iframe…): let it run, ignore its traffic.
		if waiting {
			_ = conn.Call(ctx, session, "Runtime.runIfWaitingForDebugger", nil, nil)
		}
		return
	}
	t := &Tab{ID: ti.TargetID, Title: ti.Title, URL: ti.URL, Watching: true, session: session}
	m.mu.Lock()
	m.tabs[t.ID], m.sessions[session] = t, t
	m.mu.Unlock()
	m.enable(ctx, conn, session)
	if waiting {
		_ = conn.Call(ctx, session, "Runtime.runIfWaitingForDebugger", nil, nil)
	}
	m.publishState()
}

// --- capture (Network domain) ---

type cdpRequest struct {
	URL         string            `json:"url"`
	Method      string            `json:"method"`
	Headers     map[string]string `json:"headers"`
	PostData    string            `json:"postData"`
	HasPostData bool              `json:"hasPostData"`
}

type cdpResponse struct {
	Status            int               `json:"status"`
	Headers           map[string]string `json:"headers"`
	MimeType          string            `json:"mimeType"`
	Protocol          string            `json:"protocol"`
	EncodedDataLength float64           `json:"encodedDataLength"`
	Timing            *struct {
		DNSStart          float64 `json:"dnsStart"`
		DNSEnd            float64 `json:"dnsEnd"`
		ConnectStart      float64 `json:"connectStart"`
		ConnectEnd        float64 `json:"connectEnd"`
		SSLStart          float64 `json:"sslStart"`
		SSLEnd            float64 `json:"sslEnd"`
		ReceiveHeadersEnd float64 `json:"receiveHeadersEnd"`
	} `json:"timing"`
}

func headerList(h map[string]string) []store.Header {
	out := make([]store.Header, 0, len(h))
	for k, v := range h {
		for _, line := range strings.Split(v, "\n") { // CDP joins repeated headers with \n
			out = append(out, store.Header{Name: k, Value: line})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func headerGet(h map[string]string, name string) string {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

func newStoreFlow(t *Tab, r cdpRequest) *store.Flow {
	f := &store.Flow{
		ID: ulid.Make().String(), Source: store.SourceCDP, ClientAddr: "chrome", ClientLabel: "Chrome",
		Method: r.Method, URL: r.URL, State: store.StatePending, CreatedAt: time.Now(),
		ReqHeaders: headerList(r.Headers), ReqContentType: headerGet(r.Headers, "Content-Type"),
		Tags: map[string]string{"tab_id": t.ID, "tab_title": t.Title, "tab_url": t.URL, "device": "Chrome", "app": tabName(t)},
	}
	if u, err := url.Parse(r.URL); err == nil {
		f.Scheme, f.Host, f.Path, f.Query = u.Scheme, u.Hostname(), u.Path, u.RawQuery
		f.Port, _ = strconv.Atoi(u.Port())
		if f.Port == 0 {
			f.Port = map[string]int{"https": 443, "wss": 443}[u.Scheme]
			if f.Port == 0 {
				f.Port = 80
			}
		}
	}
	return f
}

func (m *Manager) save(f *store.Flow, typ string) {
	ctx := context.Background()
	if typ == events.FlowStarted {
		if err := m.Store.Insert(ctx, f); err != nil {
			m.log().Warn("cdp store insert", "err", err)
			return
		}
	} else if err := m.Store.Update(ctx, f); err != nil {
		m.log().Warn("cdp store update", "err", err)
		return
	}
	if m.Bus != nil {
		c := *f
		m.Bus.Publish(typ, &c)
	}
}

func (m *Manager) network(method, session string, params json.RawMessage) {
	var p struct {
		RequestID        string       `json:"requestId"`
		Request          cdpRequest   `json:"request"`
		Response         cdpResponse  `json:"response"`
		RedirectResponse *cdpResponse `json:"redirectResponse"`
		Timestamp        float64      `json:"timestamp"`
		EncodedLength    float64      `json:"encodedDataLength"`
		ErrorText        string       `json:"errorText"`
		Canceled         bool         `json:"canceled"`
		URL              string       `json:"url"`
		Type             string       `json:"type"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	key := session + "|" + p.RequestID
	m.mu.Lock()
	t := m.sessions[session]
	pd := m.flows[key]
	m.mu.Unlock()
	if t == nil || !t.Watching {
		return
	}
	switch method {
	case "Network.requestWillBeSent":
		if pd != nil && p.RedirectResponse != nil {
			m.finish(key, pd, p.RedirectResponse, p.Timestamp, 0, "", false)
		}
		if strings.HasPrefix(p.Request.URL, "data:") || strings.HasPrefix(p.Request.URL, "blob:") {
			return
		}
		f := newStoreFlow(t, p.Request)
		f.ReqSize = int64(len(p.Request.PostData))
		m.mu.Lock()
		m.flows[key] = &pending{flow: f, tab: t, started: p.Timestamp}
		t.Requests++
		m.mu.Unlock()
		m.save(f, events.FlowStarted)
		if p.Request.PostData != "" {
			m.putBody(f, store.SideReq, []byte(p.Request.PostData))
		} else if p.Request.HasPostData {
			go m.fetchPostData(session, p.RequestID, f)
		}
	case "Network.responseReceived":
		if pd != nil {
			m.respond(pd, &p.Response)
		}
	case "Network.loadingFinished":
		if pd != nil {
			go m.finish(key, pd, nil, p.Timestamp, p.EncodedLength, "", true)
		}
	case "Network.loadingFailed":
		if pd != nil {
			msg := p.ErrorText
			if p.Canceled {
				msg = "canceled"
			}
			m.finish(key, pd, nil, p.Timestamp, 0, msg, false)
		}
	case "Network.webSocketCreated":
		f := newStoreFlow(t, cdpRequest{URL: p.URL, Method: "GET"})
		f.Protocol = "websocket"
		m.mu.Lock()
		m.flows[key] = &pending{flow: f, tab: t, started: p.Timestamp, ws: true}
		m.mu.Unlock()
		m.save(f, events.FlowStarted)
	case "Network.webSocketHandshakeResponseReceived":
		if pd != nil {
			pd.flow.Status = p.Response.Status
			pd.flow.RespHeaders = headerList(p.Response.Headers)
			m.save(pd.flow, events.FlowUpdated)
		}
	case "Network.webSocketFrameSent", "Network.webSocketFrameReceived":
		if pd != nil {
			var fr struct {
				Response struct {
					PayloadData string `json:"payloadData"`
				} `json:"response"`
			}
			_ = json.Unmarshal(params, &fr)
			m.mu.Lock()
			n := int64(len(fr.Response.PayloadData))
			if method == "Network.webSocketFrameSent" {
				pd.flow.BytesUp += n
			} else {
				pd.flow.BytesDown += n
			}
			frames, _ := strconv.Atoi(pd.flow.Tags["ws_frames"])
			pd.flow.Tags["ws_frames"] = strconv.Itoa(frames + 1)
			m.mu.Unlock()
		}
	case "Network.webSocketClosed":
		if pd != nil {
			pd.flow.State = store.StateCompleted
			pd.flow.Timing.Total = (p.Timestamp - pd.started) * 1000
			m.mu.Lock()
			delete(m.flows, key)
			m.mu.Unlock()
			m.save(pd.flow, events.FlowCompleted)
		}
	}
}

func (m *Manager) respond(pd *pending, r *cdpResponse) {
	f := pd.flow
	f.Status, f.RespHeaders, f.RespContentType, f.Protocol = r.Status, headerList(r.Headers), headerGet(r.Headers, "Content-Type"), r.Protocol
	if f.RespContentType == "" {
		f.RespContentType = r.MimeType
	}
	if tm := r.Timing; tm != nil {
		span := func(a, b float64) float64 {
			if a < 0 || b < 0 {
				return 0
			}
			return b - a
		}
		f.Timing.DNS = span(tm.DNSStart, tm.DNSEnd)
		f.Timing.Connect = span(tm.ConnectStart, tm.ConnectEnd)
		f.Timing.TLS = span(tm.SSLStart, tm.SSLEnd)
		f.Timing.TTFB = tm.ReceiveHeadersEnd
	}
}

func (m *Manager) finish(key string, pd *pending, redirect *cdpResponse, ts, encoded float64, errText string, body bool) {
	m.mu.Lock()
	if m.flows[key] == pd {
		delete(m.flows, key)
	}
	tags := m.matched[key]
	conn := m.conn
	m.mu.Unlock()
	f := pd.flow
	if redirect != nil {
		m.respond(pd, redirect)
	}
	f.Timing.Total = (ts - pd.started) * 1000
	if encoded > 0 {
		f.BytesDown = int64(encoded)
	}
	for k, v := range tags {
		if k == "matched_rules" {
			f.MatchedRules = strings.Split(v, ",")
		} else {
			f.Tags[k] = v
		}
	}
	typ := events.FlowCompleted
	if errText != "" {
		f.State, f.ErrorKind, f.ErrorMsg = store.StateFailed, errorKind(errText), errText
		typ = events.FlowFailed
	} else {
		f.State = store.StateCompleted
	}
	if body && conn != nil && f.Status != 204 && f.Status != 304 && f.Method != "HEAD" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		var b struct {
			Body          string `json:"body"`
			Base64Encoded bool   `json:"base64Encoded"`
		}
		if err := conn.Call(ctx, pd.tab.session, "Network.getResponseBody", map[string]string{"requestId": strings.SplitN(key, "|", 2)[1]}, &b); err == nil {
			data := []byte(b.Body)
			if b.Base64Encoded {
				data, _ = base64.StdEncoding.DecodeString(b.Body)
			}
			m.putBody(f, store.SideResp, data)
		}
		cancel()
	}
	if graphql.Likely(f) {
		ctx := context.Background()
		req, _, _, _ := m.Store.GetBody(ctx, f.ID, store.SideReq, 1<<20)
		resp, _, _, _ := m.Store.GetBody(ctx, f.ID, store.SideResp, 1<<20)
		if graphql.Annotate(f, req, resp) && f.ErrorKind == graphql.ErrorKind {
			typ = events.FlowFailed
		}
	}
	m.save(f, typ)
	m.mu.Lock()
	delete(m.matched, key)
	m.mu.Unlock()
}

func errorKind(text string) string {
	switch {
	case strings.Contains(text, "CERT"), strings.Contains(text, "SSL"):
		return "upstream_tls_error"
	case strings.Contains(text, "TIMED_OUT"):
		return "timeout"
	case text == "canceled", strings.Contains(text, "ABORTED"):
		return "client_aborted"
	}
	return "upstream_connect_error"
}

func (m *Manager) putBody(f *store.Flow, side string, data []byte) {
	if len(data) == 0 {
		return
	}
	info, err := m.Store.PutBody(context.Background(), f.ID, side, "", int64(len(data)), func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	})
	if err != nil {
		return
	}
	if side == store.SideReq {
		f.ReqSize = info.DecodedSize
	} else {
		f.RespSize = info.DecodedSize
	}
}

func (m *Manager) fetchPostData(session, requestID string, f *store.Flow) {
	m.mu.Lock()
	conn := m.conn
	m.mu.Unlock()
	if conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var r struct {
		PostData string `json:"postData"`
	}
	if conn.Call(ctx, session, "Network.getRequestPostData", map[string]string{"requestId": requestID}, &r) == nil {
		m.putBody(f, store.SideReq, []byte(r.PostData))
	}
}

// tabName is the sidebar name of a tab: its title, else its host.
func tabName(t *Tab) string {
	if t.Title != "" {
		return t.Title
	}
	if u, err := url.Parse(t.URL); err == nil && u.Host != "" {
		return u.Host
	}
	return "Tab"
}
