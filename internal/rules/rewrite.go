package rules

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/hakanor/proxypilot/internal/store"
)

// Rewrite step types.
const (
	StepSetHeader    = "set_header"    // name, value (replaces)
	StepAddHeader    = "add_header"    // name, value (appends)
	StepRemoveHeader = "remove_header" // name
	StepSetQuery     = "set_query"     // name, value (request)
	StepRemoveQuery  = "remove_query"  // name (request)
	StepReplaceBody  = "replace_body"  // pattern, value, regex
	StepJSONSet      = "json_set"      // path, value (JSON text; plain text becomes a string)
	StepJSONRemove   = "json_remove"   // path
	StepSetStatus    = "set_status"    // status (response)
	StepSetURL       = "set_url"       // value (request, same scheme allowed to change)
	StepSetPath      = "set_path"      // value (request)
)

type Step struct {
	Type    string `json:"type"`
	Name    string `json:"name,omitempty"`
	Value   string `json:"value,omitempty"`
	Pattern string `json:"pattern,omitempty"`
	Regex   bool   `json:"regex,omitempty"`
	Path    string `json:"path,omitempty"`
	Status  int    `json:"status,omitempty"`

	re   *regexp.Regexp
	segs []pathSeg
	val  any
}

// RewriteSpec is an ordered list of steps.
type RewriteSpec struct {
	Steps []Step `json:"steps"`
}

func parseRewrite(raw json.RawMessage, phase Phase) (*RewriteSpec, error) {
	var r RewriteSpec
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return nil, invalid("rewrite spec: %v", err)
	}
	if len(r.Steps) == 0 {
		return nil, invalid("rewrite needs at least one step")
	}
	for i := range r.Steps {
		s := &r.Steps[i]
		bad := func(msg string) error { return invalid("step %d (%s): %s", i+1, s.Type, msg) }
		switch s.Type {
		case StepSetHeader, StepAddHeader, StepSetQuery:
			if s.Name == "" {
				return nil, bad("name is required")
			}
		case StepRemoveHeader, StepRemoveQuery:
			if s.Name == "" {
				return nil, bad("name is required")
			}
		case StepReplaceBody:
			if s.Pattern == "" {
				return nil, bad("pattern is required")
			}
			if s.Regex {
				re, err := regexp.Compile(s.Pattern)
				if err != nil {
					return nil, bad(err.Error())
				}
				s.re = re
			}
		case StepJSONSet, StepJSONRemove:
			segs, err := parsePath(s.Path)
			if err != nil {
				return nil, bad(err.Error())
			}
			s.segs = segs
			if s.Type == StepJSONSet {
				if err := json.Unmarshal([]byte(s.Value), &s.val); err != nil {
					s.val = s.Value // not JSON: a string
				}
			}
		case StepSetStatus:
			if s.Status < 100 || s.Status > 599 {
				return nil, bad("status must be 100..599")
			}
			if phase == PhaseRequest {
				return nil, bad("status can only change in the response phase")
			}
		case StepSetURL, StepSetPath:
			if s.Value == "" {
				return nil, bad("value is required")
			}
			if phase == PhaseResponse {
				return nil, bad("the URL can only change in the request phase")
			}
			if s.Type == StepSetURL {
				if u, err := url.Parse(s.Value); err != nil || !u.IsAbs() {
					return nil, bad("value must be an absolute URL")
				}
			}
		default:
			return nil, bad("unknown step type")
		}
		if (s.Type == StepSetQuery || s.Type == StepRemoveQuery) && phase == PhaseResponse {
			return nil, bad("query parameters can only change in the request phase")
		}
	}
	return &r, nil
}

func (r *RewriteSpec) touchesBody() bool {
	for _, s := range r.Steps {
		if s.Type == StepReplaceBody || s.Type == StepJSONSet || s.Type == StepJSONRemove {
			return true
		}
	}
	return false
}

// message is the part of a request or response the steps edit.
type message struct {
	header http.Header
	body   []byte
	status int
	url    *url.URL // request only
}

func (r *RewriteSpec) apply(m *message, request bool) error {
	for _, s := range r.Steps {
		switch s.Type {
		case StepSetHeader:
			m.header.Set(s.Name, s.Value)
		case StepAddHeader:
			m.header.Add(s.Name, s.Value)
		case StepRemoveHeader:
			m.header.Del(s.Name)
		case StepSetQuery, StepRemoveQuery:
			if !request {
				continue
			}
			q := m.url.Query()
			if s.Type == StepSetQuery {
				q.Set(s.Name, s.Value)
			} else {
				q.Del(s.Name)
			}
			m.url.RawQuery = q.Encode()
		case StepReplaceBody:
			if s.re != nil {
				m.body = s.re.ReplaceAll(m.body, []byte(s.Value))
			} else {
				m.body = bytes.ReplaceAll(m.body, []byte(s.Pattern), []byte(s.Value))
			}
		case StepJSONSet, StepJSONRemove:
			var doc any
			if len(bytes.TrimSpace(m.body)) > 0 {
				dec := json.NewDecoder(bytes.NewReader(m.body))
				dec.UseNumber()
				if err := dec.Decode(&doc); err != nil {
					return fmt.Errorf("%s %s: body is not JSON", s.Type, s.Path)
				}
			}
			if s.Type == StepJSONSet {
				var err error
				if doc, err = jsonSet(doc, s.segs, s.val); err != nil {
					return fmt.Errorf("json_set %s: %v", s.Path, err)
				}
			} else {
				doc = jsonRemove(doc, s.segs)
			}
			b, err := json.Marshal(doc)
			if err != nil {
				return err
			}
			m.body = b
		case StepSetStatus:
			if !request {
				m.status = s.Status
			}
		case StepSetURL:
			if request {
				u, _ := url.Parse(s.Value)
				*m.url = *u
			}
		case StepSetPath:
			if request {
				m.url.Path, m.url.RawPath = s.Value, ""
			}
		}
	}
	return nil
}

