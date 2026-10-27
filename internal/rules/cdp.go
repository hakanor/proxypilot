package rules

import (
	"strings"

	"github.com/hakanor/proxypilot/internal/proxy"
)

// cdpPattern turns a rule URL into a DevTools Fetch urlPattern (* and ? wildcards).
func cdpPattern(c *compiled) string {
	if c.Match.Regex || c.Match.URL == "" {
		return "*"
	}
	p := c.Match.URL
	if !strings.Contains(p, "://") {
		p = "*://" + p
	}
	if rest := p[strings.Index(p, "://")+3:]; !strings.ContainsAny(rest, "/?") {
		p += "*"
	}
	return strings.ReplaceAll(p, "?", `\?`)
}

// Patterns lists Fetch.enable url patterns for the request and response stages, so
// Chrome only pauses requests some rule may act on. Empty slices = nothing to intercept.
func (e *Engine) Patterns() (request, response []string) {
	ix := e.index.Load()
	add := func(list *[]string, p string) {
		for _, x := range *list {
			if x == p {
				return
			}
		}
		*list = append(*list, p)
	}
	visit := func(c *compiled) {
		p := cdpPattern(c)
		switch {
		case c.mremote != nil:
			add(&request, p)
		case c.mlocal != nil && c.mlocal.Mode == ModeFull:
			add(&request, p)
		case c.mlocal != nil:
			add(&response, p)
		case c.rewrite != nil, c.bp != nil:
			if c.Phase != PhaseResponse {
				add(&request, p)
			}
			if c.Phase != PhaseRequest {
				add(&response, p)
			}
		}
	}
	for _, list := range ix.byHost {
		for _, c := range list {
			visit(c)
		}
	}
	for _, c := range ix.any {
		visit(c)
	}
	collapse := func(l []string) []string {
		for _, x := range l {
			if x == "*" {
				return []string{"*"}
			}
		}
		return l
	}
	return collapse(request), collapse(response)
}

// TagMatches marks f with the rules that match its request, for a response-stage
// call to OnResponse on a flow that did not go through OnRequest (Chrome).
func (e *Engine) TagMatches(f *proxy.Flow) {
	ms := e.Match(f)
	ids := make([]string, 0, len(ms))
	for _, c := range ms {
		ids = append(ids, c.ID)
	}
	tagMatched(f, ids)
}
