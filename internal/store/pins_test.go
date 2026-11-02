package store

import (
	"context"
	"testing"
)

func TestPinnedFlowsSurviveRetention(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir(), Options{MaxEntries: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, id := range []string{"a", "b", "c"} {
		if err := s.Insert(ctx, &Flow{ID: id, Method: "GET", URL: "http://x/" + id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetPin(ctx, "a", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPin(ctx, "nope", true); err != ErrNotFound {
		t.Fatalf("pin of a missing flow: %v", err)
	}
	if _, err := s.EnforceRetention(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "a"); err != nil {
		t.Fatalf("pinned flow removed: %v", err)
	}
	if _, err := s.Clear(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if ids, _ := s.Pins(ctx); len(ids) != 0 {
		t.Fatalf("pins after clear: %v", ids)
	}
}
