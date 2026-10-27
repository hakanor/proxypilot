package rules

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
)

// compiled is a rule with its matcher prepared.
type compiled struct {
	*Rule
	host    string // exact lower-case host for the index; "" = any host
	urlRe   *regexp.Regexp
	prefix  string // literal part before the first '*' (Map Local folder mapping)
	hdrRe   map[string]*regexp.Regexp
	mlocal  *MapLocal
	bp      *BreakpointSpec
	mremote *MapRemoteSpec
	rewrite *RewriteSpec
	hits    *atomic.Int64
}

// wildcardRe turns "https://api.x.com/users/*" into an anchored regexp.
// A pattern without scheme matches both http and https; without a path it matches the whole host.
func wildcardRe(p string) *regexp.Regexp {
	if p == "" {
		return regexp.MustCompile(`.*`)
	}
	if !strings.Contains(p, "://") {
		p = "*://" + p
	}
	if rest := p[strings.Index(p, "://")+3:]; !strings.ContainsAny(rest, "/?") {
		p += "*" // host only: everything on it
	}
	var b strings.Builder
	b.WriteString("(?i)^")
	for _, part := range strings.Split(p, "*") {
		b.WriteString(regexp.QuoteMeta(part))
		b.WriteString(".*")
	}
	s := strings.TrimSuffix(b.String(), ".*") + "$"
	return regexp.MustCompile(s)
}

// hostOf extracts a literal host from a wildcard URL for the index ("" if it has wildcards).
func hostOf(p string) string {
	if p == "" {
		return ""
	}
	if !strings.Contains(p, "://") {
		p = "x://" + p
	}
	u, err := url.Parse(strings.ReplaceAll(p, "*", "__wild__"))
	if err != nil || strings.Contains(u.Host, "__wild__") {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func compile(r *Rule) (*compiled, error) {
	c := &compiled{Rule: r}
	if r.Match.Regex {
		re, err := regexp.Compile(r.Match.URL)
		if err != nil {
			return nil, err
		}
		c.urlRe = re
	} else {
		c.urlRe = wildcardRe(r.Match.URL)
		c.host = hostOf(r.Match.URL)
		c.prefix, _, _ = strings.Cut(r.Match.URL, "*")
	}
	if len(r.Match.Headers) > 0 {
		c.hdrRe = map[string]*regexp.Regexp{}
		for k, v := range r.Match.Headers {
			c.hdrRe[http.CanonicalHeaderKey(k)] = wildcardValue(v)
		}
	}
	switch r.Type {
	case TypeMapLocal:
		ml, err := parseMapLocal(r.Spec)
		if err != nil {
			return nil, err
		}
		c.mlocal = ml
	case TypeBreakpoint:
		bp, err := parseBreakpoint(r.Spec)
		if err != nil {
			return nil, err
		}
		c.bp = bp
	case TypeMapRemote:
		m, err := parseMapRemote(r.Spec)
		if err != nil {
			return nil, err
		}
		c.mremote = m
	case TypeRewrite:
		rw, err := parseRewrite(r.Spec, r.Phase)
		if err != nil {
			return nil, err
		}
		c.rewrite = rw
	}
	return c, nil
}

func wildcardValue(v string) *regexp.Regexp {
	parts := strings.Split(v, "*")
	for i := range parts {
		parts[i] = regexp.QuoteMeta(parts[i])
	}
	return regexp.MustCompile("(?i)^" + strings.Join(parts, ".*") + "$")
}

// matches checks method, URL, headers and the GraphQL operation (ops: names in the
// request, "" if not needed or not GraphQL). The host index was consulted already.
func (c *compiled) matches(req *http.Request, fullURL string, ops []string) bool {
	if want := c.Match.GraphQLOperation; want != "" {
		hit := false
		for _, op := range ops {
			if op == want || (strings.Contains(want, "*") && wildcardValue(want).MatchString(op)) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if len(c.Match.Methods) > 0 {
		ok := false
		for _, m := range c.Match.Methods {
			if m == req.Method {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if !c.urlRe.MatchString(fullURL) {
		return false
	}
	for k, re := range c.hdrRe {
		if !re.MatchString(req.Header.Get(k)) {
			return false
		}
	}
	return true
}
