package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/hakanor/proxypilot/internal/events"
	"github.com/hakanor/proxypilot/internal/store"
)

const (
	batchInterval = 100 * time.Millisecond
	batchMax      = 200
	subBuffer     = 2048
	pingInterval  = 15 * time.Second
	pingTimeout   = 30 * time.Second
	writeTimeout  = 10 * time.Second
)

// FlowRow is the lightweight flow sent on the stream (enough for a table row).
type FlowRow struct {
	ID          string    `json:"id"`
	Method      string    `json:"method"`
	Scheme      string    `json:"scheme"`
	Host        string    `json:"host"`
	Path        string    `json:"path"`
	URL         string    `json:"url"`
	Status      int       `json:"status,omitempty"`
	State       string    `json:"state"`
	ContentType string    `json:"content_type,omitempty"`
	BytesUp     int64     `json:"bytes_up"`
	BytesDown   int64     `json:"bytes_down"`
	RespSize    int64     `json:"resp_size"`
	TotalMs     float64   `json:"total_ms"`
	ErrorKind   string    `json:"error_kind,omitempty"`
	ClientLabel string    `json:"client_label,omitempty"`
	Source      string    `json:"source"`
	Rules       []string  `json:"matched_rules,omitempty"`
	Tab         string    `json:"tab,omitempty"` // Chrome tab title (source=cdp)
	GQLOp       string    `json:"gql_op,omitempty"`
	Device      string    `json:"device,omitempty"`
	App         string    `json:"app,omitempty"`
	GQLType     string    `json:"gql_type,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func toRow(f *store.Flow) FlowRow {
	return FlowRow{ID: f.ID, Method: f.Method, Scheme: f.Scheme, Host: f.Host, Path: f.Path, URL: f.URL, Status: f.Status,
		State: f.State, ContentType: f.RespContentType, BytesUp: f.BytesUp, BytesDown: f.BytesDown, RespSize: f.RespSize,
		TotalMs: f.Timing.Total, ErrorKind: f.ErrorKind, ClientLabel: f.ClientLabel, Source: f.Source, Rules: f.MatchedRules, Tab: f.Tags["tab_title"], GQLOp: f.Tags["gql_op"], GQLType: f.Tags["gql_type"], Device: f.Tags["device"], App: f.Tags["app"], CreatedAt: f.CreatedAt}
}

// wireEvent is an event as sent to clients.
type wireEvent struct {
	Seq  uint64    `json:"seq"`
	Type string    `json:"type"`
	Time time.Time `json:"time"`
	Data any       `json:"data,omitempty"`
}

func toWire(e events.Event) wireEvent {
	w := wireEvent{Seq: e.Seq, Type: e.Type, Time: e.Time, Data: e.Data}
	if f, ok := e.Data.(*store.Flow); ok {
		w.Data = toRow(f)
	}
	return w
}

// subscription is the client's choice of events and flow filter.
type subscription struct {
	Type   string   `json:"type"`
	Events []string `json:"events"`
	Filter struct {
		Host   string `json:"host"`
		Status string `json:"status"`
		Method string `json:"method"`
	} `json:"filter"`
}

// matcher is a compiled subscription. A nil *matcher matches nothing.
type matcher struct {
	patterns []string
	host     func(string) bool
	status   func(int) bool
	methods  map[string]bool
}

func compile(sub subscription) (*matcher, error) {
	m := &matcher{patterns: sub.Events}
	if len(m.patterns) == 0 {
		m.patterns = []string{"*"}
	}
	if sub.Filter.Host != "" {
		m.host = store.MatchHost(sub.Filter.Host)
	}
	if sub.Filter.Status != "" {
		st, err := store.MatchStatus(sub.Filter.Status)
		if err != nil {
			return nil, err
		}
		m.status = st
	}
	if sub.Filter.Method != "" {
		m.methods = map[string]bool{}
		for _, x := range splitList(sub.Filter.Method) {
			m.methods[strings.ToUpper(x)] = true
		}
	}
	return m, nil
}

func (m *matcher) match(e events.Event) bool {
	if m == nil {
		return false
	}
	ok := false
	for _, p := range m.patterns {
		if p == "*" || p == e.Type || (strings.HasSuffix(p, ".*") && strings.HasPrefix(e.Type, strings.TrimSuffix(p, "*"))) {
			ok = true
			break
		}
	}
	if !ok {
		return false
	}
	f, isFlow := e.Data.(*store.Flow)
	if !isFlow {
		return true // filters apply to flow events only
	}
	if m.host != nil && !m.host(f.Host) {
		return false
	}
	if m.status != nil && !m.status(f.Status) {
		return false
	}
	if m.methods != nil && !m.methods[f.Method] {
		return false
	}
	return true
}

// handleStreamWS: client connects, sends {"type":"subscribe",...}; server sends
// {"type":"batch","events":[...]} at most every 100 ms or 200 events, and
// {"type":"dropped","count":N} when this client fell behind.
func (s *Server) handleStreamWS(w http.ResponseWriter, r *http.Request) {
	// Host, Origin and auth were checked by secure(); skip the library's own check.
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(64 << 10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var mu sync.RWMutex
	var m *matcher
	sub := s.deps.Bus.Subscribe(func(e events.Event) bool {
		mu.RLock()
		defer mu.RUnlock()
		return m.match(e)
	}, subBuffer)
	defer sub.Cancel()

	send := func(v any) error {
		wctx, wcancel := context.WithTimeout(ctx, writeTimeout)
		defer wcancel()
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return c.Write(wctx, websocket.MessageText, b)
	}

	// Reader: subscription updates. Ending the read loop ends the connection.
	go func() {
		defer cancel()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			var msg subscription
			if err := json.Unmarshal(data, &msg); err != nil || msg.Type != "subscribe" {
				_ = send(map[string]string{"type": "error", "message": "expected {\"type\":\"subscribe\",\"events\":[...]}"})
				continue
			}
			nm, err := compile(msg)
			if err != nil {
				_ = send(map[string]string{"type": "error", "message": err.Error()})
				continue
			}
			mu.Lock()
			m = nm
			mu.Unlock()
			_ = send(map[string]any{"type": "subscribed", "events": nm.patterns})
		}
	}()

	// Keepalive.
	go func() {
		t := time.NewTicker(pingInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pctx, pcancel := context.WithTimeout(ctx, pingTimeout)
				err := c.Ping(pctx)
				pcancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	err = s.pump(ctx, sub, func(batch []wireEvent, dropped uint64) error {
		if dropped > 0 {
			if err := send(map[string]any{"type": "dropped", "count": dropped}); err != nil {
				return err
			}
		}
		if len(batch) == 0 {
			return nil
		}
		return send(map[string]any{"type": "batch", "events": batch})
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		s.log.Debug("stream closed", "err", err)
	}
	c.Close(websocket.StatusNormalClosure, "")
}

// pump batches events from sub and calls flush until ctx ends or flush fails.
func (s *Server) pump(ctx context.Context, sub *events.Subscription, flush func([]wireEvent, uint64) error) error {
	t := time.NewTicker(batchInterval)
	defer t.Stop()
	batch := make([]wireEvent, 0, batchMax)
	var dropped uint64
	do := func() error {
		if len(batch) == 0 && dropped == 0 {
			return nil
		}
		err := flush(batch, dropped)
		batch, dropped = batch[:0], 0
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e, ok := <-sub.C:
			if !ok {
				return nil
			}
			dropped += e.Dropped
			batch = append(batch, toWire(e))
			if len(batch) >= batchMax {
				if err := do(); err != nil {
					return err
				}
			}
		case <-t.C:
			if err := do(); err != nil {
				return err
			}
		}
	}
}

// handleStreamNDJSON streams one JSON event per line (curl, `proxypilot tail`).
// Query: events=flow.*,breakpoint.* host= status= method=
func (s *Server) handleStreamNDJSON(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var sub subscription
	if ev := q.Get("events"); ev != "" {
		sub.Events = splitList(ev)
	}
	sub.Filter.Host, sub.Filter.Status, sub.Filter.Method = q.Get("host"), q.Get("status"), q.Get("method")
	m, err := compile(sub)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal", "streaming unsupported")
		return
	}
	bs := s.deps.Bus.Subscribe(m.match, subBuffer)
	defer bs.Cancel()

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	fl.Flush()

	redactRows := wantRedact(r)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = s.pump(r.Context(), bs, func(batch []wireEvent, dropped uint64) error {
		if dropped > 0 {
			if err := enc.Encode(map[string]any{"type": "dropped", "count": dropped}); err != nil {
				return err
			}
		}
		for _, e := range batch {
			if row, ok := e.Data.(FlowRow); ok && redactRows {
				row.URL = s.redactor().URL(row.URL)
				e.Data = row
			}
			if err := enc.Encode(e); err != nil {
				return err
			}
		}
		fl.Flush()
		return nil
	})
}
