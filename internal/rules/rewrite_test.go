package rules

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func steps(s ...Step) json.RawMessage { return spec(RewriteSpec{Steps: s}) }

// Every step type on a request and a response, without the proxy.
func TestRewriteSteps(t *testing.T) {
	cases := []struct {
		name    string
		step    Step
		reqBody string
		check   func(m *message) string
		resp    bool
	}{
		{"set_header", Step{Type: StepSetHeader, Name: "X-Debug", Value: "1"}, "", func(m *message) string { return m.header.Get("X-Debug") }, false},
		{"add_header", Step{Type: StepAddHeader, Name: "Accept", Value: "b"}, "", func(m *message) string { return strings.Join(m.header.Values("Accept"), ",") }, false},
		{"remove_header", Step{Type: StepRemoveHeader, Name: "Cookie"}, "", func(m *message) string { return m.header.Get("Cookie") + "<none>" }, false},
		{"set_query", Step{Type: StepSetQuery, Name: "page", Value: "9"}, "", func(m *message) string { return m.url.Query().Get("page") }, false},
		{"remove_query", Step{Type: StepRemoveQuery, Name: "debug"}, "", func(m *message) string { return m.url.RawQuery }, false},
		{"replace_body text", Step{Type: StepReplaceBody, Pattern: "cat", Value: "dog"}, "cat and cat", func(m *message) string { return string(m.body) }, false},
		{"replace_body regex", Step{Type: StepReplaceBody, Pattern: `\d+`, Value: "N", Regex: true}, "a1b22", func(m *message) string { return string(m.body) }, false},
		{"json_set nested", Step{Type: StepJSONSet, Path: "$.user.isPremium", Value: "true"}, `{"user":{"id":1}}`, func(m *message) string { return string(m.body) }, false},
		{"json_set string", Step{Type: StepJSONSet, Path: `$["display name"]`, Value: "Hakan"}, `{}`, func(m *message) string { return string(m.body) }, false},
		{"json_set array", Step{Type: StepJSONSet, Path: "$.items[-1].n", Value: "5"}, `{"items":[{"n":1},{"n":2}]}`, func(m *message) string { return string(m.body) }, false},
		{"json_remove", Step{Type: StepJSONRemove, Path: "$.items[0]"}, `{"items":[1,2,3]}`, func(m *message) string { return string(m.body) }, false},
		{"set_status", Step{Type: StepSetStatus, Status: 503}, "", func(m *message) string { return http.StatusText(m.status) }, true},
		{"set_url", Step{Type: StepSetURL, Value: "https://staging.x.com/v3/feed?x=1"}, "", func(m *message) string { return m.url.String() }, false},
		{"set_path", Step{Type: StepSetPath, Value: "/v3/feed"}, "", func(m *message) string { return m.url.String() }, false},
	}
	want := map[string]string{
		"set_header": "1", "add_header": "a,b", "remove_header": "<none>", "set_query": "9", "remove_query": "page=2",
		"replace_body text": "dog and dog", "replace_body regex": "aNbN",
		"json_set nested": `{"user":{"id":1,"isPremium":true}}`, "json_set string": `{"display name":"Hakan"}`,
		"json_set array": `{"items":[{"n":1},{"n":5}]}`, "json_remove": `{"items":[2,3]}`,
		"set_status": "Service Unavailable", "set_url": "https://staging.x.com/v3/feed?x=1", "set_path": "https://api.x.com/v3/feed?page=2&debug=1",
	}
	for _, c := range cases {
		phase := PhaseRequest
		if c.resp {
			phase = PhaseResponse
		}
		rw, err := parseRewrite(steps(c.step), phase)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		u, _ := url.Parse("https://api.x.com/v2/feed?page=2&debug=1")
		m := &message{header: http.Header{"Accept": {"a"}, "Cookie": {"s=1"}}, body: []byte(c.reqBody), status: 200, url: u}
		if err := rw.apply(m, !c.resp); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := c.check(m); got != want[c.name] {
			t.Errorf("%s: got %q, want %q", c.name, got, want[c.name])
		}
	}
}

