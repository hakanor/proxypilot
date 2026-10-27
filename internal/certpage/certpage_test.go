package certpage_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hakanor/proxypilot/internal/ca"
	"github.com/hakanor/proxypilot/internal/capture"
	"github.com/hakanor/proxypilot/internal/certpage"
	"github.com/hakanor/proxypilot/internal/clients"
	"github.com/hakanor/proxypilot/internal/events"
	"github.com/hakanor/proxypilot/internal/proxy"
)

func TestCertPageThroughProxyAndDirect(t *testing.T) {
	root, err := ca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	issuer, _ := ca.NewIssuer(root, 0)
	bus := events.NewBus()
	sub := bus.Subscribe(nil, 32)
	defer sub.Cancel()
	tracker := clients.New(t.TempDir(), bus, capture.ClientLabel)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	srv := proxy.New(proxy.Options{Hooks: tracker, Issuer: issuer, DirectHandler: certpage.New(root, tracker, ln.Addr().String)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx, ln)
	pu, _ := url.Parse("http://" + ln.Addr().String())
	viaProxy := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}, Timeout: 5 * time.Second}

	get := func(c *http.Client, u, ua string) (*http.Response, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("User-Agent", ua)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, string(b)
	}
	const iphone = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148 Safari/604.1"

	// Magic host through the proxy: page detects iOS and links the profile.
	resp, page := get(viaProxy, "http://proxypilot.cert/", iphone)
	if resp.StatusCode != 200 || !strings.Contains(page, "/ca.mobileconfig") || !strings.Contains(page, `"ios"`) {
		t.Fatalf("page: %d %.300s", resp.StatusCode, page)
	}
	resp, body := get(viaProxy, "http://proxypilot.cert/ca.mobileconfig", iphone)
	if resp.Header.Get("Content-Type") != "application/x-apple-aspen-config" || !strings.Contains(body, "com.apple.security.root") {
		t.Fatalf("mobileconfig: %v %.200s", resp.Header, body)
	}
	resp, body = get(viaProxy, "http://proxypilot.cert/ca.crt", "Android")
	if resp.Header.Get("Content-Type") != "application/x-x509-ca-cert" || len(body) != len(root.ExportDER()) {
		t.Fatalf("crt: %v %d bytes", resp.Header, len(body))
	}

	// Direct request (phone opened http://<ip>:9090/ before setting the proxy).
	direct := &http.Client{Timeout: 5 * time.Second}
	_, st := get(direct, "http://"+ln.Addr().String()+"/status", iphone)
	var s struct {
		Proxied bool
		TLS     string
	}
	json.Unmarshal([]byte(st), &s)
	if s.Proxied {
		t.Fatalf("direct status should not count as proxied: %s", st)
	}

	// Proxied traffic marks the device as reached and announces it once.
	get(viaProxy, "http://example.invalid.test/", iphone)
	c, ok := tracker.Get("127.0.0.1:1")
	if !ok || c.Requests == 0 || c.Guess != "iPhone" {
		t.Fatalf("tracker: %+v %v", c, ok)
	}
	connected := 0
	for len(sub.C) > 0 {
		if (<-sub.C).Type == events.DeviceConnected {
			connected++
		}
	}
	if connected != 1 {
		t.Fatalf("device.connected events: %d", connected)
	}
	_, st = get(direct, "http://"+ln.Addr().String()+"/status", iphone)
	json.Unmarshal([]byte(st), &s)
	if !s.Proxied {
		t.Fatalf("status after proxied traffic: %s", st)
	}

	if got, _ := tracker.Rename("127.0.0.1", "Test iPhone"); got.Name != "Test iPhone" || !got.Custom || tracker.Name("127.0.0.1:5") != "Test iPhone" {
		t.Fatalf("rename: %+v", got)
	}
}

func TestPlatform(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X)":   "ios",
		"Mozilla/5.0 (Linux; Android 14; Pixel 8) Chrome": "android",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)":    "other",
	} {
		if got := certpage.Platform(ua); got != want {
			t.Errorf("%s: %s", ua, got)
		}
	}
}
