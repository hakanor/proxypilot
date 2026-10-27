package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hakanor/proxypilot/internal/devices/execx"
	"github.com/hakanor/proxypilot/internal/proxy"
	"github.com/hakanor/proxypilot/internal/store"
)

const (
	defaultBodyMax = 4096
	maxBodyMax     = 10 << 20
)

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/status", s.handleStatus)
	mux.HandleFunc("GET /api/v1/flows", s.handleListFlows)
	mux.HandleFunc("DELETE /api/v1/flows", s.handleClearFlows)
	mux.HandleFunc("GET /api/v1/flows/{id}", s.handleGetFlow)
	mux.HandleFunc("GET /api/v1/flows/{id}/body/{side}", s.handleBody)
	mux.HandleFunc("GET /api/v1/flows/{id}/messages", s.handleMessages)
	mux.HandleFunc("GET /api/v1/summary", s.handleSummary)
	mux.HandleFunc("GET /api/v1/ssl-policy", s.handleGetPolicy)
	mux.HandleFunc("PUT /api/v1/ssl-policy", s.handlePutPolicy)
	mux.HandleFunc("DELETE /api/v1/ssl-policy/learned/{host}", s.handleForgetTunnel)
	mux.HandleFunc("GET /api/v1/ca", s.handleCA)
	mux.HandleFunc("GET /api/v1/ca/cert", s.handleCACert)
	mux.HandleFunc("PUT /api/v1/recording", s.handlePutRecording)
	mux.HandleFunc("PUT /api/v1/no-caching", s.handlePutNoCaching)
	mux.HandleFunc("GET /api/v1/app-icons/{app}", s.handleAppIcon)
	s.deviceRoutes(mux)
	s.ruleRoutes(mux)
	s.breakpointRoutes(mux)
	s.deviceSetupRoutes(mux)
	s.throttleRoutes(mux)
	s.androidRoutes(mux)
	s.repeatRoutes(mux)
	s.chromeRoutes(mux)
	s.archiveRoutes(mux)
	mux.HandleFunc("GET /api/v1/stream", s.handleStreamWS)
	mux.HandleFunc("GET /api/v1/stream/ndjson", s.handleStreamNDJSON)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "unknown endpoint")
	})

	mux.Handle("/", s.uiHandler())
}

// uiHandler serves the embedded web UI with SPA fallback to index.html.
func (s *Server) uiHandler() http.Handler {
	files := WebFS()
	fileServer := http.FileServer(http.FS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(files, p); err != nil {
				r = r.Clone(r.Context())
				r.URL.Path = "/"
			}
		}
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self' ws://127.0.0.1:* ws://localhost:* ws://[::1]:*; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'")
		fileServer.ServeHTTP(w, r)
	})
}

func wantRedact(r *http.Request) bool {
	v, err := strconv.ParseBool(r.URL.Query().Get("redact"))
	return err == nil && v
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	st := s.deps.Status()
	st.Recording = s.deps.Recorder == nil || s.deps.Recorder.Recording()
	writeJSON(w, http.StatusOK, st)
}

