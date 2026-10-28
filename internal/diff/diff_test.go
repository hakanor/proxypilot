package diff

import (
	"reflect"
	"strings"
	"testing"

	"github.com/hakanor/proxypilot/internal/store"
)

func TestHeadersCaseInsensitive(t *testing.T) {
	a := []store.Header{{Name: "Content-Type", Value: "application/json"}, {Name: "X-App-Version", Value: "4.1.0"}, {Name: "X-Old", Value: "1"}}
	b := []store.Header{{Name: "content-type", Value: "application/json"}, {Name: "X-App-Version", Value: "4.2.0"}, {Name: "X-New", Value: "2"}}
	want := []Change{
		{Path: "x-app-version", Kind: "changed", A: "4.1.0", B: "4.2.0"},
		{Path: "x-new", Kind: "added", B: "2"},
		{Path: "x-old", Kind: "removed", A: "1"},
	}
	if got := Headers(a, b); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestURLQueryParams(t *testing.T) {
	got := URL("https://api.x.com/v2/cart?page=1&sort=asc", "https://api.x.com/v2/cart?page=2&lang=tr")
	want := []Change{
		{Path: "query.lang", Kind: "added", B: "tr"},
		{Path: "query.page", Kind: "changed", A: "1", B: "2"},
		{Path: "query.sort", Kind: "removed", A: "asc"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestJSONByPath(t *testing.T) {
	a := `{"productId":123,"currency":"TRY","items":[{"q":1}],"tags":[]}`
	b := `{"productId": 123, "items": [{"q": 2}, {"q": 1}], "tags": []}`
	got := Compare([]byte(a), []byte(b))
	want := []Change{
		{Path: "$.currency", Kind: "removed", A: `"TRY"`},
		{Path: "$.items[0].q", Kind: "changed", A: "1", B: "2"},
		{Path: "$.items[1].q", Kind: "added", B: "1"},
	}
	if got.Mode != "json" || !reflect.DeepEqual(got.Changes, want) {
		t.Fatalf("got %+v", got)
	}
	if m := Compare([]byte(`{"a":1,"b":2}`), []byte("{\n  \"b\": 2,\n  \"a\": 1\n}")).Mode; m != "same" {
		t.Fatalf("reformatted JSON should be same, got %s", m)
	}
}

func TestTextLinesWithContext(t *testing.T) {
	var a, b []string
	for i := range 20 {
		a = append(a, "line "+string(rune('a'+i)))
	}
	b = append(b, a...)
	b[10] = "changed"
	got := Compare([]byte(strings.Join(a, "\n")), []byte(strings.Join(b, "\n")))
	if got.Mode != "text" {
		t.Fatalf("mode %s", got.Mode)
	}
	var ops string
	for _, l := range got.Lines {
		ops += l.Op
	}
	// … then 3 context lines, the change, 3 context lines, …
	if ops != "…   -+   …" {
		t.Fatalf("ops %q: %+v", ops, got.Lines)
	}
}

func TestBinaryAndEmpty(t *testing.T) {
	if got := Compare([]byte{0xff, 0x00}, []byte{0xff}); got.Mode != "binary" || got.Changes[0].Path != "size" {
		t.Fatalf("got %+v", got)
	}
	if got := Compare(nil, nil); got.Mode != "empty" {
		t.Fatalf("got %+v", got)
	}
	if got := Compare(nil, []byte("hi")); got.Mode != "text" || len(got.Lines) != 2 {
		t.Fatalf("got %+v", got)
	}
}

func TestBigTextStaysBounded(t *testing.T) {
	a := strings.Repeat("a\n", 5000)
	b := strings.Repeat("b\n", 5000)
	got := Compare([]byte(a), []byte(b))
	if !got.Truncated || len(got.Lines) > maxLines {
		t.Fatalf("lines=%d truncated=%v", len(got.Lines), got.Truncated)
	}
}

func TestFlowsSame(t *testing.T) {
	f := &store.Flow{ID: "1", Method: "GET", URL: "https://x.com/a", Status: 200}
	g := *f
	g.ID = "2"
	if r := Flows(f, &g, Bodies{RespA: []byte("ok"), RespB: []byte("ok")}); !r.Same {
		t.Fatalf("got %+v", r)
	}
	g.Status = 500
	r := Flows(f, &g, Bodies{})
	if r.Same || !reflect.DeepEqual(r.Response.Line, []Change{{Path: "status", Kind: "changed", A: "200", B: "500"}}) {
		t.Fatalf("got %+v", r)
	}
}
