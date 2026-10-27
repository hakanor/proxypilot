// Package capture connects proxy hooks to the store and the event bus.
// Order is guaranteed: a flow is written to the store before its event is published.
package capture

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hakanor/proxypilot/internal/events"
	"github.com/hakanor/proxypilot/internal/graphql"
	"github.com/hakanor/proxypilot/internal/proxy"
	"github.com/hakanor/proxypilot/internal/store"
)

type Recorder struct {
	proxy.NopHooks
	Store *store.Store
	Bus   *events.Bus
	Log   *slog.Logger

	// Names returns a user-chosen device name for a client address ("" = guess from User-Agent).
	Names func(addr string) string
	// Origin names the device and app of a local client from its process
	// (ok=false: unknown, fall back to the User-Agent).
	Origin func(addr string) (device, app string, ok bool)

	inserted sync.Map // proxy flow id → struct{}
	wsSeq    sync.Map // proxy flow id → *atomic.Int64, next WebSocket message seq
	skipped  sync.Map // flows started while recording was paused
	paused   atomic.Bool
}

// Recording reports whether new flows are being recorded.
func (r *Recorder) Recording() bool { return !r.paused.Load() }

// SetRecording pauses or resumes recording. Flows already in progress finish normally.
func (r *Recorder) SetRecording(on bool) {
	if r.paused.Swap(!on) == !on {
		return
	}
	r.Bus.Publish(events.RecordingChanged, map[string]bool{"recording": on})
}

// skip reports whether f was not recorded, and frees its capture files if so.
func (r *Recorder) skip(f *proxy.Flow) bool {
	_, started := r.inserted.Load(f.ID)
	_, wasSkipped := r.skipped.LoadAndDelete(f.ID)
	if started || (!wasSkipped && !r.paused.Load()) {
		return false
	}
	for _, b := range []*proxy.Body{f.ReqBody, f.RespBody} {
		if b != nil {
			b.Close()
		}
	}
	return true
}

// MaxWSMessages caps how many messages one WebSocket connection keeps.
const MaxWSMessages = 50000

// OnMessage stores one WebSocket message and announces it (without the payload).
func (r *Recorder) OnMessage(f *proxy.Flow, m proxy.WSMessage) {
	if r.paused.Load() {
		return
	}
	v, _ := r.wsSeq.LoadOrStore(f.ID, new(atomic.Int64))
	seq := v.(*atomic.Int64).Add(1)
	if seq > MaxWSMessages {
		return // long-lived connection: keep the first ones, stop growing the database
	}
	sm := store.Message{FlowID: f.ID, Seq: seq, Dir: m.Dir, At: m.At, Opcode: m.Opcode, Size: m.Size, Payload: m.Payload, Truncated: m.Truncated}
	if err := r.Store.InsertMessage(context.Background(), sm); err != nil {
		r.Log.Warn("store message failed", "flow", f.ID, "err", err)
		return
	}
	if m.Opcode == "close" && m.Dir == "down" {
		r.wsSeq.Delete(f.ID)
	}
	sm.Payload = nil
	r.Bus.Publish(events.WSMessage, sm)
}

func New(s *store.Store, b *events.Bus, log *slog.Logger) *Recorder {
	if log == nil {
		log = slog.Default()
	}
	return &Recorder{Store: s, Bus: b, Log: log}
}

func (r *Recorder) OnRequest(f *proxy.Flow) proxy.Decision {
	if r.paused.Load() {
		r.skipped.Store(f.ID, struct{}{})
		return proxy.Decision{}
	}
	sf := r.label(toStore(f, store.StatePending), f)
	if err := r.Store.Insert(context.Background(), sf); err != nil {
		r.Log.Warn("store insert failed", "flow", f.ID, "err", err)
		return proxy.Decision{}
	}
	r.inserted.Store(f.ID, struct{}{})
	r.Bus.Publish(events.FlowStarted, sf)
	return proxy.Decision{}
}

