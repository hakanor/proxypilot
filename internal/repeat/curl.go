package repeat

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/hakanor/proxypilot/internal/store"
)

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Curl renders req as a cURL command. A non-UTF-8 body is referenced as a file.
func Curl(req Request) (cmd string, binary bool) {
	lines := []string{"curl"}
	// curl defaults to GET, or POST when there is a body; say the method only otherwise.
	m := strings.ToUpper(req.Method)
	if m != "" && !(m == "GET" && req.Body == "") && !(m == "POST" && req.Body != "") {
		lines[0] += " -X " + m
	}
	lines[0] += " " + shq(req.URL)
	compressed := false
	for _, h := range req.Headers {
		if strings.EqualFold(h.Name, "Accept-Encoding") {
			compressed = true
			continue
		}
		if strings.EqualFold(h.Name, "Host") || strings.EqualFold(h.Name, "Content-Length") {
			continue
		}
		lines = append(lines, "-H "+shq(h.Name+": "+h.Value))
	}
	if compressed {
		lines = append(lines, "--compressed")
	}
	if req.Body != "" {
		if utf8.ValidString(req.Body) {
			lines = append(lines, "--data-raw "+shq(req.Body))
		} else {
			lines = append(lines, "--data-binary @body.bin")
			binary = true
		}
	}
	return strings.Join(lines, " \\\n  "), binary
}

// shellWords splits a command line like a POSIX shell: quotes, backslashes, line continuations.
func shellWords(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	in := false
	quote := rune(0)
	esc := false
	for i, r := range s {
		switch {
		case esc:
			if r != '\n' {
				cur.WriteRune(r)
				in = true
			}
			esc = false
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch {
			case r == '"':
				quote = 0
			case r == '\\' && i+1 < len(s) && strings.ContainsRune("\"\\$`\n", rune(s[i+1])):
				esc = true
			default:
				cur.WriteRune(r)
			}
		case r == '\\':
			esc = true
		case r == '\'' || r == '"':
			quote, in = r, true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if in {
				words = append(words, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if in {
		words = append(words, cur.String())
	}
	return words, nil
}

// ParseCurl reads a cURL command (as copied from browser devtools) into a Request.
func ParseCurl(cmd string) (Request, error) {
	words, err := shellWords(strings.TrimSpace(cmd))
	if err != nil {
		return Request{}, err
	}
	if len(words) == 0 || words[0] != "curl" {
		return Request{}, fmt.Errorf("not a curl command")
	}
	var req Request
	var data []string
	get := false
	for i := 1; i < len(words); i++ {
		w := words[i]
		next := func() (string, error) {
			if i+1 >= len(words) {
				return "", fmt.Errorf("%s needs a value", w)
			}
			i++
			return words[i], nil
		}
		// --flag=value form
		if strings.HasPrefix(w, "--") && strings.Contains(w, "=") {
			k, v, _ := strings.Cut(w, "=")
			words = append(words[:i+1], append([]string{v}, words[i+1:]...)...)
			w = k
		}
		switch w {
		case "-X", "--request":
			v, err := next()
			if err != nil {
				return req, err
			}
			req.Method = strings.ToUpper(v)
		case "-H", "--header":
			v, err := next()
			if err != nil {
				return req, err
			}
			k, val, ok := strings.Cut(v, ":")
			if !ok {
				return req, fmt.Errorf("header %q has no colon", v)
			}
			req.Headers = append(req.Headers, store.Header{Name: strings.TrimSpace(k), Value: strings.TrimSpace(val)})
		case "-d", "--data", "--data-raw", "--data-binary", "--data-ascii", "--data-urlencode":
			v, err := next()
			if err != nil {
				return req, err
			}
			if strings.HasPrefix(v, "@") && w != "--data-raw" {
				return req, fmt.Errorf("%s @file is not supported here; paste the body instead", w)
			}
			if w == "--data-urlencode" {
				if k, val, ok := strings.Cut(v, "="); ok {
					v = k + "=" + url.QueryEscape(val)
				} else {
					v = url.QueryEscape(v)
				}
			}
			data = append(data, v)
		case "--json":
			v, err := next()
			if err != nil {
				return req, err
			}
			data = append(data, v)
			req.Headers = append(req.Headers, store.Header{Name: "Content-Type", Value: "application/json"}, store.Header{Name: "Accept", Value: "application/json"})
		case "-u", "--user":
			v, err := next()
			if err != nil {
				return req, err
			}
			req.Headers = append(req.Headers, store.Header{Name: "Authorization", Value: "Basic " + base64.StdEncoding.EncodeToString([]byte(v))})
		case "-A", "--user-agent":
			v, err := next()
			if err != nil {
				return req, err
			}
			req.Headers = append(req.Headers, store.Header{Name: "User-Agent", Value: v})
		case "-b", "--cookie":
			v, err := next()
			if err != nil {
				return req, err
			}
			req.Headers = append(req.Headers, store.Header{Name: "Cookie", Value: v})
		case "-e", "--referer":
			v, err := next()
			if err != nil {
				return req, err
			}
			req.Headers = append(req.Headers, store.Header{Name: "Referer", Value: v})
		case "--url":
			v, err := next()
			if err != nil {
				return req, err
			}
			req.URL = v
		case "-G", "--get":
			get = true
		case "-I", "--head":
			req.Method = "HEAD"
		case "--compressed":
			req.Headers = append(req.Headers, store.Header{Name: "Accept-Encoding", Value: "gzip, deflate, br"})
		case "-x", "--proxy", "-o", "--output", "-w", "--write-out", "--connect-timeout", "-m", "--max-time", "--retry", "-T", "--upload-file":
			if _, err := next(); err != nil {
				return req, err
			}
		default:
			if strings.HasPrefix(w, "-") {
				continue // -s -k -L -v -i and friends don't change the request
			}
			if req.URL == "" {
				req.URL = w
			}
		}
	}
	if req.URL == "" {
		return req, fmt.Errorf("no URL in the curl command")
	}
	if !strings.Contains(req.URL, "://") {
		req.URL = "http://" + req.URL
	}
	body := strings.Join(data, "&")
	switch {
	case get && body != "":
		sep := "?"
		if strings.Contains(req.URL, "?") {
			sep = "&"
		}
		req.URL += sep + body
	case body != "":
		req.Body = body
		if req.Method == "" {
			req.Method = "POST"
		}
		hasCT := false
		for _, h := range req.Headers {
			if strings.EqualFold(h.Name, "Content-Type") {
				hasCT = true
			}
		}
		if !hasCT {
			req.Headers = append(req.Headers, store.Header{Name: "Content-Type", Value: "application/x-www-form-urlencoded"})
		}
	}
	if req.Method == "" {
		req.Method = "GET"
	}
	return req, nil
}