// handlePutRecording: {"recording": false} pauses capture, true resumes it.
func (s *Server) handlePutRecording(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Recording *bool `json:"recording"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&body); err != nil || body.Recording == nil {
		writeError(w, http.StatusBadRequest, "bad_request", `expected {"recording": true|false}`)
		return
	}
	if s.deps.Recorder == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "recording control unavailable")
		return
	}
	s.deps.Recorder.SetRecording(*body.Recording)
	writeJSON(w, http.StatusOK, map[string]bool{"recording": *body.Recording})
}

// handlePutNoCaching: {"enabled": true} strips cache headers so every request reaches
// the server; false lets clients cache again.
func (s *Server) handlePutNoCaching(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&body); err != nil || body.Enabled == nil {
		writeError(w, http.StatusBadRequest, "bad_request", `expected {"enabled": true|false}`)
		return
	}
	if s.deps.NoCache == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "no caching unavailable")
		return
	}
	s.deps.NoCache.SetEnabled(*body.Enabled)
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": *body.Enabled})
}

// parseFilter maps query parameters onto store.Filter.
func parseFilter(r *http.Request) (store.Filter, error) {
	q := r.URL.Query()
	f := store.Filter{
		SessionID:   q.Get("session"),
		Host:        q.Get("host"),
		Status:      q.Get("status"),
		ContentType: q.Get("type"),
		GraphQLOp:   q.Get("gql_op"),
		IDs:         splitList(q.Get("ids")),
		Source:      q.Get("source"),
		Text:        q.Get("q"),
	}
	if m := q.Get("method"); m != "" {
		f.Methods = splitList(m)
	}
	if st := q.Get("state"); st != "" {
		f.States = splitList(st)
	}
	if v := q.Get("has_error"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return f, errors.New("has_error must be true or false")
		}
		f.HasError = &b
	}
	if v := q.Get("body"); v != "" {
		f.BodySearch, _ = strconv.ParseBool(v)
	}
	switch q.Get("in") {
	case "", "url":
	case "body":
		f.BodySearch = true
	case "headers":
		f.HeaderSearch = true
	default:
		return f, errors.New("in must be url, body or headers")
	}
	var err error
	if f.Since, err = parseTime(q.Get("since")); err != nil {
		return f, err
	}
	if f.Until, err = parseTime(q.Get("until")); err != nil {
		return f, err
	}
	return f, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseTime accepts "", a duration ago ("5m", "2h") or RFC 3339.
func parseTime(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(v); err == nil {
		return time.Now().Add(-d), nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, errors.New("time must be a duration like 5m or RFC 3339")
	}
	return t, nil
}

type flowPage struct {
	Flows      []*store.Flow `json:"flows"`
	NextCursor string        `json:"next_cursor,omitempty"`
	HasMore    bool          `json:"has_more"`
	Limit      int           `json:"limit"`
}

func (s *Server) handleListFlows(w http.ResponseWriter, r *http.Request) {
	f, err := parseFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	limit = store.ClampLimit(limit)
	page, err := s.deps.Store.List(r.Context(), f, limit, r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if wantRedact(r) {
		for i, fl := range page.Flows {
			page.Flows[i] = s.redactor().Flow(fl)
		}
	}
	writeJSON(w, http.StatusOK, flowPage{Flows: page.Flows, NextCursor: page.NextCursor, HasMore: page.NextCursor != "", Limit: limit})
}

func (s *Server) handleClearFlows(w http.ResponseWriter, r *http.Request) {
	sess, err := s.deps.Store.Clear(r.Context(), r.URL.Query().Get("session"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cleared": true, "session": sess})
}

func (s *Server) handleGetFlow(w http.ResponseWriter, r *http.Request) {
	f, err := s.deps.Store.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "flow not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if wantRedact(r) {
		f = s.redactor().Flow(f)
	}
	type bodies struct {
		Req  *store.BodyInfo `json:"req,omitempty"`
		Resp *store.BodyInfo `json:"resp,omitempty"`
	}
	var b bodies
	if info, err := s.deps.Store.BodyInfoFor(r.Context(), f.ID, store.SideReq); err == nil {
		b.Req = &info
	}
	if info, err := s.deps.Store.BodyInfoFor(r.Context(), f.ID, store.SideResp); err == nil {
		b.Resp = &info
	}
	writeJSON(w, http.StatusOK, struct {
		*store.Flow
		Bodies bodies `json:"bodies"`
	}{f, b})
}

// BodyResponse is GET /flows/{id}/body/{side}.
type BodyResponse struct {
	FlowID      string `json:"flow_id"`
	Side        string `json:"side"`
	Size        int64  `json:"size"`
	Returned    int    `json:"returned"`
	Truncated   bool   `json:"truncated"`
	ContentType string `json:"content_type"`
	Encoding    string `json:"encoding"` // utf8 | base64
	Data        string `json:"data"`
	// ContentEncoding is the original Content-Encoding; Data is always decoded.
	ContentEncoding string `json:"content_encoding,omitempty"`
}

func (s *Server) handleBody(w http.ResponseWriter, r *http.Request) {
	id, side := r.PathValue("id"), r.PathValue("side")
	if side != store.SideReq && side != store.SideResp {
		writeError(w, http.StatusBadRequest, "bad_request", "side must be req or resp")
		return
	}
	f, err := s.deps.Store.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "flow not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	ct := f.RespContentType
	if side == store.SideReq {
		ct = f.ReqContentType
	}
	q := r.URL.Query()

	if raw, _ := strconv.ParseBool(q.Get("raw")); raw {
		rc, info, err := s.deps.Store.OpenBody(r.Context(), id, side)
		if errors.Is(err, store.ErrNotFound) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		defer rc.Close()
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Content-Length", strconv.FormatInt(info.DecodedSize, 10))
		w.Header().Set("Content-Disposition", "attachment")
		_, _ = io.Copy(w, rc)
		return
	}

	max := int64(defaultBodyMax)
	if v := q.Get("max"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "max must be a positive integer")
			return
		}
		max = min(n, maxBodyMax)
	}
	resp := BodyResponse{FlowID: id, Side: side, ContentType: ct, Encoding: "utf8"}
	data, truncated, info, err := s.deps.Store.GetBody(r.Context(), id, side, max)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, resp) // flow exists, body is empty
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if wantRedact(r) {
		data = s.redactor().Body(data, ct)
	}
	resp.Size, resp.Returned, resp.Truncated, resp.ContentEncoding = info.DecodedSize, len(data), truncated, info.Encoding
	if utf8.Valid(data) {
		resp.Data = string(data)
	} else {
		resp.Encoding, resp.Data = "base64", base64.StdEncoding.EncodeToString(data)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	since, err := parseTime(r.URL.Query().Get("since"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	sum, err := s.deps.Store.Summary(r.Context(), since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if wantRedact(r) && sum.Slowest != nil {
		sum.Slowest.URL = s.redactor().URL(sum.Slowest.URL)
	}
	writeJSON(w, http.StatusOK, sum)
}

func (s *Server) handleGetPolicy(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.deps.Policy.Snapshot())
}

type policyUpdate struct {
	Mode      string   `json:"mode"`
	Allowlist []string `json:"allowlist"`
	Denylist  []string `json:"denylist"`
}

func (s *Server) handlePutPolicy(w http.ResponseWriter, r *http.Request) {
	var u policyUpdate
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&u); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON: "+err.Error())
		return
	}
	mode, err := proxy.ParseSSLMode(u.Mode)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.deps.Policy.Set(mode, u.Allowlist, u.Denylist)
	snap := s.deps.Policy.Snapshot()
	if s.deps.SavePolicy != nil {
		s.deps.SavePolicy(snap)
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) handleForgetTunnel(w http.ResponseWriter, r *http.Request) {
	s.deps.Policy.ForgetTunnel(r.PathValue("host"))
	writeJSON(w, http.StatusOK, s.deps.Policy.Snapshot())
}

func (s *Server) handleCA(w http.ResponseWriter, _ *http.Request) {
	c := s.deps.CA.Certificate()
	writeJSON(w, http.StatusOK, map[string]any{
		"common_name":        c.Subject.CommonName,
		"fingerprint_sha256": s.deps.CA.Fingerprint(),
		"not_before":         c.NotBefore,
		"not_after":          c.NotAfter,
		"cert_path":          s.deps.CA.CertPath(),
		"trust":              s.deps.CA.TrustStatus(),
		"trust_command":      "sudo " + execx.Quote(s.deps.CA.TrustCommand()[0], s.deps.CA.TrustCommand()[1:]...),
	})
}

func (s *Server) handleCACert(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("format") == "der" {
		w.Header().Set("Content-Type", "application/x-x509-ca-cert")
		w.Header().Set("Content-Disposition", `attachment; filename="proxypilot-ca.crt"`)
		w.Write(s.deps.CA.ExportDER())
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="proxypilot-ca.pem"`)
	w.Write(s.deps.CA.ExportPEM())
}

// handleAppIcon serves the icon of an app seen behind the proxy (sidebar).
func (s *Server) handleAppIcon(w http.ResponseWriter, r *http.Request) {
	if s.deps.AppIcon == nil {
		writeError(w, http.StatusNotFound, "no_icon", "no icon")
		return
	}
	b, err := s.deps.AppIcon(r.PathValue("app"))
	if err != nil {
		writeError(w, http.StatusNotFound, "no_icon", "no icon for this app")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(b)
}
