// Package rules is the traffic manipulation engine: matching, Map Local, TTL and persistence.
package rules

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Rule types.
const (
	TypeMapLocal   = "map_local"
	TypeMapRemote  = "map_remote"
	TypeRewrite    = "rewrite"
	TypeBreakpoint = "breakpoint"
	TypeThrottle   = "throttle" // rejected: throttling is a proxy setting (PUT /api/v1/throttle)
)

type Phase string

const (
	PhaseRequest  Phase = "request"
	PhaseResponse Phase = "response"
	PhaseBoth     Phase = "both"
)

const OwnerUser = "user"

type Rule struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Type      string          `json:"type"`
	Enabled   bool            `json:"enabled"`
	Owner     string          `json:"owner"` // "user" | "agent:<name>"
	ExpiresAt *time.Time      `json:"expires_at,omitempty"`
	Match     Matcher         `json:"match"`
	Phase     Phase           `json:"phase,omitempty"`
	Spec      json.RawMessage `json:"spec,omitempty"`
	Priority  int             `json:"priority"`
	CreatedAt time.Time       `json:"created_at"`
	// Hits counts matched requests since the daemon started (not persisted).
	Hits int64 `json:"hits"`
}

type Matcher struct {
	URL              string            `json:"url"` // wildcard: https://api.x.com/users/*  (empty = everything)
	Regex            bool              `json:"regex,omitempty"`
	Methods          []string          `json:"methods,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"` // header name → wildcard value
	GraphQLOperation string            `json:"graphql_operation,omitempty"`
}

// IsAgent reports whether an agent owns the rule (kept in memory only).
func (r *Rule) IsAgent() bool { return strings.HasPrefix(r.Owner, "agent:") }

var ErrInvalid = errors.New("invalid rule")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// validate normalizes r and checks fields that do not depend on the type.
func (r *Rule) validate() error {
	r.Type = strings.ToLower(strings.ReplaceAll(r.Type, "-", "_"))
	switch r.Type {
	case TypeMapLocal, TypeBreakpoint, TypeMapRemote, TypeRewrite:
	case TypeThrottle:
		return invalid("throttling is a proxy setting, not a rule: use `proxypilot throttle on 3g --host …` or PUT /api/v1/throttle")
	default:
		return invalid("unknown rule type %q", r.Type)
	}
	if r.Owner == "" {
		r.Owner = OwnerUser
	}
	if r.Owner != OwnerUser && (!strings.HasPrefix(r.Owner, "agent:") || len(r.Owner) == len("agent:")) {
		return invalid(`owner must be "user" or "agent:<name>"`)
	}
	if r.Phase == "" {
		r.Phase = PhaseRequest
	}
	if r.Phase != PhaseRequest && r.Phase != PhaseResponse && r.Phase != PhaseBoth {
		return invalid("phase must be request, response or both")
	}
	if r.Match.Regex {
		if _, err := regexp.Compile(r.Match.URL); err != nil {
			return invalid("url regex: %v", err)
		}
	}
	for i, m := range r.Match.Methods {
		r.Match.Methods[i] = strings.ToUpper(strings.TrimSpace(m))
	}
	if r.Name == "" {
		r.Name = r.Match.URL
		if r.Name == "" {
			r.Name = "All requests"
		}
	}
	switch r.Type {
	case TypeMapLocal:
		if _, err := parseMapLocal(r.Spec); err != nil {
			return err
		}
	case TypeBreakpoint:
		if _, err := parseBreakpoint(r.Spec); err != nil {
			return err
		}
	case TypeMapRemote:
		if _, err := parseMapRemote(r.Spec); err != nil {
			return err
		}
	case TypeRewrite:
		if _, err := parseRewrite(r.Spec, r.Phase); err != nil {
			return err
		}
	}
	return nil
}

// BreakpointSpec configures a breakpoint rule.
type BreakpointSpec struct {
	Timeout   string `json:"timeout,omitempty"`    // "60s" (default), max 30m
	OnTimeout string `json:"on_timeout,omitempty"` // continue (default) | abort
	timeout   time.Duration
}

func parseBreakpoint(raw json.RawMessage) (*BreakpointSpec, error) {
	var b BreakpointSpec
	if len(raw) > 0 && string(raw) != "null" {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&b); err != nil {
			return nil, invalid("breakpoint spec: %v", err)
		}
	}
	if b.Timeout != "" {
		d, err := time.ParseDuration(b.Timeout)
		if err != nil || d <= 0 || d > 30*time.Minute {
			return nil, invalid("timeout must be between 1s and 30m")
		}
		b.timeout = d
	}
	if b.OnTimeout == "" {
		b.OnTimeout = "continue"
	}
	if b.OnTimeout != "continue" && b.OnTimeout != "abort" {
		return nil, invalid("on_timeout must be continue or abort")
	}
	return &b, nil
}
