package rules

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/hakanor/proxypilot/internal/breakpoints"
	"github.com/hakanor/proxypilot/internal/events"
	"github.com/hakanor/proxypilot/internal/graphql"
	"github.com/hakanor/proxypilot/internal/proxy"
)

const File = "rules.json"

var ErrNotFound = errors.New("rule not found")

// Engine holds the rules and applies them as proxy hooks. With no rules the
// hooks return immediately (one atomic load).
type Engine struct {
	proxy.NopHooks
	path string
	bus  *events.Bus
	log  *slog.Logger

	mu    sync.RWMutex
	rules map[string]*Rule
	hits  map[string]*atomic.Int64 // per rule id; survives updates and toggles
	// index is rebuilt on every change and read lock-free by the hooks.
	index atomic.Pointer[index]
	now   func() time.Time
	pause Pauser
}

// Pauser holds flows for breakpoint rules (the breakpoints manager).
type Pauser interface {
	PauseRequest(f *proxy.Flow, o breakpoints.Options) proxy.Decision
	PauseResponse(f *proxy.Flow, o breakpoints.Options) proxy.Decision
}

// SetPauser enables breakpoint rules. Without it they only match (and are counted).
func (e *Engine) SetPauser(p Pauser) { e.pause = p }

func (c *compiled) bpOptions() breakpoints.Options {
	return breakpoints.Options{RuleID: c.ID, RuleName: c.Name, Owner: c.Owner, Timeout: c.bp.timeout, OnTimeout: c.bp.OnTimeout}
}

type index struct {
	byHost map[string][]*compiled // exact host
	any    []*compiled            // wildcard host, regex, or empty URL
	n      int
	gql    bool // some rule matches on a GraphQL operation (needs the request body)
}

func New(dataDir string, bus *events.Bus, log *slog.Logger) (*Engine, error) {
	if log == nil {
		log = slog.Default()
	}
	e := &Engine{bus: bus, log: log, rules: map[string]*Rule{}, hits: map[string]*atomic.Int64{}, now: time.Now}
	if dataDir != "" {
		e.path = filepath.Join(dataDir, File)
		if err := e.load(); err != nil {
			return nil, err
		}
	}
	e.rebuild()
	return e, nil
}

func (e *Engine) load() error {
	b, err := os.ReadFile(e.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var list []*Rule
	if err := json.Unmarshal(b, &list); err != nil {
		return fmt.Errorf("%s: %w", e.path, err)
	}
	for _, r := range list {
		if err := r.validate(); err != nil {
			e.log.Warn("skipping invalid rule", "id", r.ID, "err", err)
			continue
		}
		e.rules[r.ID] = r
	}
	return nil
}

// save persists user rules only; agent rules live in memory with their TTL. Caller holds mu.
func (e *Engine) save() error {
	if e.path == "" {
		return nil
	}
	list := make([]*Rule, 0, len(e.rules))
	for _, r := range e.rules {
		if !r.IsAgent() {
			c := *r
			c.Hits = 0
			list = append(list, &c)
		}
	}
	sortRules(list)
	b, _ := json.MarshalIndent(list, "", "  ")
	tmp := e.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, e.path)
}

// sortRules orders by priority (high first), then creation time.
func sortRules[T interface{ ~*Rule }](list []T) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := (*Rule)(list[i]), (*Rule)(list[j])
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
}

// counter returns the hit counter for id. Caller holds mu.
func (e *Engine) counter(id string) *atomic.Int64 {
	n, ok := e.hits[id]
	if !ok {
		n = &atomic.Int64{}
		e.hits[id] = n
	}
	return n
}

func (e *Engine) withHits(r *Rule) *Rule {
	c := *r
	if n, ok := e.hits[r.ID]; ok {
		c.Hits = n.Load()
	}
	return &c
}

// rebuild compiles enabled rules into the host index. Caller holds mu (or is New).
func (e *Engine) rebuild() {
	ix := &index{byHost: map[string][]*compiled{}}
	list := make([]*Rule, 0, len(e.rules))
	for _, r := range e.rules {
		if r.Enabled {
			list = append(list, r)
		}
	}
	sortRules(list)
	for _, r := range list {
		c, err := compile(r)
		if err != nil {
			e.log.Warn("rule does not compile", "id", r.ID, "err", err)
			continue
		}
		c.hits = e.counter(r.ID)
		if r.Match.GraphQLOperation != "" {
			ix.gql = true
		}
		if c.host != "" {
			ix.byHost[c.host] = append(ix.byHost[c.host], c)
		} else {
			ix.any = append(ix.any, c)
		}
		ix.n++
	}
	e.index.Store(ix)
}

func (e *Engine) changed(action string, r *Rule) {
	if e.bus != nil {
		e.bus.Publish(events.RuleChanged, map[string]any{"action": action, "rule": r})
	}
}

