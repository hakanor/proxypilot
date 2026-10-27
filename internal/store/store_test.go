package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
)

func openTest(t *testing.T, opts Options) *Store {
	t.Helper()
	s, err := Open(t.TempDir(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func bytesOpener(b []byte) Opener {
	return func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }
}

var ctx = context.Background()

func seed(t *testing.T, s *Store, n int, mk func(i int) *Flow) []*Flow {
	t.Helper()
	base := time.Now().Add(-time.Hour)
	out := make([]*Flow, n)
	for i := 0; i < n; i++ {
		f := mk(i)
		f.CreatedAt = base.Add(time.Duration(i) * time.Second)
		if err := s.Insert(ctx, f); err != nil {
			t.Fatal(err)
		}
		out[i] = f
	}
	return out
}

func TestInsertGetUpdate(t *testing.T) {
	s := openTest(t, Options{})
	f := &Flow{Method: "GET", Scheme: "https", Host: "api.example.com", Port: 443, Path: "/v1", URL: "https://api.example.com/v1",
		ReqHeaders: []Header{{"Accept", "application/json"}, {"X-A", "1"}}}
	if err := s.Insert(ctx, f); err != nil {
		t.Fatal(err)
	}
	f.Status, f.State, f.BytesDown = 200, StateCompleted, 1234
	f.RespHeaders = []Header{{"Content-Type", "application/json"}}
	f.Timing = Timing{TTFB: 12.5, Total: 40}
	if err := s.Update(ctx, f); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != 200 || got.State != StateCompleted || got.BytesDown != 1234 || got.Timing.Total != 40 {
		t.Fatalf("update lost: %+v", got)
	}
	if len(got.ReqHeaders) != 2 || got.ReqHeaders[1].Name != "X-A" || len(got.RespHeaders) != 1 {
		t.Fatalf("headers: %+v / %+v", got.ReqHeaders, got.RespHeaders)
	}
	if _, err := s.Get(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestListDefaultLimitAndCursor(t *testing.T) {
	s := openTest(t, Options{})
	seed(t, s, 120, func(i int) *Flow { return &Flow{Method: "GET", Host: "h.test", URL: "http://h.test/"} })

	p, err := s.List(ctx, Filter{}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Flows) != DefaultLimit || p.NextCursor == "" {
		t.Fatalf("default limit not applied: %d, cursor=%q", len(p.Flows), p.NextCursor)
	}
	if n := ClampLimit(10000); n != MaxLimit {
		t.Fatalf("max limit = %d", n)
	}

	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		p, err := s.List(ctx, Filter{}, 50, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for i, f := range p.Flows {
			if seen[f.ID] {
				t.Fatalf("duplicate %s on page %d", f.ID, pages)
			}
			seen[f.ID] = true
			if i > 0 && f.CreatedAt.After(p.Flows[i-1].CreatedAt) {
				t.Fatal("not newest-first")
			}
		}
		pages++
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	if len(seen) != 120 || pages != 3 {
		t.Fatalf("paged %d flows in %d pages", len(seen), pages)
	}
}

func TestFilters(t *testing.T) {
	s := openTest(t, Options{})
	hosts := []string{"api.invio.com", "cdn.invio.com", "example.org"}
	statuses := []int{200, 404, 500, 301}
	methods := []string{"GET", "POST"}
	seed(t, s, 24, func(i int) *Flow {
		f := &Flow{Method: methods[i%2], Host: hosts[i%3], Status: statuses[i%4], RespContentType: "application/json", Source: SourceProxy}
		f.URL = "https://" + f.Host + "/item/" + string(rune('a'+i))
		if i%4 == 2 {
			f.ErrorKind = "upstream_connect_error"
		}
		if i%6 == 0 {
			f.RespContentType = "image/png"
		}
		return f
	})
	count := func(f Filter) int {
		p, err := s.List(ctx, f, 500, "")
		if err != nil {
			t.Fatal(err)
		}
		return len(p.Flows)
	}
	yes, no := true, false
	cases := []struct {
		name string
		f    Filter
		want int
	}{
		{"wildcard host", Filter{Host: "*.invio.com"}, 16},
		{"exact host", Filter{Host: "example.org"}, 8},
		{"method", Filter{Methods: []string{"post"}}, 12},
		{"2xx", Filter{Status: "2xx"}, 6},
		{"4xx,5xx", Filter{Status: "4xx,5xx"}, 12},
		{">=500", Filter{Status: ">=500"}, 6},
		{"range", Filter{Status: "300-404"}, 12},
		{"exact status", Filter{Status: "404"}, 6},
		{"content type", Filter{ContentType: "png"}, 4},
		{"errors", Filter{HasError: &yes}, 6},
		{"no errors", Filter{HasError: &no}, 18},
		{"text", Filter{Text: "/item/a"}, 1},
		{"combo", Filter{Host: "*.invio.com", Methods: []string{"GET"}, Status: "2xx"}, 4},
	}
	for _, c := range cases {
		if got := count(c.f); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
	if _, err := s.List(ctx, Filter{Status: "abc"}, 10, ""); err == nil {
		t.Error("bad status filter accepted")
	}
}

func TestGzipBodyDecoded(t *testing.T) {
	s := openTest(t, Options{})
	f := &Flow{Method: "GET", URL: "http://x/"}
	if err := s.Insert(ctx, f); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(`{"hello":"world"}`))
	zw.Close()
	raw := buf.Bytes()

	info, err := s.PutBody(ctx, f.ID, SideResp, "gzip", int64(len(raw)), bytesOpener(raw))
	if err != nil {
		t.Fatal(err)
	}
	if info.DecodeError != "" || info.DecodedSize != 17 || info.RawSize != int64(len(raw)) {
		t.Fatalf("info = %+v", info)
	}
	b, trunc, _, err := s.GetBody(ctx, f.ID, SideResp, 0)
	if err != nil || trunc || string(b) != `{"hello":"world"}` {
		t.Fatalf("body=%q trunc=%v err=%v", b, trunc, err)
	}

	// brotli
	var bb bytes.Buffer
	bw := brotli.NewWriter(&bb)
	bw.Write([]byte("brotli body"))
	bw.Close()
	if _, err := s.PutBody(ctx, f.ID, SideReq, "br", int64(bb.Len()), bytesOpener(bb.Bytes())); err != nil {
		t.Fatal(err)
	}
	if b, _, _, _ := s.GetBody(ctx, f.ID, SideReq, 0); string(b) != "brotli body" {
		t.Fatalf("brotli body = %q", b)
	}

	// broken gzip falls back to raw
	info, err = s.PutBody(ctx, f.ID, SideResp, "gzip", 5, bytesOpener([]byte("plain")))
	if err != nil {
		t.Fatal(err)
	}
	if info.DecodeError == "" {
		t.Fatal("expected decode error")
	}
	if b, _, _, _ := s.GetBody(ctx, f.ID, SideResp, 0); string(b) != "plain" {
		t.Fatalf("raw fallback = %q", b)
	}
}

func TestLargeBodyOnDiskAndRetention(t *testing.T) {
	s := openTest(t, Options{MaxEntries: 5, InlineBodyLimit: 16})
	flows := seed(t, s, 8, func(i int) *Flow { return &Flow{Method: "GET", URL: "http://x/"} })
	big := []byte(strings.Repeat("z", 100))
	var paths []string
	for _, f := range flows {
		info, err := s.PutBody(ctx, f.ID, SideResp, "", 100, bytesOpener(big))
		if err != nil {
			t.Fatal(err)
		}
		if !info.OnDisk {
			t.Fatal("large body should be on disk")
		}
		_, _, p, _ := s.bodyRow(ctx, f.ID, SideResp)
		paths = append(paths, p)
	}
	n, err := s.EnforceRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("removed %d, want 3", n)
	}
	for i, p := range paths {
		_, err := os.Stat(p)
		if i < 3 && !os.IsNotExist(err) {
			t.Fatalf("body file of evicted flow %d still exists", i)
		}
		if i >= 3 && err != nil {
			t.Fatalf("body file of kept flow %d missing: %v", i, err)
		}
	}
	if c, _ := s.Count(ctx); c != 5 {
		t.Fatalf("count = %d", c)
	}
}

func TestMaxBodyTruncates(t *testing.T) {
	s := openTest(t, Options{InlineBodyLimit: 8, MaxBodySize: 32})
	f := &Flow{URL: "http://x/"}
	s.Insert(ctx, f)
	info, err := s.PutBody(ctx, f.ID, SideResp, "", 100, bytesOpener(bytes.Repeat([]byte("a"), 100)))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Truncated || info.DecodedSize != 32 {
		t.Fatalf("info = %+v", info)
	}
	b, trunc, _, _ := s.GetBody(ctx, f.ID, SideResp, 1000)
	if len(b) != 32 || !trunc {
		t.Fatalf("len=%d trunc=%v", len(b), trunc)
	}
}

func TestClearAndSummary(t *testing.T) {
	s := openTest(t, Options{})
	seed(t, s, 4, func(i int) *Flow {
		return &Flow{Host: "a.test", Status: []int{200, 200, 404, 0}[i], BytesDown: 100, ErrorKind: []string{"", "", "", "timeout"}[i]}
	})
	sum, err := s.Summary(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Total != 4 || sum.ByStatus["2xx"] != 2 || sum.ByStatus["4xx"] != 1 || sum.ByStatus["none"] != 1 || sum.Errors != 1 || sum.BytesDown != 400 {
		t.Fatalf("summary = %+v", sum)
	}
	old := s.CurrentSession()
	sess, err := s.Clear(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID == old || s.CurrentSession() != sess.ID {
		t.Fatal("clear did not start a new session")
	}
	if c, _ := s.Count(ctx); c != 0 {
		t.Fatalf("count after clear = %d", c)
	}
}

func TestSummaryFailuresAndHeaderSearch(t *testing.T) {
	s := openTest(t, Options{})
	mk := func(method, path string, status int, kind string, ms float64) {
		f := &Flow{Method: method, Host: "api.x.com", Path: path, URL: "https://api.x.com" + path, Status: status, ErrorKind: kind,
			State: StateCompleted, Timing: Timing{Total: ms}, ReqHeaders: []Header{{"X-Trace", "trace-" + path}}}
		if err := s.Insert(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	mk("POST", "/checkout", 500, "", 120)
	mk("POST", "/checkout", 500, "", 90)
	mk("GET", "/user", 401, "", 30)
	mk("GET", "/big.json", 200, "", 3200)
	sum, err := s.Summary(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Failures) != 2 || sum.Failures[0].Path != "/checkout" || sum.Failures[0].Count != 2 {
		t.Fatalf("failures = %+v", sum.Failures)
	}
	if sum.Slowest == nil || sum.Slowest.TotalMs != 3200 {
		t.Fatalf("slowest = %+v", sum.Slowest)
	}
	p, err := s.List(ctx, Filter{Text: "trace-/user", HeaderSearch: true}, 10, "")
	if err != nil || len(p.Flows) != 1 || p.Flows[0].Path != "/user" {
		t.Fatalf("header search: %v %+v", err, p.Flows)
	}
}
