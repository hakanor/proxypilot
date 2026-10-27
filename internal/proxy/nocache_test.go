package proxy

import (
	"net/http"
	"testing"
)

func TestNoCache(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://example.com/", nil)
	req.Header.Set("If-None-Match", `"abc"`)
	req.Header.Set("If-Modified-Since", "Mon, 01 Jan 2024 00:00:00 GMT")
	resp := &http.Response{Header: http.Header{"Etag": {`"abc"`}, "Cache-Control": {"max-age=3600"}, "Last-Modified": {"x"}}}
	f := &Flow{Request: req, Response: resp}

	var n NoCache
	n.OnRequest(f)
	n.OnResponse(f)
	if req.Header.Get("If-None-Match") == "" || resp.Header.Get("Cache-Control") != "max-age=3600" {
		t.Fatal("changed headers while off")
	}

	n.SetEnabled(true)
	n.OnRequest(f)
	n.OnResponse(f)
	if req.Header.Get("If-None-Match") != "" || req.Header.Get("If-Modified-Since") != "" {
		t.Errorf("conditional headers kept: %v", req.Header)
	}
	if req.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("request Cache-Control = %q", req.Header.Get("Cache-Control"))
	}
	if resp.Header.Get("ETag") != "" || resp.Header.Get("Last-Modified") != "" {
		t.Errorf("validators kept: %v", resp.Header)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Errorf("response Cache-Control = %q", got)
	}
}