// Add validates and stores a new rule. ttl > 0 sets ExpiresAt.
func (e *Engine) Add(r Rule, ttl time.Duration) (*Rule, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	r.ID = "r_" + strings.ToLower(ulid.Make().String())
	r.CreatedAt = e.now().UTC()
	r.Hits = 0
	if ttl > 0 {
		t := e.now().Add(ttl).UTC()
		r.ExpiresAt = &t
	}
	e.mu.Lock()
	e.rules[r.ID] = &r
	err := e.save()
	e.rebuild()
	e.mu.Unlock()
	e.changed("created", &r)
	return &r, err
}

// Update replaces a rule's fields (ID, CreatedAt and Hits are kept).
func (e *Engine) Update(id string, r Rule) (*Rule, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	old, ok := e.rules[id]
	if !ok {
		e.mu.Unlock()
		return nil, ErrNotFound
	}
	r.ID, r.CreatedAt, r.Hits = id, old.CreatedAt, 0
	e.rules[id] = &r
	err := e.save()
	e.rebuild()
	e.mu.Unlock()
	e.changed("updated", &r)
	return &r, err
}

func (e *Engine) Toggle(id string, enabled *bool) (*Rule, error) {
	e.mu.Lock()
	r, ok := e.rules[id]
	if !ok {
		e.mu.Unlock()
		return nil, ErrNotFound
	}
	c := *r
	if enabled != nil {
		c.Enabled = *enabled
	} else {
		c.Enabled = !c.Enabled
	}
	e.rules[id] = &c
	err := e.save()
	e.rebuild()
	e.mu.Unlock()
	e.changed("updated", &c)
	return &c, err
}

func (e *Engine) Delete(id string) error {
	e.mu.Lock()
	r, ok := e.rules[id]
	if !ok {
		e.mu.Unlock()
		return ErrNotFound
	}
	delete(e.rules, id)
	delete(e.hits, id)
	err := e.save()
	e.rebuild()
	e.mu.Unlock()
	e.changed("deleted", r)
	return err
}

// DeleteOwner removes rules whose owner matches pattern ("agent:*", "agent:claude", "user").
func (e *Engine) DeleteOwner(pattern string) (int, error) {
	e.mu.Lock()
	var gone []*Rule
	for id, r := range e.rules {
		if ok, _ := path.Match(pattern, r.Owner); ok {
			gone = append(gone, r)
			delete(e.rules, id)
			delete(e.hits, id)
		}
	}
	err := e.save()
	e.rebuild()
	e.mu.Unlock()
	for _, r := range gone {
		e.changed("deleted", r)
	}
	return len(gone), err
}

func (e *Engine) Get(id string) (*Rule, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	r, ok := e.rules[id]
	if !ok {
		return nil, ErrNotFound
	}
	return e.withHits(r), nil
}

// List returns all rules in evaluation order.
func (e *Engine) List() []*Rule {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]*Rule, 0, len(e.rules))
	for _, r := range e.rules {
		out = append(out, e.withHits(r))
	}
	sortRules(out)
	return out
}

// Active is the number of enabled rules (status bar).
func (e *Engine) Active() int { return e.index.Load().n }

// Expire deletes rules past their TTL.
func (e *Engine) Expire() int {
	now := e.now()
	e.mu.Lock()
	var gone []*Rule
	for id, r := range e.rules {
		if r.ExpiresAt != nil && !now.Before(*r.ExpiresAt) {
			gone = append(gone, r)
			delete(e.rules, id)
			delete(e.hits, id)
		}
	}
	if len(gone) > 0 {
		if err := e.save(); err != nil {
			e.log.Warn("saving rules failed", "err", err)
		}
		e.rebuild()
	}
	e.mu.Unlock()
	for _, r := range gone {
		e.changed("expired", r)
	}
	return len(gone)
}

// Run expires rules every second until ctx ends.
func (e *Engine) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.Expire()
		}
	}
}

// requestURL is the canonical URL rules match against: scheme://host[:non-default port]/path?query.
func requestURL(f *proxy.Flow) (string, string) {
	u := *f.Request.URL
	u.Scheme = f.Scheme
	host, port, err := net.SplitHostPort(f.Host)
	if err != nil {
		host = f.Host
	}
	host = strings.ToLower(host)
	u.Host = host
	if port != "" && !(port == "443" && f.Scheme == "https") && !(port == "80" && f.Scheme == "http") {
		u.Host = net.JoinHostPort(host, port)
	}
	u.User, u.Fragment = nil, ""
	return u.String(), host
}

// Match returns the enabled rules that match f's request, highest priority first.
func (e *Engine) Match(f *proxy.Flow) []*compiled {
	ix := e.index.Load()
	if ix.n == 0 || f.Request == nil {
		return nil
	}
	full, host := requestURL(f)
	var ops []string
	if ix.gql {
		ops = graphqlOps(f)
	}
	var out []*compiled
	for _, list := range [][]*compiled{ix.byHost[host], ix.any} {
		for _, c := range list {
			if c.matches(f.Request, full, ops) {
				out = append(out, c)
			}
		}
	}
	if len(ix.byHost[host]) > 0 && len(ix.any) > 0 {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Priority > out[j].Priority })
	}
	return out
}

func tagMatched(f *proxy.Flow, ids []string) {
	if len(ids) > 0 {
		f.SetTag("matched_rules", strings.Join(ids, ","))
	}
}

