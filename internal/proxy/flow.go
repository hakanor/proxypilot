// Package proxy implements the HTTP forward proxy, CONNECT tunnelling and
// HTTPS interception. Everything that happens to a request is reported
// through Hooks so storage, events and rules stay outside this package.
package proxy

import (
	"net/http"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Flow is one request/response exchange (or one CONNECT tunnel).
type Flow struct {
	ID         string
	ClientAddr string
	Scheme     string // http | https
	Host       string // host:port of the upstream
	Tunneled   bool   // CONNECT passed through without interception
	Started    time.Time

	Request  *http.Request
	Response *http.Response

	// Captured bodies (decoded transfer-encoding, original content-encoding).
	ReqBody  *Body
	RespBody *Body

	// Bytes actually moved on each side; for tunnels these are raw TCP bytes.
	BytesUp   int64 // client → upstream
	BytesDown int64 // upstream → client

	Timing Timing

	ErrorKind string
	Err       error

	mu   sync.Mutex
	tags map[string]string
}

// Timing durations are measured from Flow.Started.
type Timing struct {
	DNS     time.Duration
	Connect time.Duration
	TLS     time.Duration
	TTFB    time.Duration
	Total   time.Duration
	Reused  bool // upstream connection came from the pool
}

func newFlow(clientAddr, scheme string) *Flow {
	return &Flow{ID: ulid.Make().String(), ClientAddr: clientAddr, Scheme: scheme, Started: time.Now()}
}

// SetTag attaches metadata (e.g. matched rule ids) for later layers.
func (f *Flow) SetTag(k, v string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tags == nil {
		f.tags = map[string]string{}
	}
	f.tags[k] = v
}

func (f *Flow) Tags() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(f.tags))
	for k, v := range f.tags {
		out[k] = v
	}
	return out
}

// Action tells the proxy what to do after a hook.
type Action int

const (
	Continue Action = iota // forward as is (hooks may have mutated the request/response)
	Respond                // reply with Decision.Response without contacting upstream / replacing upstream's
	Abort                  // close the client connection
)

type Decision struct {
	Action   Action
	Response *http.Response
}

// Hooks are called synchronously on the connection goroutine.
// The recorder stores and publishes flows; the rules engine applies rules and breakpoints.
type Hooks interface {
	OnRequest(f *Flow) Decision
	OnResponse(f *Flow) Decision
	OnComplete(f *Flow)
	OnError(f *Flow, err error)
}

// NopHooks does nothing; embed it to implement only some hooks.
type NopHooks struct{}

func (NopHooks) OnRequest(*Flow) Decision  { return Decision{} }
func (NopHooks) OnResponse(*Flow) Decision { return Decision{} }
func (NopHooks) OnComplete(*Flow)          {}
func (NopHooks) OnError(*Flow, error)      {}

// Error kinds stored in Flow.ErrorKind.
const (
	ErrUpstreamConnect   = "upstream_connect_error"
	ErrUpstreamTLS       = "upstream_tls_error"
	ErrClientTLSRejected = "client_tls_rejected"
	ErrClientTLS         = "client_tls_error"
	ErrTimeout           = "timeout"
	ErrClientAborted     = "client_aborted"
	ErrProxyLoop         = "proxy_loop"
	ErrAborted           = "aborted"
)

// NewFlow creates a flow for traffic that does not come through the proxy listener
// (Chrome DevTools), so it can run through the same hooks.
func NewFlow(clientAddr, scheme string) *Flow { return newFlow(clientAddr, scheme) }
