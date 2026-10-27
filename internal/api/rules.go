package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hakanor/proxypilot/internal/rules"
	"github.com/hakanor/proxypilot/internal/store"
)

func (s *Server) ruleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/rules", s.handleListRules)
	mux.HandleFunc("POST /api/v1/rules", s.handleCreateRule)
	mux.HandleFunc("DELETE /api/v1/rules", s.handleDeleteRules)
	mux.HandleFunc("GET /api/v1/rules/{id}", s.handleGetRule)
	mux.HandleFunc("PUT /api/v1/rules/{id}", s.handleUpdateRule)
	mux.HandleFunc("DELETE /api/v1/rules/{id}", s.handleDeleteRule)
	mux.HandleFunc("POST /api/v1/rules/{id}/toggle", s.handleToggleRule)
	mux.HandleFunc("GET /api/v1/rules/{id}/content", s.handleGetRuleContent)
	mux.HandleFunc("PUT /api/v1/rules/{id}/content", s.handlePutRuleContent)
	mux.HandleFunc("POST /api/v1/flows/{id}/map-local", s.handleSaveMapLocal)
}

// RuleInput is a rule as clients send it; ttl ("30m") sets expires_at.
type RuleInput struct {
	rules.Rule
	TTL string `json:"ttl,omitempty"`
}

func (s *Server) rulesAvailable(w http.ResponseWriter) bool {
	if s.deps.Rules == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "rules unavailable")
		return false
	}
	return true
}

func ruleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, rules.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, rules.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_rule", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

func decodeRule(r *http.Request) (rules.Rule, time.Duration, error) {
	var in RuleInput
	in.Enabled = true // default for new rules when the field is omitted
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&in); err != nil {
		return rules.Rule{}, 0, fmt.Errorf("%w: invalid JSON: %v", rules.ErrInvalid, err)
	}
	var ttl time.Duration
	if in.TTL != "" {
		d, err := time.ParseDuration(in.TTL)
		if err != nil || d <= 0 {
			return rules.Rule{}, 0, fmt.Errorf("%w: ttl must be a positive duration like 30m", rules.ErrInvalid)
		}
		ttl = d
	}
	return in.Rule, ttl, nil
}

