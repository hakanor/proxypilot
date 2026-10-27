package cdp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hakanor/proxypilot/internal/events"
	"github.com/hakanor/proxypilot/internal/rules"
	"github.com/hakanor/proxypilot/internal/store"
)

// TestChromeEndToEnd drives a real headless Chrome. It only runs with
// PROXYPILOT_CHROME_TEST=1: launching Chrome can trigger macOS keychain or
// firewall prompts, which a plain `go test ./...` must never do.
func TestChromeEndToEnd(t *testing.T) {
	if os.Getenv("PROXYPILOT_CHROME_TEST") != "1" {
		t.Skip("set PROXYPILOT_CHROME_TEST=1 to run against a real Chrome")
	}
	if _, err := FindChrome(""); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	st, err := store.Open(dir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	bus := events.NewBus()
	eng, _ := rules.New("", bus, nil)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<title>ProxyPilot test page</title><script>fetch('/api/data').then(r=>r.text()).then(t=>document.title='done:'+t)</script>`)
		default:
			io.WriteString(w, "from server")
		}
	}))
	defer up.Close()

	// The same Map Local rule the proxy would use.
	spec, _ := json.Marshal(map[string]any{"body": "mocked by rule"})
	if _, err := eng.Add(rules.Rule{Type: rules.TypeMapLocal, Enabled: true, Match: rules.Matcher{URL: up.URL + "/api/*"}, Spec: spec}, 0); err != nil {
		t.Fatal(err)
	}

	m := &Manager{Store: st, Bus: bus, Rules: eng, DataDir: dir, Headless: true, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := m.Launch(ctx, up.URL+"/"); err != nil {
		t.Fatal(err)
	}
	defer m.Detach()

	// Wait until the page's fetch completed and was recorded.
	var api *store.Flow
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && api == nil {
		page, _ := st.List(ctx, store.Filter{}, 50, "")
		for _, f := range page.Flows {
			if strings.HasSuffix(f.URL, "/api/data") && f.State == store.StateCompleted {
				api = f
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if api == nil {
		t.Fatal("Chrome request was not recorded")
	}
	if api.Source != store.SourceCDP || api.Tags["tab_title"] == "" || len(api.MatchedRules) == 0 {
		t.Fatalf("flow: source=%s tags=%v rules=%v", api.Source, api.Tags, api.MatchedRules)
	}
	body, _, _, err := st.GetBody(ctx, api.ID, store.SideResp, 1000)
	if err != nil || string(body) != "mocked by rule" {
		t.Fatalf("Map Local not applied in Chrome: %q %v", body, err)
	}

	s := m.Status()
	if !s.Connected || !s.Launched || len(s.Tabs) == 0 {
		t.Fatalf("status: %+v", s)
	}

	// Closing Chrome ends the connection cleanly.
	m.mu.Lock()
	conn := m.conn
	m.mu.Unlock()
	_ = conn.Call(ctx, "", "Browser.close", nil, nil)
	select {
	case <-conn.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("connection did not close with Chrome")
	}
	time.Sleep(100 * time.Millisecond)
	if m.Status().Connected {
		t.Fatal("still marked connected after Chrome closed")
	}
}
