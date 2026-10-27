package capture

import (
	"compress/gzip"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/hakanor/proxypilot/internal/events"
	"github.com/hakanor/proxypilot/internal/proxy"
	"github.com/hakanor/proxypilot/internal/store"
)

func TestRecorderStoresThenPublishes(t *testing.T) {
	st, err := store.Open(t.TempDir(), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	bus := events.NewBus()
	sub := bus.Subscribe(nil, 100)
	defer sub.Cancel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		zw.Write([]byte(`{"ok":true}`))
		zw.Close()
	}))
	defer upstream.Close()

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	srv := proxy.New(proxy.Options{Hooks: New(st, bus, nil)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx, ln)

	pu, _ := url.Parse("http://" + ln.Addr().String())
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu), DisableCompression: true}}
	req, _ := http.NewRequest("GET", upstream.URL+"/api?x=1", nil)
	req.Header.Set("User-Agent", "InvioApp/4.2 (iPhone; iOS 18.1)")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	var started, completed events.Event
	timeout := time.After(5 * time.Second)
	for completed.Type == "" {
		select {
		case e := <-sub.C:
			switch e.Type {
			case events.FlowStarted:
				started = e
			case events.FlowCompleted:
				completed = e
			}
		case <-timeout:
			t.Fatal("no flow.completed event")
		}
	}
	if started.Type == "" || started.Seq >= completed.Seq {
		t.Fatal("flow.started missing or out of order")
	}
	id := completed.Data.(*store.Flow).ID

	// The event is published after the write, so the row must already be final.
	f, err := st.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if f.State != store.StateCompleted || f.Status != 200 || f.Query != "x=1" || f.ClientLabel != "iOS Simulator" || f.Tags["app"] != "InvioApp" {
		t.Fatalf("stored flow: %+v", f)
	}
	if f.BytesDown == 0 || f.RespSize != int64(len(`{"ok":true}`)) {
		t.Fatalf("sizes: wire=%d decoded=%d", f.BytesDown, f.RespSize)
	}
	b, _, info, err := st.GetBody(context.Background(), id, store.SideResp, 0)
	if err != nil || string(b) != `{"ok":true}` || info.Encoding != "gzip" {
		t.Fatalf("body=%q info=%+v err=%v", b, info, err)
	}
}

func TestClientLabel(t *testing.T) {
	cases := map[string]string{
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X)":            "iPhone",
		"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit Chrome/129.0": "Android",
		"curl/8.7.1": "curl",
		"Mozilla/5.0 (Macintosh) Chrome/129.0 Safari/537.36": "Chrome",
		"okhttp/4.12.0": "Android app",
		"MyTool/1.0":    "MyTool",
	}
	for ua, want := range cases {
		if got := ClientLabel(ua); got != want {
			t.Errorf("%q → %q, want %q", ua, got, want)
		}
	}
}

func TestRecordingPause(t *testing.T) {
	st, err := store.Open(t.TempDir(), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	bus := events.NewBus()
	rec := New(st, bus, nil)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) }))
	defer upstream.Close()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	srv := proxy.New(proxy.Options{Hooks: rec})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx, ln)
	pu, _ := url.Parse("http://" + ln.Addr().String())
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
	get := func() {
		resp, err := c.Get(upstream.URL)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	count := func() int { n, _ := st.Count(context.Background()); return n }

	rec.SetRecording(false)
	if rec.Recording() {
		t.Fatal("still recording")
	}
	get()
	time.Sleep(100 * time.Millisecond)
	if n := count(); n != 0 {
		t.Fatalf("paused recorder stored %d flows", n)
	}
	rec.SetRecording(true)
	get()
	deadline := time.Now().Add(2 * time.Second)
	for count() != 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := count(); n != 1 {
		t.Fatalf("resumed recorder stored %d flows, want 1", n)
	}
}
