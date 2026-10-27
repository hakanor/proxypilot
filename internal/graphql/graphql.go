// Package graphql recognizes GraphQL requests and responses: operation names and
// types for the traffic list and rule matching, and `errors` in 200 responses.
package graphql

import (
	"bytes"
	"encoding/json"
	"mime"
	"net/url"
	"regexp"
	"strings"
)

// Op is one operation of a request (batched requests have several).
type Op struct {
	Name      string          `json:"name"`
	Type      string          `json:"type"` // query | mutation | subscription
	Query     string          `json:"query,omitempty"`
	Variables json.RawMessage `json:"variables,omitempty"`
	// Persisted queries send only a hash.
	Persisted bool `json:"persisted,omitempty"`
}

var opRe = regexp.MustCompile(`^\s*(query|mutation|subscription)\b\s*([_A-Za-z][_0-9A-Za-z]*)?`)

// stripComments removes # comments so the operation keyword is found.
func stripComments(q string) string {
	var b strings.Builder
	for _, line := range strings.Split(q, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// parseQuery finds the first operation's type and name ("{ me }" is an anonymous query).
func parseQuery(q string) (typ, name string) {
	q = stripComments(q)
	// Skip leading fragment definitions to reach the operation.
	for {
		t := strings.TrimSpace(q)
		if !strings.HasPrefix(t, "fragment ") {
			break
		}
		depth, end := 0, -1
		for i, r := range t {
			if r == '{' {
				depth++
			} else if r == '}' {
				depth--
				if depth == 0 {
					end = i
					break
				}
			}
		}
		if end < 0 {
			break
		}
		q = t[end+1:]
	}
	if m := opRe.FindStringSubmatch(q); m != nil {
		return m[1], m[2]
	}
	if strings.HasPrefix(strings.TrimSpace(q), "{") {
		return "query", ""
	}
	return "", ""
}

type payload struct {
	Query         string          `json:"query"`
	OperationName string          `json:"operationName"`
	Variables     json.RawMessage `json:"variables"`
	Extensions    struct {
		Persisted *struct {
			Hash string `json:"sha256Hash"`
		} `json:"persistedQuery"`
	} `json:"extensions"`
}

func (p payload) op() (Op, bool) {
	o := Op{Name: p.OperationName, Query: p.Query, Variables: p.Variables}
	if p.Query != "" {
		typ, name := parseQuery(p.Query)
		if typ == "" {
			return Op{}, false
		}
		o.Type = typ
		if o.Name == "" {
			o.Name = name
		}
		return o, true
	}
	if p.Extensions.Persisted != nil {
		o.Persisted, o.Type = true, "query"
		return o, o.Name != ""
	}
	return Op{}, false
}

// Detect returns the operations of a request, or nil if it is not GraphQL.
// body may be truncated; a body that does not parse is not GraphQL.
func Detect(method, rawURL, contentType string, body []byte) []Op {
	mt, _, _ := mime.ParseMediaType(contentType)
	switch {
	case mt == "application/graphql":
		typ, name := parseQuery(string(body))
		if typ == "" {
			return nil
		}
		return []Op{{Name: name, Type: typ, Query: string(body)}}
	case strings.EqualFold(method, "GET"):
		u, err := url.Parse(rawURL)
		if err != nil {
			return nil
		}
		q := u.Query()
		if q.Get("query") == "" && q.Get("extensions") == "" {
			return nil
		}
		p := payload{Query: q.Get("query"), OperationName: q.Get("operationName")}
		if v := q.Get("variables"); v != "" {
			p.Variables = json.RawMessage(v)
		}
		if e := q.Get("extensions"); e != "" {
			_ = json.Unmarshal([]byte(e), &p.Extensions)
		}
		if o, ok := p.op(); ok {
			return []Op{o}
		}
		return nil
	}
	b := bytes.TrimSpace(body)
	if len(b) == 0 || (b[0] != '{' && b[0] != '[') {
		return nil
	}
	if b[0] == '[' { // batched
		var list []payload
		if json.Unmarshal(b, &list) != nil {
			return nil
		}
		var ops []Op
		for _, p := range list {
			if o, ok := p.op(); ok {
				ops = append(ops, o)
			}
		}
		return ops
	}
	var p payload
	if json.Unmarshal(b, &p) != nil {
		return nil
	}
	if o, ok := p.op(); ok {
		return []Op{o}
	}
	return nil
}

// Names joins operation names for tags ("FeedQuery,Like"); anonymous ops show as "anonymous".
func Names(ops []Op) string {
	names := make([]string, len(ops))
	for i, o := range ops {
		names[i] = o.Name
		if names[i] == "" {
			names[i] = "anonymous"
		}
	}
	return strings.Join(names, ",")
}

// Errors returns the messages of a GraphQL response's `errors` (also inside batches).
func Errors(body []byte) []string {
	b := bytes.TrimSpace(body)
	if len(b) == 0 {
		return nil
	}
	type resp struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	var list []resp
	if b[0] == '[' {
		if json.Unmarshal(b, &list) != nil {
			return nil
		}
	} else {
		var r resp
		if json.Unmarshal(b, &r) != nil {
			return nil
		}
		list = []resp{r}
	}
	var out []string
	for _, r := range list {
		for _, e := range r.Errors {
			out = append(out, e.Message)
		}
	}
	return out
}