func (r *Recorder) OnComplete(f *proxy.Flow) {
	state := store.StateCompleted
	if f.Tunneled {
		state = store.StateTunneled
	}
	sf := r.persist(f, state)
	if sf != nil {
		r.Bus.Publish(events.FlowCompleted, sf)
	}
}

func (r *Recorder) OnError(f *proxy.Flow, err error) {
	sf := r.persist(f, store.StateFailed)
	if sf == nil {
		return
	}
	r.Bus.Publish(events.FlowFailed, sf)
	if tags := f.Tags(); tags["suggestion"] == "ssl_denylist" {
		host, _, _ := net.SplitHostPort(f.Host)
		r.Bus.Publish(events.Suggestion, map[string]any{
			"kind":       "ssl_denylist",
			"host":       host,
			"rejections": tags["rejections"],
			"message":    "The client keeps rejecting the ProxyPilot certificate for this host (SSL pinning or untrusted CA). Add it to the SSL denylist?",
		})
	}
}

// persist writes the final state and bodies, then frees the capture temp files.
func (r *Recorder) persist(f *proxy.Flow, state string) *store.Flow {
	if r.skip(f) {
		return nil
	}
	ctx := context.Background()
	sf := r.label(toStore(f, state), f)
	_, wasInserted := r.inserted.LoadAndDelete(f.ID)
	var err error
	if wasInserted {
		err = r.Store.Update(ctx, sf)
	} else {
		err = r.Store.Insert(ctx, sf)
	}
	if err != nil {
		r.Log.Warn("store write failed", "flow", f.ID, "err", err)
		return nil
	}
	r.putBody(ctx, sf, store.SideReq, f.ReqBody, headerOf(f.Request))
	r.putBody(ctx, sf, store.SideResp, f.RespBody, respHeader(f.Response))
	gql := r.annotateGraphQL(ctx, sf)
	if sf.ReqSize > 0 || sf.RespSize > 0 || gql {
		_ = r.Store.Update(ctx, sf) // decoded sizes, GraphQL tags
	}
	return sf
}

func (r *Recorder) putBody(ctx context.Context, sf *store.Flow, side string, b *proxy.Body, h http.Header) {
	if b == nil {
		return
	}
	defer b.Close()
	if b.Size() == 0 {
		return
	}
	info, err := r.Store.PutBody(ctx, sf.ID, side, h.Get("Content-Encoding"), b.Size(), func() (io.ReadCloser, error) { return b.Open() })
	if err != nil {
		r.Log.Warn("store body failed", "flow", sf.ID, "side", side, "err", err)
		return
	}
	if side == store.SideReq {
		sf.ReqSize = info.DecodedSize
	} else {
		sf.RespSize = info.DecodedSize
	}
}

func headerOf(req *http.Request) http.Header {
	if req == nil {
		return http.Header{}
	}
	return req.Header
}

