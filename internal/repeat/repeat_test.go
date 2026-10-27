package repeat

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hakanor/proxypilot/internal/capture"
	"github.com/hakanor/proxypilot/internal/events"
	"github.com/hakanor/proxypilot/internal/proxy"
	"github.com/hakanor/proxypilot/internal/store"
)

func TestCurlRoundTrip(t *testing.T) {
	in := Request{Method: "PUT", URL: "https://api.x.com/v2/items/3?tag=a b", Headers: []store.Header{
		{Name: "Authorization", Value: "Bearer it's-a-token"}, {Name: "Content-Type", Value: "application/json"}, {Name: "Accept-Encoding", Value: "gzip"},
	}, Body: `{"name":"O'Brien","note":"a\\b $HOME"}`}
	cmd, binary := Curl(in)
	if binary || !strings.Contains(cmd, "-X PUT") || !strings.Contains(cmd, "--compressed") {
		t.Fatalf("curl: %s", cmd)
	}
	out, err := ParseCurl(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if out.Method != "PUT" || out.URL != in.URL || out.Body != in.Body {
		t.Fatalf("round trip:\n%+v\n%+v", in, out)
	}
	got := map[string]string{}
	for _, h := range out.Headers {
		got[h.Name] = h.Value
	}
	if got["Authorization"] != "Bearer it's-a-token" || got["Content-Type"] != "application/json" {
		t.Fatalf("headers: %v", got)
	}
	// The exported command must parse the same in a real shell.
	if sh, err := runShell("sh", "-c", "printf '%s\\n' "+strings.ReplaceAll(strings.TrimPrefix(cmd, "curl"), "\\\n", "")); err == nil {
		if !strings.Contains(sh, `{"name":"O'Brien","note":"a\\b $HOME"}`) {
			t.Fatalf("shell sees a different body:\n%s", sh)
		}
	}
	if c, _ := Curl(Request{Method: "GET", URL: "http://x"}); strings.Contains(c, "-X") {
		t.Fatalf("plain GET: %s", c)
	}
	if c, _ := Curl(Request{Method: "POST", URL: "http://x", Body: "a=1"}); strings.Contains(c, "-X") {
		t.Fatalf("POST with body: %s", c)
	}
	if _, bin := Curl(Request{Method: "POST", URL: "http://x", Body: "\xff\xfe"}); !bin {
		t.Fatal("binary body not flagged")
	}
}

func TestParseDevtoolsCurl(t *testing.T) {
	cmd := `curl 'https://api.example.com/graphql' \
  -H 'accept: */*' \
  -H 'content-type: application/json' \
  -b 'sid=abc; theme=dark' \
  --data-raw '{"query":"{ me { id } }"}' \
  --compressed`
	r, err := ParseCurl(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if r.Method != "POST" || r.URL != "https://api.example.com/graphql" || r.Body != `{"query":"{ me { id } }"}` || len(r.Headers) != 4 {
		t.Fatalf("%+v", r)
	}
	r, _ = ParseCurl(`curl -G "http://x.test/search" -d q=shoes -d page=2 -u user:pw`)
	if r.Method != "GET" || r.URL != "http://x.test/search?q=shoes&page=2" || r.Headers[0].Value != "Basic dXNlcjpwdw==" {
		t.Fatalf("-G: %+v", r)
	}
	for _, bad := range []string{"wget http://x", "curl -H", `curl 'unterminated`, "curl -s"} {
		if _, err := ParseCurl(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func setup(t *testing.T) (*Repeater, *store.Store, *httptest.Server, chan *http.Request) {
	t.Helper()
	st, err := store.Open(t.TempDir(), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	seen := make(chan *http.Request, 100)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(strings.NewReader(string(b)))
		seen <- r
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, r.Method+" "+r.URL.RequestURI()+" "+string(b))
	}))
	t.Cleanup(up.Close)
	rec := capture.New(st, events.NewBus(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := proxy.New(proxy.Options{Hooks: rec})
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Serve(ctx, ln)
	return &Repeater{Proxy: srv, Store: st, Hooks: rec, Bypass: rec}, st, up, seen
}

func TestComposeRepeatAndLimits(t *testing.T) {
	r, st, up, seen := setup(t)
	ctx := context.Background()
	sum, err := r.Send(ctx, Request{Method: "POST", URL: up.URL + "/orders?x=1", Headers: []store.Header{{Name: "X-Test", Value: "1"}}, Body: "a=1"}, Options{}, nil)
	if err != nil || len(sum.Results) != 1 || sum.Results[0].Status != 200 {
		t.Fatalf("compose: %+v %v", sum, err)
	}
	got := <-seen
	if got.Header.Get("X-Test") != "1" || got.Method != "POST" {
		t.Fatalf("upstream saw %+v", got)
	}
	id := sum.Results[0].FlowID
	time.Sleep(100 * time.Millisecond)
	f, err := st.Get(ctx, id)
	if err != nil || f.Source != store.SourceComposer {
		t.Fatalf("recorded flow: %+v %v", f, err)
	}

	// Repeat with edits, 5 times, 3 at once.
	base, err := r.FromFlow(ctx, id)
	if err != nil || base.Body != "a=1" {
		t.Fatalf("from flow: %+v %v", base, err)
	}
	body := "a=2"
	edited, _ := Edits{SetHeaders: map[string]string{"X-Test": "2"}, SetQuery: map[string]string{"x": "9"}, Body: &body}.Apply(base)
	sum, err = r.Send(ctx, edited, Options{Count: 5, Concurrency: 3}, map[string]string{"repeat_of": id})
	if err != nil || len(sum.Results) != 5 || sum.ByStatus["2xx"] != 5 {
		t.Fatalf("repeat: %+v %v", sum, err)
	}
	for i := 0; i < 5; i++ {
		g := <-seen
		if g.Header.Get("X-Test") != "2" || g.URL.Query().Get("x") != "9" {
			t.Fatalf("edited request: %v %v", g.Header, g.URL)
		}
	}

	for _, o := range []Options{{Count: MaxCount + 1}, {Count: 1, Concurrency: MaxConcurrency + 1}, {Count: -1}} {
		if _, err := r.Send(ctx, base, o, nil); !errors.Is(err, ErrLimit) {
			t.Errorf("%+v: %v", o, err)
		}
	}
	if _, err := r.Send(ctx, Request{URL: "/relative"}, Options{}, nil); err == nil {
		t.Error("relative URL accepted")
	}
}
