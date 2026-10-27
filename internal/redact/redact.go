// Package redact masks secrets before data leaves the daemon for CLI and MCP
// output. Masked values keep their type and length visible, never the value:
//
//	Bearer eyJh…[redacted, 812 chars]
package redact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"regexp"
	"strings"

	"github.com/hakanor/proxypilot/internal/store"
)

// DefaultHeaders are always masked.
var DefaultHeaders = []string{"Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie", "X-Api-Key"}

// DefaultKeys are masked in JSON bodies, form bodies and query strings.
var DefaultKeys = []string{"password", "passwd", "token", "access_token", "refresh_token", "id_token", "secret", "client_secret", "api_key", "apikey"}

// Redactor masks headers, query parameters and bodies.
type Redactor struct {
	headers map[string]bool
	keys    map[string]bool
}

// New builds a Redactor with the defaults plus extra header names (config).
func New(extraHeaders ...string) *Redactor {
	r := &Redactor{headers: map[string]bool{}, keys: map[string]bool{}}
	for _, h := range append(append([]string{}, DefaultHeaders...), extraHeaders...) {
		r.headers[strings.ToLower(strings.TrimSpace(h))] = true
	}
	for _, k := range DefaultKeys {
		r.keys[k] = true
	}
	return r
}

// Default is the Redactor without extra headers.
var Default = New()

