// Package diff compares two recorded flows: request line, headers, query
// parameters and bodies (JSON by path, text by line).
package diff

import (
	"bytes"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hakanor/proxypilot/internal/store"
)

// Change is one difference. Kind is added (only in B), removed (only in A) or changed.
type Change struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	A    string `json:"a,omitempty"`
	B    string `json:"b,omitempty"`
}

// Line is one line of a text diff. Op is " " (same), "-" (only in A), "+"
// (only in B) or "…" (unchanged lines left out).
type Line struct {
	Op   string `json:"op"`
	Text string `json:"text,omitempty"`
}

// Body compares two bodies. Mode is same, json, text, binary or empty.
type Body struct {
	Mode      string   `json:"mode"`
	Changes   []Change `json:"changes,omitempty"` // json: by path; binary: size
	Lines     []Line   `json:"lines,omitempty"`   // text, with context around changes
	Truncated bool     `json:"truncated,omitempty"`
}

// Side is one half of a flow (request or response).
type Side struct {
	Line    []Change `json:"line"` // method, host, path, query.<name>; or status
	Headers []Change `json:"headers"`
	Body    Body     `json:"body"`
}

type Ref struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	URL    string `json:"url"`
	Status int    `json:"status,omitempty"`
}

type Result struct {
	A        Ref  `json:"a"`
	B        Ref  `json:"b"`
	Same     bool `json:"same"`
	Request  Side `json:"request"`
	Response Side `json:"response"`
}

// Limits keep a diff of two big bodies bounded.
const (
	maxChanges = 500
	maxLines   = 500
	maxCells   = 4_000_000 // line-diff table size; beyond it the middle is shown as removed + added
	context    = 3
)

// Bodies holds the four decoded bodies and whether each was cut short when read.
type Bodies struct {
	ReqA, ReqB, RespA, RespB []byte
	Truncated                bool
}

// Flows compares a and b. Redact both flows and bodies before calling this.
func Flows(a, b *store.Flow, bodies Bodies) Result {
	r := Result{
		A: Ref{ID: a.ID, Method: a.Method, URL: a.URL, Status: a.Status},
		B: Ref{ID: b.ID, Method: b.Method, URL: b.URL, Status: b.Status},
	}
	r.Request.Line = append(field("method", a.Method, b.Method), URL(a.URL, b.URL)...)
	r.Request.Headers = Headers(a.ReqHeaders, b.ReqHeaders)
	r.Request.Body = Compare(bodies.ReqA, bodies.ReqB)
	r.Response.Line = field("status", itoa(a.Status), itoa(b.Status))
	r.Response.Headers = Headers(a.RespHeaders, b.RespHeaders)
	r.Response.Body = Compare(bodies.RespA, bodies.RespB)
	if bodies.Truncated {
		r.Request.Body.Truncated, r.Response.Body.Truncated = true, true
	}
	r.Same = len(r.Request.Line)+len(r.Request.Headers)+len(r.Response.Line)+len(r.Response.Headers) == 0 &&
		r.Request.Body.equal() && r.Response.Body.equal()
	return r
}

func (b Body) equal() bool { return b.Mode == "same" || b.Mode == "empty" }