const maxRewriteBody = 16 << 20

// readDecoded reads a body and undoes Content-Encoding; ok=false if it is too big.
func readDecoded(rc io.ReadCloser, h http.Header) ([]byte, bool, error) {
	if rc == nil || rc == http.NoBody {
		return nil, true, nil
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, maxRewriteBody+1))
	if err != nil {
		return nil, false, err
	}
	if len(b) > maxRewriteBody {
		return nil, false, nil
	}
	if enc := h.Get("Content-Encoding"); enc != "" && enc != "identity" {
		dec, err := store.Decode(b, enc)
		if err != nil {
			return nil, false, fmt.Errorf("decode %s body: %w", enc, err)
		}
		b = dec
	}
	return b, true, nil
}

// setBody installs an edited (plain) body and fixes the framing headers.
func setBody(h http.Header, b []byte) (io.ReadCloser, int64) {
	h.Del("Content-Encoding")
	h.Del("Transfer-Encoding")
	h.Set("Content-Length", strconv.Itoa(len(b)))
	return io.NopCloser(bytes.NewReader(b)), int64(len(b))
}

// rewriteRequest edits req in place (the proxy sends this object).
func (c *compiled) rewriteRequest(req *http.Request) error {
	m := &message{header: req.Header, url: req.URL}
	if c.rewrite.touchesBody() {
		b, ok, err := readDecoded(req.Body, req.Header)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("request body over %d MB; rewrite skipped", maxRewriteBody>>20)
		}
		m.body = b
	}
	if err := c.rewrite.apply(m, true); err != nil {
		return err
	}
	if c.rewrite.touchesBody() {
		req.Body, req.ContentLength = setBody(req.Header, m.body)
	}
	req.Host = req.URL.Host
	return nil
}

// rewriteResponse returns an edited copy of resp.
func (c *compiled) rewriteResponse(resp *http.Response) (*http.Response, error) {
	out := *resp
	out.Header = resp.Header.Clone()
	m := &message{header: out.Header, status: resp.StatusCode}
	if c.rewrite.touchesBody() {
		b, ok, err := readDecoded(resp.Body, resp.Header)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("response body over %d MB; rewrite skipped", maxRewriteBody>>20)
		}
		m.body = b
	}
	if err := c.rewrite.apply(m, false); err != nil {
		return nil, err
	}
	if c.rewrite.touchesBody() {
		out.Body, out.ContentLength = setBody(out.Header, m.body)
	}
	if m.status != resp.StatusCode {
		out.StatusCode, out.Status = m.status, fmt.Sprintf("%d %s", m.status, http.StatusText(m.status))
	}
	return &out, nil
}

// MapRemoteSpec sends matching requests elsewhere.
type MapRemoteSpec struct {
	// To is the target. Scheme, host and port always come from it; its path replaces the
	// original unless empty or "/"; a trailing * keeps the part of the original URL that
	// the rule's * matched (https://staging.x.com/v2/* ). Its query replaces the original if set.
	To                 string `json:"to"`
	PreserveHostHeader bool   `json:"preserve_host_header,omitempty"`
	to                 *url.URL
	wild               bool
}

func parseMapRemote(raw json.RawMessage) (*MapRemoteSpec, error) {
	var m MapRemoteSpec
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, invalid("map_remote spec: %v", err)
	}
	to := m.To
	if strings.HasSuffix(to, "*") {
		m.wild, to = true, strings.TrimSuffix(to, "*")
	}
	u, err := url.Parse(to)
	if err != nil || !u.IsAbs() || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, invalid("map_remote to must be an http(s) URL like https://staging.example.com")
	}
	m.to = u
	return &m, nil
}

// target computes the new URL for req (original in full form).
func (c *compiled) mapRemote(req *http.Request, original string) *url.URL {
	m := c.mremote
	u := *req.URL
	u.Scheme, u.Host = m.to.Scheme, m.to.Host
	switch {
	case m.wild:
		rest := strings.TrimPrefix(original, c.prefix)
		if i := strings.IndexByte(rest, '?'); i >= 0 {
			rest = rest[:i]
		}
		u.Path, u.RawPath = m.to.Path+rest, ""
	case m.to.Path != "" && m.to.Path != "/":
		u.Path, u.RawPath = m.to.Path, ""
	}
	if m.to.RawQuery != "" {
		u.RawQuery = m.to.RawQuery
	}
	return &u
}