func TestRewriteValidation(t *testing.T) {
	bad := []struct {
		phase Phase
		step  Step
	}{
		{PhaseRequest, Step{Type: "explode"}},
		{PhaseRequest, Step{Type: StepSetHeader}},
		{PhaseRequest, Step{Type: StepReplaceBody, Pattern: "(", Regex: true}},
		{PhaseRequest, Step{Type: StepJSONSet, Path: "user.x"}},
		{PhaseRequest, Step{Type: StepSetStatus, Status: 500}},
		{PhaseResponse, Step{Type: StepSetQuery, Name: "a"}},
		{PhaseResponse, Step{Type: StepSetPath, Value: "/x"}},
		{PhaseRequest, Step{Type: StepSetURL, Value: "/relative"}},
	}
	for i, b := range bad {
		if _, err := parseRewrite(steps(b.step), b.phase); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d: want ErrInvalid, got %v", i, err)
		}
	}
	if _, err := parseRewrite(spec(RewriteSpec{}), PhaseRequest); !errors.Is(err, ErrInvalid) {
		t.Error("empty steps accepted")
	}
	for _, s := range []string{`{"to":"ftp://x"}`, `{"to":"/path"}`, `{}`} {
		if _, err := parseMapRemote(json.RawMessage(s)); !errors.Is(err, ErrInvalid) {
			t.Errorf("map_remote %s accepted", s)
		}
	}
}

func gz(s string) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write([]byte(s))
	w.Close()
	return b.Bytes()
}

// Through the proxy: gzip JSON response edited, request header rewritten, map remote to "staging".
func TestRewriteAndMapRemoteThroughProxy(t *testing.T) {
	prod := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(gz(`{"user":{"id":7,"isPremium":false},"seen":"` + r.Header.Get("X-Debug") + `"}`))
	}))
	defer prod.Close()
	staging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "staging:"+r.URL.Path+"?"+r.URL.RawQuery+" host="+r.Host)
	}))
	defer staging.Close()

	e, _ := New("", nil, nil)
	c := &http.Client{Transport: &http.Transport{Proxy: proxyWith(t, e).Transport.(*http.Transport).Proxy, DisableCompression: true}}
	add := func(r Rule) {
		t.Helper()
		r.Enabled = true
		if _, err := e.Add(r, 0); err != nil {
			t.Fatal(err)
		}
	}
	add(Rule{Type: TypeRewrite, Phase: PhaseRequest, Match: Matcher{URL: prod.URL + "/me"}, Spec: steps(Step{Type: StepSetHeader, Name: "X-Debug", Value: "on"})})
	add(Rule{Type: TypeRewrite, Phase: PhaseResponse, Match: Matcher{URL: prod.URL + "/me"}, Spec: steps(Step{Type: StepJSONSet, Path: "$.user.isPremium", Value: "true"}, Step{Type: StepSetHeader, Name: "X-Rewritten", Value: "1"})})
	add(Rule{Type: TypeMapRemote, Match: Matcher{URL: prod.URL + "/api/*"}, Spec: spec(map[string]any{"to": staging.URL + "/v2/*"})})
	add(Rule{Type: TypeMapRemote, Match: Matcher{URL: prod.URL + "/keep*"}, Spec: spec(map[string]any{"to": staging.URL, "preserve_host_header": true})})

	resp, err := c.Get(prod.URL + "/me")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != `{"seen":"on","user":{"id":7,"isPremium":true}}` || resp.Header.Get("Content-Encoding") != "" || resp.Header.Get("X-Rewritten") != "1" {
		t.Fatalf("rewrite: %s %v", b, resp.Header)
	}

	resp, err = c.Get(prod.URL + "/api/users/3?x=1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	su, _ := url.Parse(staging.URL)
	if string(b) != "staging:/v2/users/3?x=1 host="+su.Host {
		t.Fatalf("map remote wildcard: %s", b)
	}
	resp, _ = c.Get(prod.URL + "/keep?y=2")
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	pu, _ := url.Parse(prod.URL)
	if string(b) != "staging:/keep?y=2 host="+pu.Host {
		t.Fatalf("map remote preserve host: %s", b)
	}
}
