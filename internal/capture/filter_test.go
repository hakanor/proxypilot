package capture

import (
	"testing"

	"github.com/hakanor/proxypilot/internal/store"
)

func TestWanted(t *testing.T) {
	r := &Recorder{}
	api := &store.Flow{Host: "api.example.com", Source: store.SourceProxy, Tags: map[string]string{"app": "Safari"}}
	if !r.wanted(api) {
		t.Fatal("no filter should record")
	}
	r.SetFilter(Filter{Hosts: []string{"*.example.com"}})
	if !r.wanted(api) || r.wanted(&store.Flow{Host: "other.com", Source: store.SourceProxy}) {
		t.Fatal("host filter")
	}
	r.SetFilter(Filter{Apps: []string{"safari"}})
	if !r.wanted(api) || r.wanted(&store.Flow{Host: "api.example.com", Source: store.SourceProxy, Tags: map[string]string{"app": "Chrome"}}) {
		t.Fatal("app filter")
	}
	if !r.wanted(&store.Flow{Host: "x.com", Source: "composer"}) {
		t.Fatal("composer must always record")
	}
}
