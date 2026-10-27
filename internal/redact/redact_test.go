package redact

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hakanor/proxypilot/internal/store"
)

func TestMaskFormat(t *testing.T) {
	tok := "eyJhbGciOiJIUzI1NiJ9." + strings.Repeat("x", 791)
	if got := Mask("Bearer " + tok); got != "Bearer eyJh…[redacted, 812 chars]" {
		t.Fatalf("got %q", got)
	}
	if got := Mask("short"); got != "[redacted, 5 chars]" {
		t.Fatalf("short value leaked: %q", got)
	}
}

func TestHeaders(t *testing.T) {
	r := New("X-Custom-Secret-Header")
	in := []store.Header{
		{Name: "Authorization", Value: "Bearer abcdefghijklmnopqrstuvwxyz"},
		{Name: "Cookie", Value: "sid=0123456789abcdef0123; theme=dark"},
		{Name: "Set-Cookie", Value: "sid=0123456789abcdef0123; Path=/; HttpOnly"},
		{Name: "X-Api-Key", Value: "k"},
		{Name: "X-Auth-Token", Value: "tok"},
		{Name: "X-Custom-Secret-Header", Value: "v"},
		{Name: "Accept", Value: "application/json"},
	}
	out := r.Headers(in)
	want := []string{
		"Bearer abcd…[redacted, 26 chars]",
		"sid=0123…[redacted, 20 chars]; theme=[redacted, 4 chars]",
		"sid=0123…[redacted, 20 chars]; Path=/; HttpOnly",
		"[redacted, 1 chars]",
		"[redacted, 3 chars]",
		"[redacted, 1 chars]",
		"application/json",
	}
	for i, w := range want {
		if out[i].Value != w {
			t.Errorf("%s: got %q want %q", in[i].Name, out[i].Value, w)
		}
	}
	if in[0].Value == out[0].Value {
		t.Fatal("input mutated or not masked")
	}
}

func TestKeys(t *testing.T) {
	r := Default
	for _, k := range []string{"password", "Password", "passwd", "access_token", "accessToken", "refresh-token", "client_secret", "apiKey", "api_key", "APIKEY", "user_password", "token"} {
		if !r.SensitiveKey(k) {
			t.Errorf("%s should be sensitive", k)
		}
	}
	for _, k := range []string{"user", "tokenizer_version", "id", "secretary", "page"} {
		if r.SensitiveKey(k) {
			t.Errorf("%s should not be sensitive", k)
		}
	}
}

func TestQuery(t *testing.T) {
	got := Default.Query("page=2&api_key=abcdefghijklmnopqrst&token=x")
	if !strings.HasPrefix(got, "page=2&api_key=") || strings.Contains(got, "abcdefghijklmnopqrst") || strings.Contains(got, "token=x") {
		t.Fatalf("got %q", got)
	}
}

func TestJSONBody(t *testing.T) {
	in := `{"user":"hakan","password":"hunter2","nested":{"refresh_token":"r123","list":[{"client_secret":"s"}]},"count":3,"token":12345}`
	out := Default.Body([]byte(in), "application/json; charset=utf-8")
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("invalid JSON after redaction: %s", out)
	}
	s := string(out)
	for _, secret := range []string{"hunter2", "r123", `"s"`, "12345"} {
		if strings.Contains(s, secret) {
			t.Errorf("%s leaked: %s", secret, s)
		}
	}
	if v["user"] != "hakan" || v["count"] != float64(3) {
		t.Errorf("non-secret changed: %s", s)
	}
}

func TestTruncatedJSONBody(t *testing.T) {
	in := `{"user":"a","password":"hunter2","data":"abc`
	out := string(Default.Body([]byte(in), "application/json"))
	if strings.Contains(out, "hunter2") || !strings.Contains(out, `"user":"a"`) {
		t.Fatalf("got %s", out)
	}
}

func TestFormBodyAndOther(t *testing.T) {
	out := string(Default.Body([]byte("username=a&password=hunter2"), "application/x-www-form-urlencoded"))
	if strings.Contains(out, "hunter2") || !strings.Contains(out, "username=a") {
		t.Fatalf("form: %s", out)
	}
	if got := string(Default.Body([]byte("password=hunter2"), "text/plain")); got != "password=hunter2" {
		t.Fatalf("plain text changed: %s", got)
	}
}

func TestFlow(t *testing.T) {
	f := &store.Flow{URL: "https://a/x?api_key=abcdefghijklmnopqrst&p=1", Query: "api_key=abcdefghijklmnopqrst&p=1"}
	c := Default.Flow(f)
	if strings.Contains(c.URL, "abcdefghijklmnopqrst") || !strings.HasSuffix(c.URL, "&p=1") {
		t.Fatalf("url: %s", c.URL)
	}
	if f.URL == c.URL {
		t.Fatal("original mutated")
	}
}
