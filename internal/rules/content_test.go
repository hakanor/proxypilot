package rules

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMapLocalContentFileRoundTrip(t *testing.T) {
	e, err := New("", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "resp.json")
	os.WriteFile(f, []byte(`{"a":1}`), 0o644)
	spec, _ := json.Marshal(MapLocal{File: f})
	r, err := e.Add(Rule{Type: TypeMapLocal, Match: Matcher{URL: "https://x/*"}, Spec: spec, Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.MapLocalContent(r.ID)
	if err != nil || c.Content != `{"a":1}` || !c.Editable {
		t.Fatalf("read: %+v %v", c, err)
	}
	if _, err := e.SetMapLocalContent(r.ID, `{"a":2}`); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(f); string(b) != `{"a":2}` {
		t.Fatalf("file = %s", b)
	}
}

func TestMapLocalContentInline(t *testing.T) {
	e, _ := New("", nil, nil)
	body := "hi"
	spec, _ := json.Marshal(MapLocal{Body: &body})
	r, err := e.Add(Rule{Type: TypeMapLocal, Match: Matcher{URL: "https://x/*"}, Spec: spec, Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.SetMapLocalContent(r.ID, "bye")
	if err != nil || c.Content != "bye" || c.Source != "inline" {
		t.Fatalf("%+v %v", c, err)
	}
}
