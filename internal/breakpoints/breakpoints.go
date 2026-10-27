// Package breakpoints pauses flows until a person (web UI, CLI) or an agent decides
// what to do: continue unchanged, continue with edits, or abort.
package breakpoints

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/hakanor/proxypilot/internal/events"
)

const (
	PhaseRequest  = "request"
	PhaseResponse = "response"

	ActionContinue = "continue"
	ActionModify   = "modify"
	ActionAbort    = "abort"

	DefaultTimeout = 60 * time.Second
	MaxTimeout     = 30 * time.Minute
	// MaxBody is how much of a body is held for editing.
	MaxBody = 10 << 20
)

var (
	ErrNotFound        = errors.New("breakpoint not found")
	ErrAlreadyResolved = errors.New("breakpoint already resolved")
	ErrInvalid         = errors.New("invalid resolution")
)

type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Request is the editable request (request phase, and context in the response phase).
type Request struct {
	Method  string   `json:"method"`
	URL     string   `json:"url"`
	Headers []Header `json:"headers"`
	Body    string   `json:"body"`
	// BodyBase64 is set instead of Body for binary content.
	BodyBase64 string `json:"body_base64,omitempty"`
}

// Response is the editable response (response phase).
type Response struct {
	Status     int      `json:"status"`
	Headers    []Header `json:"headers"`
	Body       string   `json:"body"`
	BodyBase64 string   `json:"body_base64,omitempty"`
	// Encoding is the upstream Content-Encoding; Body is shown decoded. Echo it back when
	// editing so the header is dropped (the edited body is sent as plain bytes).
	Encoding string `json:"encoding,omitempty"`
}