func itoa(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func field(path, a, b string) []Change {
	switch {
	case a == b:
		return nil
	case a == "":
		return []Change{{Path: path, Kind: "added", B: b}}
	case b == "":
		return []Change{{Path: path, Kind: "removed", A: a}}
	}
	return []Change{{Path: path, Kind: "changed", A: a, B: b}}
}

// URL compares scheme+host, path and each query parameter on its own.
func URL(a, b string) []Change {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	if errA != nil || errB != nil {
		return field("url", a, b)
	}
	out := field("host", ua.Scheme+"://"+ua.Host, ub.Scheme+"://"+ub.Host)
	out = append(out, field("path", ua.EscapedPath(), ub.EscapedPath())...)
	return append(out, maps("query.", join(ua.Query()), join(ub.Query()))...)
}

func join(v map[string][]string) map[string]string {
	m := make(map[string]string, len(v))
	for k, vs := range v {
		m[k] = strings.Join(vs, ", ")
	}
	return m
}

// Headers compares by name (case-insensitive); repeated headers are joined.
func Headers(a, b []store.Header) []Change {
	return maps("", headerMap(a), headerMap(b))
}

func headerMap(hs []store.Header) map[string]string {
	m := map[string]string{}
	for _, h := range hs {
		k := strings.ToLower(h.Name)
		if old, ok := m[k]; ok {
			m[k] = old + ", " + h.Value
		} else {
			m[k] = h.Value
		}
	}
	return m
}

// maps compares two key → value maps, sorted by key.
func maps(prefix string, a, b map[string]string) []Change {
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var out []Change
	for _, k := range keys {
		va, inA := a[k]
		vb, inB := b[k]
		switch {
		case !inB:
			out = append(out, Change{Path: prefix + k, Kind: "removed", A: va})
		case !inA:
			out = append(out, Change{Path: prefix + k, Kind: "added", B: vb})
		case va != vb:
			out = append(out, Change{Path: prefix + k, Kind: "changed", A: va, B: vb})
		}
	}
	return out
}

// Compare diffs two bodies: JSON by path when both parse, text by line, else by size.
func Compare(a, b []byte) Body {
	switch {
	case bytes.Equal(a, b):
		if len(a) == 0 {
			return Body{Mode: "empty"}
		}
		return Body{Mode: "same"}
	case !utf8.Valid(a) || !utf8.Valid(b):
		return Body{Mode: "binary", Changes: field("size", strconv.Itoa(len(a)), strconv.Itoa(len(b)))}
	}
	var ja, jb any
	if json.Unmarshal(a, &ja) == nil && json.Unmarshal(b, &jb) == nil {
		fa, fb := map[string]string{}, map[string]string{}
		flatten(ja, "$", fa)
		flatten(jb, "$", fb)
		c := maps("", fa, fb)
		if len(c) == 0 { // same data, different formatting
			return Body{Mode: "same"}
		}
		body := Body{Mode: "json", Changes: c}
		if len(c) > maxChanges {
			body.Changes, body.Truncated = c[:maxChanges], true
		}
		return body
	}
	lines, cut := Lines(string(a), string(b))
	return Body{Mode: "text", Lines: lines, Truncated: cut}
}

// flatten turns JSON into path → compact JSON value; empty objects and arrays are leaves.
func flatten(v any, path string, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 {
			out[path] = "{}"
		}
		for k, c := range t {
			flatten(c, path+"."+k, out)
		}
	case []any:
		if len(t) == 0 {
			out[path] = "[]"
		}
		for i, c := range t {
			flatten(c, path+"["+strconv.Itoa(i)+"]", out)
		}
	default:
		b, _ := json.Marshal(t)
		out[path] = string(b)
	}
}

// Lines is a line diff with up to 3 unchanged lines around each change.
func Lines(a, b string) ([]Line, bool) {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	pre := 0
	for pre < len(la) && pre < len(lb) && la[pre] == lb[pre] {
		pre++
	}
	suf := 0
	for suf < len(la)-pre && suf < len(lb)-pre && la[len(la)-1-suf] == lb[len(lb)-1-suf] {
		suf++
	}
	var ops []Line
	for _, s := range la[:pre] {
		ops = append(ops, Line{" ", s})
	}
	ops = append(ops, middle(la[pre:len(la)-suf], lb[pre:len(lb)-suf])...)
	for _, s := range la[len(la)-suf:] {
		ops = append(ops, Line{" ", s})
	}
	return withContext(ops)
}

// middle is an LCS line diff; past maxCells it shows all of a as removed, then b as added.
func middle(a, b []string) []Line {
	var out []Line
	if len(a)*len(b) > maxCells {
		for _, s := range a {
			out = append(out, Line{"-", s})
		}
		for _, s := range b {
			out = append(out, Line{"+", s})
		}
		return out
	}
	// lcs[i][j] = length of the LCS of a[i:] and b[j:].
	lcs := make([][]int32, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int32, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, Line{" ", a[i]})
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, Line{"-", a[i]})
			i++
		default:
			out = append(out, Line{"+", b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, Line{"-", a[i]})
	}
	for ; j < len(b); j++ {
		out = append(out, Line{"+", b[j]})
	}
	return out
}

func withContext(ops []Line) ([]Line, bool) {
	keep := make([]bool, len(ops))
	for i, l := range ops {
		if l.Op != " " {
			for k := max(0, i-context); k <= min(len(ops)-1, i+context); k++ {
				keep[k] = true
			}
		}
	}
	var out []Line
	for i, l := range ops {
		if keep[i] {
			out = append(out, l)
		} else if len(out) == 0 || out[len(out)-1].Op != "…" {
			out = append(out, Line{Op: "…"})
		}
		if len(out) >= maxLines {
			return out, true
		}
	}
	return out, false
}
