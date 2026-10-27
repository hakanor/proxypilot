package archive

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hakanor/proxypilot/internal/capture"
	"github.com/hakanor/proxypilot/internal/events"
	"github.com/hakanor/proxypilot/internal/proxy"
	"github.com/hakanor/proxypilot/internal/redact"
	"github.com/hakanor/proxypilot/internal/store"
)

func record(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir(), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Set-Cookie", "sid=abcdef123456; HttpOnly")
			zw := gzip.NewWriter(w)
			zw.Write([]byte(`{"access_token":"supersecretvalue123456","user":"hakan"}`))
			zw.Close()
		case "/bin":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte{0x89, 'P', 'N', 'G', 0, 0xff, 0xfe})
		case "/graphql":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"errors":[{"message":"Not authorized"}]}`)
		}
	}))
	t.Cleanup(up.Close)
	rec := capture.New(st, events.NewBus(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	srv := proxy.New(proxy.Options{Hooks: rec})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Serve(ctx, ln)
	pu, _ := url.Parse("http://" + ln.Addr().String())
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu), DisableCompression: true}}
	do := func(method, path, ct, body string) {
		req, _ := http.NewRequest(method, up.URL+path+"?q=1&api_key=abcdefghijklmnopqrst", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer realsecrettoken987654321")
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	do("GET", "/json", "", "")
	do("POST", "/bin", "application/octet-stream", "\x00\x01binary\xff")
	do("POST", "/graphql", "application/json", `{"query":"mutation Like { like(id:1) }"}`)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if n, _ := st.Count(context.Background()); n == 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	return st
}

func TestHARRoundTripAndRedaction(t *testing.T) {
	st := record(t)
	ctx := context.Background()

	var raw bytes.Buffer
	n, err := ExportHAR(ctx, st, &raw, ExportOptions{})
	if err != nil || n != 3 {
		t.Fatalf("export: %d %v", n, err)
	}
	var h HAR
	if err := json.Unmarshal(raw.Bytes(), &h); err != nil {
		t.Fatal(err)
	}
	// Required HAR 1.2 shape (what Chrome DevTools' importer reads).
	if h.Log.Version != "1.2" || h.Log.Creator.Name == "" || len(h.Log.Entries) != 3 {
		t.Fatalf("log: %+v", h.Log)
	}
	byPath := map[string]Entry{}
	for _, e := range h.Log.Entries {
		if _, err := time.Parse(time.RFC3339Nano, e.StartedDateTime); err != nil || e.Request.Cookies == nil || e.Response.Headers == nil || e.Request.QueryString == nil {
			t.Fatalf("entry shape: %+v", e)
		}
		u, _ := url.Parse(e.Request.URL)
		byPath[u.Path] = e
	}
	if c := byPath["/json"].Response.Content; c.Text != `{"access_token":"supersecretvalue123456","user":"hakan"}` || c.Encoding != "" || c.MimeType != "application/json" {
		t.Fatalf("decoded JSON body: %+v", c)
	}
	if c := byPath["/bin"].Response.Content; c.Encoding != "base64" {
		t.Fatalf("binary body must be base64: %+v", c)
	}
	if pd := byPath["/bin"].Request.PostData; pd == nil || pd.Encoding != "base64" {
		t.Fatalf("binary request body: %+v", pd)
	}
	if e := byPath["/graphql"]; e.Tags["gql_op"] != "Like" || e.Error != "graphql_error" {
		t.Fatalf("graphql annotations: %+v %s", e.Tags, e.Error)
	}

	// Redacted export hides every secret.
	var red bytes.Buffer
	if _, err := ExportHAR(ctx, st, &red, ExportOptions{Redact: redact.Default}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"realsecrettoken987654321", "supersecretvalue123456", "abcdefghijklmnopqrst", "abcdef123456"} {
		if strings.Contains(red.String(), s) {
			t.Errorf("redacted HAR leaks %q", s)
		}
	}

	// Import into a new session.
	res, err := ImportHAR(ctx, st, &raw)
	if err != nil || res.Imported != 3 || res.Skipped != 0 {
		t.Fatalf("import: %+v %v", res, err)
	}
	page, _ := st.List(ctx, store.Filter{SessionID: res.Session}, 10, "")
	if len(page.Flows) != 3 {
		t.Fatalf("imported %d flows", len(page.Flows))
	}
	for _, f := range page.Flows {
		if f.Source != store.SourceImport {
			t.Fatalf("source %s", f.Source)
		}
		if strings.HasSuffix(f.Path, "/bin") {
			b, _, _, _ := st.GetBody(ctx, f.ID, store.SideResp, 100)
			if !bytes.Equal(b, []byte{0x89, 'P', 'N', 'G', 0, 0xff, 0xfe}) {
				t.Fatalf("binary body after import: %v", b)
			}
		}
	}
	if _, err := ImportHAR(ctx, st, strings.NewReader("not json")); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestSessionSaveOpen(t *testing.T) {
	st := record(t)
	ctx := context.Background()
	var buf bytes.Buffer
	n, err := SaveSession(ctx, st, &buf, ExportOptions{})
	if err != nil || n != 3 {
		t.Fatalf("save: %d %v", n, err)
	}
	res, err := OpenSession(ctx, st, bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil || res.Imported != 3 {
		t.Fatalf("open: %+v %v", res, err)
	}
	page, _ := st.List(ctx, store.Filter{SessionID: res.Session, GraphQLOp: "Like"}, 10, "")
	if len(page.Flows) != 1 || page.Flows[0].Tags["original_source"] != "proxy" {
		t.Fatalf("gql filter on imported session: %+v", page.Flows)
	}
	b, _, _, _ := st.GetBody(ctx, page.Flows[0].ID, store.SideResp, 100)
	if !strings.Contains(string(b), "Not authorized") {
		t.Fatalf("body: %s", b)
	}
	if _, err := OpenSession(ctx, st, bytes.NewReader([]byte("zip?")), 4); err == nil {
		t.Fatal("garbage accepted")
	}
}
