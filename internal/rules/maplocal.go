package rules

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// MapLocal answers matching requests with local content.
type MapLocal struct {
	File       string            `json:"file,omitempty"` // a file, read on every request
	Dir        string            `json:"dir,omitempty"`  // a folder: URL remainder after the pattern's '*' maps to a path
	Body       *string           `json:"body,omitempty"`
	BodyBase64 string            `json:"body_base64,omitempty"`
	Status     int               `json:"status,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	// Mode: "full" (default) never contacts the server; "body_only" sends the request
	// upstream and only replaces the response body (status and headers stay).
	Mode string `json:"mode,omitempty"`
}

const (
	ModeFull     = "full"
	ModeBodyOnly = "body_only"
)

func parseMapLocal(raw json.RawMessage) (*MapLocal, error) {
	var m MapLocal
	if len(raw) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&m); err != nil {
			return nil, invalid("map_local spec: %v", err)
		}
	}
	sources := 0
	for _, set := range []bool{m.File != "", m.Dir != "", m.Body != nil, m.BodyBase64 != ""} {
		if set {
			sources++
		}
	}
	if sources != 1 {
		return nil, invalid("map_local needs exactly one of file, dir, body, body_base64")
	}
	if m.BodyBase64 != "" {
		if _, err := base64.StdEncoding.DecodeString(m.BodyBase64); err != nil {
			return nil, invalid("body_base64: %v", err)
		}
	}
	if m.Mode == "" {
		m.Mode = ModeFull
	}
	if m.Mode != ModeFull && m.Mode != ModeBodyOnly {
		return nil, invalid("mode must be full or body_only")
	}
	if m.Status == 0 {
		m.Status = http.StatusOK
	}
	if m.Status < 100 || m.Status > 599 {
		return nil, invalid("status %d out of range", m.Status)
	}
	return &m, nil
}

// load returns the body and a guessed content type. Files are read on every call.
func (m *MapLocal) load(c *compiled, fullURL string) ([]byte, string, error) {
	switch {
	case m.Body != nil:
		return []byte(*m.Body), sniff([]byte(*m.Body), ""), nil
	case m.BodyBase64 != "":
		b, _ := base64.StdEncoding.DecodeString(m.BodyBase64)
		return b, sniff(b, ""), nil
	case m.File != "":
		b, err := os.ReadFile(expandHome(m.File))
		return b, sniff(b, m.File), err
	}
	// Folder: https://cdn.x.com/js/* + dir ./dist → ./dist/<rest of path>.
	rest := strings.TrimPrefix(fullURL, c.prefix)
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" || strings.HasSuffix(rest, "/") {
		rest += "index.html"
	}
	root, err := filepath.Abs(expandHome(m.Dir))
	if err != nil {
		return nil, "", err
	}
	p := filepath.Join(root, filepath.FromSlash(rest))
	if p != root && !strings.HasPrefix(p, root+string(filepath.Separator)) {
		return nil, "", fmt.Errorf("path %q escapes %s", rest, root)
	}
	b, err := os.ReadFile(p)
	return b, sniff(b, p), err
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

func sniff(b []byte, name string) string {
	if ext := filepath.Ext(name); ext != "" {
		if ct := mime.TypeByExtension(ext); ct != "" {
			return ct
		}
	}
	t := bytes.TrimSpace(b)
	if len(t) > 0 && (t[0] == '{' || t[0] == '[') && json.Valid(t) {
		return "application/json"
	}
	return http.DetectContentType(b)
}

// response builds the local response. A missing file becomes a 404 that says why.
func (m *MapLocal) response(c *compiled, req *http.Request, fullURL string) *http.Response {
	body, ct, err := m.load(c, fullURL)
	status := m.Status
	if err != nil {
		status, ct = http.StatusNotFound, "text/plain; charset=utf-8"
		body = []byte(fmt.Sprintf("ProxyPilot Map Local (%s): %v\n", c.Name, err))
	}
	h := http.Header{}
	h.Set("Content-Type", ct)
	for k, v := range m.Headers {
		h.Set(k, v)
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("X-ProxyPilot-Rule", c.ID)
	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        h,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

// replaceBody keeps upstream's status/headers and swaps the body (mode body_only).
func (m *MapLocal) replaceBody(c *compiled, resp *http.Response, fullURL string) *http.Response {
	body, ct, err := m.load(c, fullURL)
	if err != nil {
		return m.response(c, resp.Request, fullURL)
	}
	out := *resp
	out.Header = resp.Header.Clone()
	out.Header.Del("Content-Encoding")
	out.Header.Del("Transfer-Encoding")
	if m.Headers["Content-Type"] == "" && out.Header.Get("Content-Type") == "" {
		out.Header.Set("Content-Type", ct)
	}
	for k, v := range m.Headers {
		out.Header.Set(k, v)
	}
	out.Header.Set("Content-Length", strconv.Itoa(len(body)))
	out.Header.Set("X-ProxyPilot-Rule", c.ID)
	out.Body = io.NopCloser(bytes.NewReader(body))
	out.ContentLength = int64(len(body))
	return &out
}
