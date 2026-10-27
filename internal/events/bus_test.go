package events

import (
	"testing"
	"time"
)

func TestSlowSubscriberDoesNotBlockOthers(t *testing.T) {
	b := NewBus()
	slow := b.Subscribe(nil, 1) // never read
	fast := b.Subscribe(nil, 1000)
	defer slow.Cancel()
	defer fast.Cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			b.Publish(FlowCompleted, i)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publisher blocked by slow subscriber")
	}

	for i := 0; i < 500; i++ {
		e := <-fast.C
		if e.Data.(int) != i || e.Dropped != 0 {
			t.Fatalf("fast subscriber got %v (dropped %d) at %d", e.Data, e.Dropped, i)
		}
	}
	if slow.Dropped() != 499 {
		t.Fatalf("slow dropped = %d, want 499", slow.Dropped())
	}
	<-slow.C // drain the one buffered event
	b.Publish(FlowCompleted, "next")
	e := <-slow.C
	if e.Dropped != 499 {
		t.Fatalf("drop count not reported to slow subscriber: %d", e.Dropped)
	}
}

func TestFilterAndCancel(t *testing.T) {
	b := NewBus()
	s := b.Subscribe(Types(FlowFailed), 10)
	b.Publish(FlowCompleted, nil)
	b.Publish(FlowFailed, "x")
	e := <-s.C
	if e.Type != FlowFailed || e.Seq != 2 {
		t.Fatalf("got %+v", e)
	}
	s.Cancel()
	s.Cancel() // idempotent
	if _, ok := <-s.C; ok {
		t.Fatal("channel not closed")
	}
	b.Publish(FlowFailed, nil) // must not panic
	if b.Subscribers() != 0 {
		t.Fatal("subscriber not removed")
	}
}