func respHeader(resp *http.Response) http.Header {
	if resp == nil {
		return http.Header{}
	}
	return resp.Header
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// toStore maps a proxy flow to its stored form.
func toStore(f *proxy.Flow, state string) *store.Flow {
	sf := &store.Flow{
		ID: f.ID, Source: store.SourceProxy, ClientAddr: f.ClientAddr, Scheme: f.Scheme,
		State: state, BytesUp: f.BytesUp, BytesDown: f.BytesDown, ErrorKind: f.ErrorKind,
		CreatedAt: f.Started, Tags: f.Tags(), MatchedRules: matchedRules(f),
		Timing: store.Timing{DNS: ms(f.Timing.DNS), Connect: ms(f.Timing.Connect), TLS: ms(f.Timing.TLS),
			TTFB: ms(f.Timing.TTFB), Total: ms(f.Timing.Total), Reused: f.Timing.Reused},
	}
	if f.Err != nil {
		sf.ErrorMsg = f.Err.Error()
	}
	host, port := f.Host, ""
	if h, p, err := net.SplitHostPort(f.Host); err == nil {
		host, port = h, p
	}
	sf.Host = strings.ToLower(host)
	sf.Port, _ = strconv.Atoi(port)

	if req := f.Request; req != nil {
		sf.Method = req.Method
		sf.Protocol = req.Proto
		sf.ReqContentType = req.Header.Get("Content-Type")
		sf.ReqHeaders = headers(req.Header)
		sf.ClientLabel = ClientLabel(req.Header.Get("User-Agent"))
		if f.Tunneled {
			sf.Method = http.MethodConnect
			sf.URL = f.Host
		} else if req.URL != nil {
			sf.Path, sf.Query = req.URL.Path, req.URL.RawQuery
			sf.URL = req.URL.String()
		}
	} else {
		sf.Method = http.MethodConnect
		sf.URL = f.Host
	}
	if resp := f.Response; resp != nil {
		sf.Status = resp.StatusCode
		sf.RespContentType = resp.Header.Get("Content-Type")
		sf.RespHeaders = headers(resp.Header)
	}
	return sf
}

func headers(h http.Header) []store.Header {
	out := make([]store.Header, 0, len(h))
	for name, vals := range h {
		for _, v := range vals {
			out = append(out, store.Header{Name: name, Value: v})
		}
	}
	return out
}

// ClientLabel guesses a readable client name from a User-Agent.
func ClientLabel(ua string) string {
	l := strings.ToLower(ua)
	switch {
	case ua == "":
		return ""
	case strings.Contains(l, "iphone"):
		return "iPhone"
	case strings.Contains(l, "ipad"):
		return "iPad"
	case strings.Contains(l, "android"):
		return "Android"
	case strings.HasPrefix(l, "curl/"):
		return "curl"
	case strings.Contains(l, "edg/"):
		return "Edge"
	case strings.Contains(l, "chrome/"):
		return "Chrome"
	case strings.Contains(l, "firefox/"):
		return "Firefox"
	case strings.Contains(l, "safari/"):
		return "Safari"
	case strings.Contains(l, "cfnetwork") || strings.Contains(l, "darwin"):
		return "Apple app"
	case strings.Contains(l, "okhttp"):
		return "Android app"
	}
	if i := strings.IndexAny(ua, "/ "); i > 0 {
		return ua[:i]
	}
	return ua
}

// matchedRules reads the rule ids the rules engine tagged on the flow.
func matchedRules(f *proxy.Flow) []string {
	v := f.Tags()["matched_rules"]
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}

// label sets the sidebar grouping (tags device and app) and the client label.
func (r *Recorder) label(sf *store.Flow, f *proxy.Flow) *store.Flow {
	if sf.Tags == nil {
		sf.Tags = map[string]string{}
	}
	if src := f.Tags()["source"]; src != "" {
		sf.Source = src
		sf.ClientLabel = "Composer"
		sf.Tags["device"], sf.Tags["app"] = "ProxyPilot", "Composer"
		return sf
	}
	custom := ""
	if r.Names != nil {
		custom = r.Names(f.ClientAddr)
	}
	ua := ""
	if f.Request != nil {
		ua = f.Request.Header.Get("User-Agent")
	}
	sf.Tags["device"], sf.Tags["app"] = DeviceApp(f.ClientAddr, ua, custom)
	if r.Origin != nil && custom == "" {
		if dev, app, ok := r.Origin(f.ClientAddr); ok {
			sf.Tags["device"] = dev
			if app != "" {
				sf.Tags["app"] = app
			}
		}
	}
	sf.ClientLabel = sf.Tags["device"]
	return sf
}

// annotateGraphQL reads back the (decoded) bodies of likely GraphQL requests.
func (r *Recorder) annotateGraphQL(ctx context.Context, sf *store.Flow) bool {
	if !graphql.Likely(sf) {
		return false
	}
	req, _, _, _ := r.Store.GetBody(ctx, sf.ID, store.SideReq, 1<<20)
	resp, _, _, _ := r.Store.GetBody(ctx, sf.ID, store.SideResp, 1<<20)
	return graphql.Annotate(sf, req, resp)
}