// handleListRules: ?owner=agent:* filters by owner pattern.
func (s *Server) handleListRules(w http.ResponseWriter, r *http.Request) {
	if !s.rulesAvailable(w) {
		return
	}
	list := s.deps.Rules.List()
	if owner := r.URL.Query().Get("owner"); owner != "" {
		out := list[:0]
		for _, x := range list {
			if ok, _ := filepath.Match(owner, x.Owner); ok {
				out = append(out, x)
			}
		}
		list = out
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": list})
}

func (s *Server) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	if !s.rulesAvailable(w) {
		return
	}
	rule, ttl, err := decodeRule(r)
	if err != nil {
		ruleError(w, err)
		return
	}
	created, err := s.deps.Rules.Add(rule, ttl)
	if err != nil {
		ruleError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleGetRule(w http.ResponseWriter, r *http.Request) {
	if !s.rulesAvailable(w) {
		return
	}
	rule, err := s.deps.Rules.Get(r.PathValue("id"))
	if err != nil {
		ruleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (s *Server) handleUpdateRule(w http.ResponseWriter, r *http.Request) {
	if !s.rulesAvailable(w) {
		return
	}
	rule, ttl, err := decodeRule(r)
	if err != nil {
		ruleError(w, err)
		return
	}
	if ttl > 0 {
		t := time.Now().Add(ttl).UTC()
		rule.ExpiresAt = &t
	}
	updated, err := s.deps.Rules.Update(r.PathValue("id"), rule)
	if err != nil {
		ruleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	if !s.rulesAvailable(w) {
		return
	}
	if err := s.deps.Rules.Delete(r.PathValue("id")); err != nil {
		ruleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": 1})
}

// handleDeleteRules: DELETE /rules?owner=agent:* (owner is required so nothing is wiped by accident).
func (s *Server) handleDeleteRules(w http.ResponseWriter, r *http.Request) {
	if !s.rulesAvailable(w) {
		return
	}
	owner := r.URL.Query().Get("owner")
	if owner == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "owner is required (e.g. owner=agent:* or owner=*)")
		return
	}
	n, err := s.deps.Rules.DeleteOwner(owner)
	if err != nil {
		ruleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

// handleToggleRule flips enabled, or sets it with {"enabled": bool}.
func (s *Server) handleToggleRule(w http.ResponseWriter, r *http.Request) {
	if !s.rulesAvailable(w) {
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&body)
	rule, err := s.deps.Rules.Toggle(r.PathValue("id"), body.Enabled)
	if err != nil {
		ruleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

// handleSaveMapLocal copies a recorded response body to <data>/maplocal/ and
// creates a Map Local rule for the same method and URL, so it can be edited on disk.
func (s *Server) handleSaveMapLocal(w http.ResponseWriter, r *http.Request) {
	if !s.rulesAvailable(w) {
		return
	}
	f, err := s.deps.Store.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "flow not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	var opts struct {
		Owner string `json:"owner"`
		TTL   string `json:"ttl"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&opts)

	dir := filepath.Join(s.deps.DataDir, "maplocal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	ext := ".bin"
	if mt, _, _ := mime.ParseMediaType(f.RespContentType); mt != "" {
		if exts, _ := mime.ExtensionsByType(mt); len(exts) > 0 {
			ext = exts[0]
		}
		if strings.Contains(mt, "json") {
			ext = ".json"
		}
	}
	path := filepath.Join(dir, f.ID+ext)
	data := []byte{}
	if rc, _, err := s.deps.Store.OpenBody(r.Context(), f.ID, store.SideResp); err == nil {
		data, _ = io.ReadAll(rc)
		rc.Close()
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	headers := map[string]string{}
	if f.RespContentType != "" {
		headers["Content-Type"] = f.RespContentType
	}
	status := f.Status
	if status < 100 {
		status = http.StatusOK // failed flows have no status
	}
	spec, _ := json.Marshal(map[string]any{"file": path, "status": status, "headers": headers})
	u := f.URL
	if i := strings.IndexByte(u, '?'); i >= 0 {
		u = u[:i] + "*" // any query
	}
	var ttl time.Duration
	if opts.TTL != "" {
		if ttl, err = time.ParseDuration(opts.TTL); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "ttl: "+err.Error())
			return
		}
	}
	rule, err := s.deps.Rules.Add(rules.Rule{
		Name:    f.Method + " " + f.Host + f.Path,
		Type:    rules.TypeMapLocal,
		Enabled: true,
		Owner:   opts.Owner,
		Match:   rules.Matcher{URL: u, Methods: []string{f.Method}},
		Spec:    spec,
	}, ttl)
	if err != nil {
		ruleError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"rule": rule, "file": path})
}

// handleGetRuleContent returns the response a Map Local rule serves (Rules editor).
func (s *Server) handleGetRuleContent(w http.ResponseWriter, r *http.Request) {
	if !s.rulesAvailable(w) {
		return
	}
	c, err := s.deps.Rules.MapLocalContent(r.PathValue("id"))
	if err != nil {
		ruleContentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// handlePutRuleContent saves edited response text ({"content": "..."}).
func (s *Server) handlePutRuleContent(w http.ResponseWriter, r *http.Request) {
	if !s.rulesAvailable(w) {
		return
	}
	var body struct {
		Content *string `json:"content"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, rules.MaxContent+1024)).Decode(&body); err != nil || body.Content == nil {
		writeError(w, http.StatusBadRequest, "invalid_rule", "body must be {\"content\": \"...\"}")
		return
	}
	c, err := s.deps.Rules.SetMapLocalContent(r.PathValue("id"), *body.Content)
	if err != nil {
		ruleContentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func ruleContentError(w http.ResponseWriter, err error) {
	if errors.Is(err, rules.ErrNotMapLocal) {
		writeError(w, http.StatusBadRequest, "not_map_local", err.Error())
		return
	}
	ruleError(w, err)
}
