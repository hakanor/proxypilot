// Package events is the in-process event bus. Producers never block: a slow
// subscriber loses events and is told how many through Event.Dropped.
package events

import (
	"sync"
	"sync/atomic"
	"time"
)

// Event types.
const (
	FlowStarted       = "flow.started"
	FlowUpdated       = "flow.updated"
	FlowCompleted     = "flow.completed"
	FlowFailed        = "flow.failed"
	BreakpointPaused  = "breakpoint.paused"
	BreakpointResumed = "breakpoint.resumed"
	RuleChanged       = "rule.changed"
	DeviceConnected   = "device.connected"
	ProxyState        = "proxy.state"
	Suggestion        = "suggestion"
	RecordingChanged  = "recording.changed"
	WSMessage         = "ws.message" // one WebSocket message of an open flow
)

type Event struct {
	Seq  uint64    `json:"seq"`
	Type string    `json:"type"`
	Time time.Time `json:"time"`
	Data any       `json:"data,omitempty"`
	// Dropped is the number of events this subscriber missed right before this one.
	Dropped uint64 `json:"dropped,omitempty"`
}

// Filter selects events for a subscriber; nil accepts everything.
type Filter func(Event) bool

// Types builds a Filter matching any of the given types.
func Types(types ...string) Filter {
	set := make(map[string]bool, len(types))
	for _, t := range types {
		set[t] = true
	}
	return func(e Event) bool { return set[e.Type] }
}

type Bus struct {
	seq  atomic.Uint64
	mu   sync.RWMutex
	subs map[*Subscription]struct{}
}

func NewBus() *Bus { return &Bus{subs: map[*Subscription]struct{}{}} }

type Subscription struct {
	C <-chan Event

	ch      chan Event
	filter  Filter
	bus     *Bus
	pending atomic.Uint64 // drops not yet reported
	total   atomic.Uint64
	once    sync.Once
	mu      sync.Mutex // serializes sends with close
	closed  bool
}

// Subscribe registers a subscriber with a buffered channel (min 1).
func (b *Bus) Subscribe(filter Filter, buffer int) *Subscription {
	if buffer < 1 {
		buffer = 1
	}
	ch := make(chan Event, buffer)
	s := &Subscription{C: ch, ch: ch, filter: filter, bus: b}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// Cancel unsubscribes and closes C.
func (s *Subscription) Cancel() {
	s.once.Do(func() {
		s.bus.mu.Lock()
		delete(s.bus.subs, s)
		s.bus.mu.Unlock()
		s.mu.Lock()
		s.closed = true
		close(s.ch)
		s.mu.Unlock()
	})
}

// Dropped is the total number of events this subscriber has missed.
func (s *Subscription) Dropped() uint64 { return s.total.Load() }

// Publish stamps and delivers e to every matching subscriber without blocking.
func (b *Bus) Publish(typ string, data any) Event {
	e := Event{Seq: b.seq.Add(1), Type: typ, Time: time.Now(), Data: data}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for s := range b.subs {
		if s.filter != nil && !s.filter(e) {
			continue
		}
		s.deliver(e)
	}
	return e
}

func (s *Subscription) deliver(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	e.Dropped = s.pending.Load()
	select {
	case s.ch <- e:
		s.pending.Store(0)
	default:
		s.pending.Add(1)
		s.total.Add(1)
	}
}

// Subscribers reports the current subscriber count.
func (b *Bus) Subscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}
