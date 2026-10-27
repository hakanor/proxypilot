package proxy

import (
	"bytes"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestHostMatches(t *testing.T) {
	cases := []struct {
		host string
		pats []string
		want bool
	}{
		{"api.example.com", []string{"api.example.com"}, true},
		{"api.example.com", []string{"*.example.com"}, true},
		{"example.com", []string{"*.example.com"}, false},
		{"other.com", []string{"*.example.com", "api.example.com"}, false},
		{"img1.cdn.net", []string{"img?.cdn.net"}, true},
	}
	for _, c := range cases {
		if got := hostMatches(c.host, c.pats); got != c.want {
			t.Errorf("hostMatches(%q, %v) = %v", c.host, c.pats, got)
		}
	}
}

func TestPacedBody(t *testing.T) {
	// 80 kbit/s = 10 KB/s: 5 KB should take about half a second.
	data := bytes.Repeat([]byte("x"), 5000)
	start := time.Now()
	n, err := io.Copy(io.Discard, newPacedBody(io.NopCloser(bytes.NewReader(data)), 80))
	el := time.Since(start)
	if err != nil || n != 5000 {
		t.Fatalf("copy: %d %v", n, err)
	}
	if el < 400*time.Millisecond || el > 900*time.Millisecond {
		t.Fatalf("5 KB at 10 KB/s took %v, want ~500ms", el)
	}
}

func TestThrottleScope(t *testing.T) {
	var th Throttle
	th.Set(ThrottleSettings{Enabled: true, ThrottleProfile: ThrottleProfile{Name: "custom", LatencyMs: 120}, Hosts: []string{"slow.test"}})
	run := func(url string) time.Duration {
		req, _ := http.NewRequest("GET", url, nil)
		f := newFlow("127.0.0.1:1", "http")
		f.Request = req
		start := time.Now()
		th.OnRequest(f)
		return time.Since(start)
	}
	if d := run("http://slow.test/"); d < 100*time.Millisecond {
		t.Errorf("matching host delayed only %v", d)
	}
	if d := run("http://fast.test/"); d > 20*time.Millisecond {
		t.Errorf("other host delayed %v", d)
	}
	th.Set(ThrottleSettings{})
	if d := run("http://slow.test/"); d > 20*time.Millisecond {
		t.Errorf("disabled throttle delayed %v", d)
	}
}