// OnRequest applies the first matching Map Local in full mode.
func (e *Engine) OnRequest(f *proxy.Flow) proxy.Decision {
	if e.index.Load().n == 0 {
		return proxy.Decision{}
	}
	ms := e.Match(f)
	if len(ms) == 0 {
		return proxy.Decision{}
	}
	ids := make([]string, 0, len(ms))
	for _, c := range ms {
		ids = append(ids, c.ID)
		c.hits.Add(1)
	}
	tagMatched(f, ids)
	full, _ := requestURL(f)
	mapped := false
	for _, c := range ms {
		if c.mremote != nil && !mapped {
			mapped = true
			applyMapRemote(c, f, full)
			full, _ = requestURL(f)
			continue
		}
		if c.rewrite != nil && c.Phase != PhaseResponse {
			if err := c.rewriteRequest(f.Request); err != nil {
				e.log.Warn("rewrite failed", "rule", c.ID, "err", err)
				f.SetTag("rewrite_error", err.Error())
			} else {
				f.SetTag("rewritten", "request")
			}
			if host := f.Request.URL.Host; host != "" {
				f.Host = hostPort(f.Request.URL)
				f.Scheme = f.Request.URL.Scheme
			}
			full, _ = requestURL(f)
			continue
		}
		if c.bp != nil && e.pause != nil && c.Phase != PhaseResponse {
			if d := e.pause.PauseRequest(f, c.bpOptions()); d.Action != proxy.Continue {
				return d
			}
			full, _ = requestURL(f) // the decision may have edited the request
			continue
		}
		if c.mlocal != nil && c.mlocal.Mode == ModeFull && c.Phase != PhaseResponse {
			f.SetTag("map_local", c.ID)
			return proxy.Decision{Action: proxy.Respond, Response: c.mlocal.response(c, f.Request, full)}
		}
	}
	return proxy.Decision{}
}

// OnResponse applies the first body_only Map Local, then the first response-phase
// breakpoint (which therefore shows what the client would receive).
func (e *Engine) OnResponse(f *proxy.Flow) proxy.Decision {
	if e.index.Load().n == 0 || f.Response == nil || f.Tags()["matched_rules"] == "" {
		return proxy.Decision{}
	}
	full, _ := requestURL(f)
	var bodyOnly, bp *compiled
	var rewrites []*compiled
	for _, c := range e.Match(f) {
		if c.rewrite != nil && c.Phase != PhaseRequest {
			rewrites = append(rewrites, c)
		}
		if bodyOnly == nil && c.mlocal != nil && c.mlocal.Mode == ModeBodyOnly {
			bodyOnly = c
		}
		if bp == nil && c.bp != nil && e.pause != nil && c.Phase != PhaseRequest {
			bp = c
		}
	}
	changed := false
	if bodyOnly != nil {
		f.Response = bodyOnly.mlocal.replaceBody(bodyOnly, f.Response, full)
		f.SetTag("map_local", bodyOnly.ID)
		changed = true
	}
	for _, c := range rewrites {
		out, err := c.rewriteResponse(f.Response)
		if err != nil {
			e.log.Warn("rewrite failed", "rule", c.ID, "err", err)
			f.SetTag("rewrite_error", err.Error())
			continue
		}
		f.Response, changed = out, true
		f.SetTag("rewritten", "response")
	}
	if bp != nil {
		if d := e.pause.PauseResponse(f, bp.bpOptions()); d.Action != proxy.Continue {
			return d
		}
	}
	if changed {
		return proxy.Decision{Action: proxy.Respond, Response: f.Response}
	}
	return proxy.Decision{}
}

var _ proxy.Hooks = (*Engine)(nil)

// applyMapRemote points the outgoing request at the rule's target and records both URLs.
func applyMapRemote(c *compiled, f *proxy.Flow, original string) {
	req := f.Request
	u := c.mapRemote(req, original)
	req.URL = u
	if !c.mremote.PreserveHostHeader {
		req.Host = u.Host
	}
	f.Scheme, f.Host = u.Scheme, hostPort(u)
	f.SetTag("original_url", original)
	f.SetTag("map_remote_to", u.String())
}

func hostPort(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	if u.Scheme == "https" {
		return u.Hostname() + ":443"
	}
	return u.Hostname() + ":80"
}

// graphqlOps returns the GraphQL operation names of f's request, reading (and
// replaying) up to 1 MB of the body once per flow.
func graphqlOps(f *proxy.Flow) []string {
	if v, ok := f.Tags()["gql_req_ops"]; ok {
		if v == "" {
			return nil
		}
		return strings.Split(v, ",")
	}
	req := f.Request
	var body []byte
	if req.Body != nil && req.Body != http.NoBody {
		orig := req.Body
		body, _ = io.ReadAll(io.LimitReader(orig, 1<<20))
		req.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(body), orig), orig}
	}
	ops := graphql.Detect(req.Method, req.URL.String(), req.Header.Get("Content-Type"), body)
	names := ""
	if len(ops) > 0 {
		names = graphql.Names(ops)
	}
	f.SetTag("gql_req_ops", names)
	if names == "" {
		return nil
	}
	return strings.Split(names, ",")
}
