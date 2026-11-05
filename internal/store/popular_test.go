package store

import (
	"context"
	"testing"
)

func TestPopularGroupsByHost(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, Options{})
	for _, u := range [][2]string{
		{"www.mobile.example.com", "/campaigns?page=1"},
		{"www.mobile.example.com", "/campaigns?page=2"},
		{"WWW.mobile.example.com", "/cards"},
		{"api.other.com", "/v1"},
		{"", "/ignored"},
	} {
		if err := s.BumpPopular(ctx, u[0], u[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Insert(ctx, &Flow{ID: "f", Method: "GET", Host: "api.other.com", Path: "/v1", URL: "http://api.other.com/v1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.BumpPopularFlow(ctx, "f"); err != nil {
		t.Fatal(err)
	}
	if err := s.BumpPopularFlow(ctx, "missing"); err != ErrNotFound {
		t.Fatalf("missing flow: %v", err)
	}
	got, err := s.Popular(ctx, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Host != "www.mobile.example.com" || got[0].Hits != 3 || len(got[0].Paths) != 1 || got[0].Paths[0] != (PopularPath{"/campaigns", 2}) || got[1].Hits != 2 {
		t.Fatalf("popular: %+v", got)
	}
	if _, err := s.Clear(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Popular(ctx, 10, 0); len(got) != 2 {
		t.Fatalf("Clear must keep popular: %+v", got)
	}
	if err := s.ClearPopular(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Popular(ctx, 10, 0); len(got) != 0 {
		t.Fatalf("after ClearPopular: %+v", got)
	}
}