// Paused is one waiting flow.
type Paused struct {
	ID        string    `json:"id"`
	FlowID    string    `json:"flow_id"`
	Phase     string    `json:"phase"`
	RuleID    string    `json:"rule_id"`
	RuleName  string    `json:"rule_name"`
	Owner     string    `json:"owner"` // who decides: "user" or "agent:<name>"
	Source    string    `json:"source"`
	Client    string    `json:"client,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	OnTimeout string    `json:"on_timeout"`
	Request   Request   `json:"request"`
	Response  *Response `json:"response,omitempty"`
	Truncated bool      `json:"truncated,omitempty"` // body larger than MaxBody: edits to it are not allowed

	decided chan Resolution
}

// Resolution is the decision for a paused flow.
type Resolution struct {
	Action   string    `json:"action"`
	Request  *Request  `json:"request,omitempty"`
	Response *Response `json:"response,omitempty"`
	By       string    `json:"by,omitempty"`
	// TimedOut is set by the manager when the timeout applied the default.
	TimedOut bool `json:"timed_out,omitempty"`
}

type Manager struct {
	bus *events.Bus
	now func() time.Time

	mu      sync.Mutex
	pending map[string]*Paused
	waiters map[chan *Paused]func(*Paused) bool
	// resolved remembers recent ids so a late second decision gets 409, not 404.
	resolved recent
	closed   bool
}

// recent is a bounded set of ids.
type recent struct {
	ids  map[string]struct{}
	ring []string
}

func (r *recent) add(id string) {
	if r.ids == nil {
		r.ids = map[string]struct{}{}
	}
	if len(r.ring) >= 1024 {
		delete(r.ids, r.ring[0])
		r.ring = r.ring[1:]
	}
	r.ids[id] = struct{}{}
	r.ring = append(r.ring, id)
}

func (r *recent) has(id string) bool { _, ok := r.ids[id]; return ok }

func New(bus *events.Bus) *Manager {
	return &Manager{bus: bus, now: time.Now, pending: map[string]*Paused{}, waiters: map[chan *Paused]func(*Paused) bool{}}
}

func (m *Manager) publish(typ string, data any) {
	if m.bus != nil {
		m.bus.Publish(typ, data)
	}
}

// Pause registers p and blocks until it is resolved, it times out, or ctx ends
// (client went away). The returned resolution always has an action.
func (m *Manager) Pause(ctx context.Context, p *Paused, timeout time.Duration) Resolution {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if timeout > MaxTimeout {
		timeout = MaxTimeout
	}
	if p.OnTimeout != ActionAbort {
		p.OnTimeout = ActionContinue
	}
	if p.Owner == "" {
		p.Owner = "user"
	}
	p.ID = "bp_" + strings.ToLower(ulid.Make().String())
	p.CreatedAt = m.now().UTC()
	p.ExpiresAt = p.CreatedAt.Add(timeout)
	p.decided = make(chan Resolution, 1)

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Resolution{Action: ActionContinue, By: "shutdown"}
	}
	m.pending[p.ID] = p
	for ch, match := range m.waiters {
		if match(p) {
			select {
			case ch <- p:
			default:
			}
		}
	}
	snapshot := *p
	m.mu.Unlock()
	m.publish(events.BreakpointPaused, &snapshot)

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var res Resolution
	for res.Action == "" {
		select {
		case res = <-p.decided:
		case <-ctx.Done():
			res = Resolution{Action: ActionAbort, By: "client_gone"}
			m.take(p.ID)
		case <-timer.C:
			m.mu.Lock()
			exp := p.ExpiresAt
			m.mu.Unlock()
			if d := exp.Sub(m.now()); d > 0 { // extended meanwhile
				timer.Reset(d)
				continue
			}
			if m.take(p.ID) {
				res = Resolution{Action: p.OnTimeout, TimedOut: true, By: "timeout"}
			} else {
				res = <-p.decided // resolved at the same moment
			}
		}
	}
	m.publish(events.BreakpointResumed, map[string]any{"id": p.ID, "flow_id": p.FlowID, "phase": p.Phase, "action": res.Action, "by": res.By, "timed_out": res.TimedOut})
	return res
}

// take removes id from pending; false if it was already resolved.
func (m *Manager) take(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.pending[id]; !ok {
		return false
	}
	delete(m.pending, id)
	m.resolved.add(id)
	return true
}

// Resume delivers a decision. The first decision wins; later ones get ErrAlreadyResolved.
func (m *Manager) Resume(id string, r Resolution) error {
	switch r.Action {
	case ActionContinue, ActionAbort:
	case ActionModify:
		if r.Request == nil && r.Response == nil {
			return errors.Join(ErrInvalid, errors.New("modify needs request or response"))
		}
	default:
		return errors.Join(ErrInvalid, errors.New("action must be continue, modify or abort"))
	}
	m.mu.Lock()
	p, ok := m.pending[id]
	if !ok {
		m.mu.Unlock()
		if m.known(id) {
			return ErrAlreadyResolved
		}
		return ErrNotFound
	}
	if r.Action == ActionModify {
		if r.Request != nil && p.Phase != PhaseRequest {
			m.mu.Unlock()
			return errors.Join(ErrInvalid, errors.New("the request can only be edited in the request phase"))
		}
		if r.Response != nil && p.Phase != PhaseResponse {
			m.mu.Unlock()
			return errors.Join(ErrInvalid, errors.New("the response can only be edited in the response phase"))
		}
	}
	delete(m.pending, id)
	m.resolved.add(id)
	m.mu.Unlock()
	p.decided <- r
	return nil
}

// Extend adds d to a pending breakpoint's deadline.
func (m *Manager) Extend(id string, d time.Duration) (*Paused, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pending[id]
	if !ok {
		return nil, ErrNotFound
	}
	limit := p.CreatedAt.Add(MaxTimeout)
	p.ExpiresAt = p.ExpiresAt.Add(d)
	if p.ExpiresAt.After(limit) {
		p.ExpiresAt = limit
	}
	c := *p
	m.publishLocked(events.BreakpointPaused, &c)
	return &c, nil
}

// TakeOver makes the user the decider of an agent-held breakpoint.
func (m *Manager) TakeOver(id, by string) (*Paused, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pending[id]
	if !ok {
		return nil, ErrNotFound
	}
	if by == "" {
		by = "user"
	}
	p.Owner = by
	c := *p
	m.publishLocked(events.BreakpointPaused, &c)
	return &c, nil
}

func (m *Manager) publishLocked(typ string, data any) {
	go m.publish(typ, data) // never publish under the lock
}

func (m *Manager) Get(id string) (*Paused, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pending[id]
	if !ok {
		if m.resolved.has(id) {
			return nil, ErrAlreadyResolved
		}
		return nil, ErrNotFound
	}
	c := *p
	return &c, nil
}

// List returns pending breakpoints, oldest first.
func (m *Manager) List() []*Paused {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Paused, 0, len(m.pending))
	for _, p := range m.pending {
		c := *p
		out = append(out, &c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pending)
}

// Wait returns the oldest pending breakpoint matching match, or blocks for the next one.
func (m *Manager) Wait(ctx context.Context, match func(*Paused) bool) (*Paused, error) {
	if match == nil {
		match = func(*Paused) bool { return true }
	}
	ch := make(chan *Paused, 1)
	m.mu.Lock()
	var first *Paused
	for _, p := range m.pending {
		if match(p) && (first == nil || p.CreatedAt.Before(first.CreatedAt)) {
			first = p
		}
	}
	if first != nil {
		c := *first
		m.mu.Unlock()
		return &c, nil
	}
	m.waiters[ch] = match
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.waiters, ch)
		m.mu.Unlock()
	}()
	select {
	case p := <-ch:
		m.mu.Lock()
		c := *p
		m.mu.Unlock()
		return &c, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ReleaseAll continues every pending flow unchanged (daemon shutdown) and refuses new pauses.
func (m *Manager) ReleaseAll() int {
	m.mu.Lock()
	m.closed = true
	list := make([]*Paused, 0, len(m.pending))
	for id, p := range m.pending {
		list = append(list, p)
		delete(m.pending, id)
		m.resolved.add(id)
	}
	m.mu.Unlock()
	for _, p := range list {
		p.decided <- Resolution{Action: ActionContinue, By: "shutdown"}
	}
	return len(list)
}

func (m *Manager) known(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.resolved.has(id)
}