// normKey folds "Access-Token", "accessToken" and "access_token" together.
func normKey(k string) string {
	var b strings.Builder
	for i, c := range k {
		switch {
		case c == '-' || c == '_' || c == ' ' || c == '.':
			b.WriteByte('_')
		case c >= 'A' && c <= 'Z':
			if i > 0 && b.Len() > 0 && !strings.HasSuffix(b.String(), "_") {
				b.WriteByte('_')
			}
			b.WriteRune(c + ('a' - 'A'))
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// SensitiveKey reports whether a JSON/form/query key holds a secret.
func (r *Redactor) SensitiveKey(k string) bool {
	n := normKey(k)
	if r.keys[n] || r.keys[strings.ReplaceAll(n, "_", "")] {
		return true
	}
	// x_auth_token, user_password, stripe_api_key…
	for key := range r.keys {
		if strings.HasSuffix(n, "_"+key) {
			return true
		}
	}
	return false
}

// SensitiveHeader reports whether a header's value must be masked.
func (r *Redactor) SensitiveHeader(name string) bool {
	return r.headers[strings.ToLower(name)] || r.SensitiveKey(name)
}

// Mask renders a value as "<scheme> <first 4>…[redacted, N chars]".
// Short values reveal nothing but their length.
func Mask(v string) string {
	scheme := ""
	if s, rest, ok := strings.Cut(v, " "); ok && len(s) <= 10 && !strings.ContainsAny(s, "=;,") {
		scheme, v = s+" ", rest
	}
	n := len([]rune(v))
	if n >= 16 {
		return fmt.Sprintf("%s%s…[redacted, %d chars]", scheme, string([]rune(v)[:4]), n)
	}
	return fmt.Sprintf("%s[redacted, %d chars]", scheme, n)
}

// maskCookie masks each cookie value, keeping names: "sid=[redacted, 32 chars]; theme=[redacted, 4 chars]".
func maskCookie(v string) string {
	parts := strings.Split(v, ";")
	for i, p := range parts {
		name, val, ok := strings.Cut(strings.TrimSpace(p), "=")
		if !ok {
			parts[i] = strings.TrimSpace(p)
			continue
		}
		if i > 0 && isCookieAttr(name) { // Set-Cookie attributes stay readable
			parts[i] = name + "=" + val
			continue
		}
		parts[i] = name + "=" + Mask(val)
	}
	return strings.Join(parts, "; ")
}

func isCookieAttr(name string) bool {
	switch strings.ToLower(name) {
	case "path", "domain", "expires", "max-age", "samesite":
		return true
	}
	return false
}

// HeaderValue masks one header value if its name is sensitive.
func (r *Redactor) HeaderValue(name, value string) string {
	if !r.SensitiveHeader(name) {
		return value
	}
	switch strings.ToLower(name) {
	case "cookie", "set-cookie":
		return maskCookie(value)
	}
	return Mask(value)
}

// Headers returns a masked copy.
func (r *Redactor) Headers(hs []store.Header) []store.Header {
	if hs == nil {
		return nil
	}
	out := make([]store.Header, len(hs))
	for i, h := range hs {
		out[i] = store.Header{Name: h.Name, Value: r.HeaderValue(h.Name, h.Value)}
	}
	return out
}

// Query masks values of sensitive parameters, keeping order and encoding.
func (r *Redactor) Query(raw string) string {
	if raw == "" {
		return raw
	}
	parts := strings.Split(raw, "&")
	for i, p := range parts {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			continue
		}
		name, err := url.QueryUnescape(k)
		if err != nil {
			name = k
		}
		if r.SensitiveKey(name) {
			val, err := url.QueryUnescape(v)
			if err != nil {
				val = v
			}
			parts[i] = k + "=" + Mask(val) // left unescaped so it stays readable
		}
	}
	return strings.Join(parts, "&")
}

// Flow returns a copy with headers, query and URL masked.
func (r *Redactor) Flow(f *store.Flow) *store.Flow {
	if f == nil {
		return nil
	}
	c := *f
	c.ReqHeaders = r.Headers(f.ReqHeaders)
	c.RespHeaders = r.Headers(f.RespHeaders)
	if f.Query != "" {
		c.Query = r.Query(f.Query)
		if i := strings.IndexByte(c.URL, '?'); i >= 0 {
			c.URL = c.URL[:i+1] + c.Query
		}
	}
	return &c
}

var jsonSecretRe = regexp.MustCompile(`(?i)("([^"\\]*)"\s*:\s*)"((?:[^"\\]|\\.)*)"`)

// Body masks secrets in JSON or form bodies. Other content is returned unchanged.
// Truncated JSON (not parseable) is masked with a key/value pattern instead.
func (r *Redactor) Body(b []byte, contentType string) []byte {
	mt, _, _ := mime.ParseMediaType(contentType)
	trimmed := bytes.TrimSpace(b)
	isJSON := strings.Contains(mt, "json") || (mt == "" && len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '['))
	switch {
	case isJSON:
		var v any
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		if err := dec.Decode(&v); err == nil {
			if out, err := json.Marshal(r.walk(v)); err == nil {
				return out
			}
		}
		return jsonSecretRe.ReplaceAllFunc(b, func(m []byte) []byte {
			sub := jsonSecretRe.FindSubmatch(m)
			if !r.SensitiveKey(string(sub[2])) {
				return m
			}
			return append(append([]byte{}, sub[1]...), []byte(`"`+Mask(string(sub[3]))+`"`)...)
		})
	case mt == "application/x-www-form-urlencoded":
		return []byte(r.Query(string(b)))
	}
	return b
}

func (r *Redactor) walk(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if s, ok := val.(string); ok && r.SensitiveKey(k) {
				t[k] = Mask(s)
			} else if r.SensitiveKey(k) && val != nil {
				if _, isObj := val.(map[string]any); !isObj {
					if _, isArr := val.([]any); !isArr {
						t[k] = Mask(fmt.Sprint(val))
						continue
					}
				}
				t[k] = r.walk(val)
			} else {
				t[k] = r.walk(val)
			}
		}
		return t
	case []any:
		for i := range t {
			t[i] = r.walk(t[i])
		}
		return t
	}
	return v
}

// URL masks sensitive query parameters of a full URL.
func (r *Redactor) URL(u string) string {
	base, q, ok := strings.Cut(u, "?")
	if !ok {
		return u
	}
	return base + "?" + r.Query(q)
}
