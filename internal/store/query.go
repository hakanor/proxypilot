package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Filter narrows List. Zero values mean "no constraint".
type Filter struct {
	SessionID    string
	Host         string   // exact or wildcard: api.example.com, *.example.com, *example*
	Methods      []string // GET, POST…
	Status       string   // "2xx", "4xx,5xx", ">=500", "404", "400-499"
	ContentType  string   // substring of the response content type
	Source       string
	HasError     *bool
	Text         string // substring of the URL
	BodySearch   bool   // also search inline bodies for Text (bounded)
	HeaderSearch bool   // search header names and values for Text instead of the URL
	States       []string
	// GraphQLOp matches an operation name recorded in tags.gql_op (also within batches).
	GraphQLOp string
	// IDs restricts to these flows (exports).
	IDs   []string
	Since time.Time
	Until time.Time
}

// Page is one List result.
type Page struct {
	Flows      []*Flow `json:"flows"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

// ClampLimit applies the default and the maximum.
func ClampLimit(n int) int {
	if n <= 0 {
		return DefaultLimit
	}
	if n > MaxLimit {
		return MaxLimit
	}
	return n
}

// List returns flows newest first. cursor comes from a previous Page.NextCursor.
func (s *Store) List(ctx context.Context, f Filter, limit int, cursor string) (Page, error) {
	limit = ClampLimit(limit)
	where, args, err := f.sql()
	if err != nil {
		return Page{}, err
	}
	if cursor != "" {
		ts, id, err := decodeCursor(cursor)
		if err != nil {
			return Page{}, err
		}
		where = append(where, `(created_at < ? OR (created_at = ? AND id < ?))`)
		args = append(args, ts, ts, id)
	}
	q := `SELECT ` + flowCols + ` FROM flows`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	var out Page
	for rows.Next() {
		fl, err := scanFlow(rows)
		if err != nil {
			return Page{}, err
		}
		out.Flows = append(out.Flows, fl)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	if len(out.Flows) > limit {
		out.Flows = out.Flows[:limit]
		last := out.Flows[limit-1]
		out.NextCursor = encodeCursor(last.CreatedAt.UnixNano(), last.ID)
	}
	if out.Flows == nil {
		out.Flows = []*Flow{}
	}
	return out, nil
}

// Search is a URL (and optionally inline body) text search.
func (s *Store) Search(ctx context.Context, query string, includeBodies bool, limit int) (Page, error) {
	return s.List(ctx, Filter{Text: query, BodySearch: includeBodies}, limit, "")
}

func encodeCursor(ts int64, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(ts, 10) + "|" + id))
}

func decodeCursor(c string) (int64, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, "", errors.New("invalid cursor")
	}
	ts, id, ok := strings.Cut(string(b), "|")
	if !ok {
		return 0, "", errors.New("invalid cursor")
	}
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return 0, "", errors.New("invalid cursor")
	}
	return n, id, nil
}

func (f Filter) sql() ([]string, []any, error) {
	var where []string
	var args []any
	if f.SessionID != "" {
		where, args = append(where, `session_id = ?`), append(args, f.SessionID)
	}
	if f.Host != "" {
		h := strings.ToLower(f.Host)
		if strings.Contains(h, "*") {
			where, args = append(where, `host LIKE ? ESCAPE '\'`), append(args, globToLike(h))
		} else {
			where, args = append(where, `host = ?`), append(args, h)
		}
	}
	if len(f.Methods) > 0 {
		ph := make([]string, len(f.Methods))
		for i, m := range f.Methods {
			ph[i] = "?"
			args = append(args, strings.ToUpper(m))
		}
		where = append(where, `method IN (`+strings.Join(ph, ",")+`)`)
	}
	if f.Status != "" {
		w, a, err := statusSQL(f.Status)
		if err != nil {
			return nil, nil, err
		}
		where, args = append(where, w), append(args, a...)
	}
	if f.ContentType != "" {
		where, args = append(where, `resp_content_type LIKE ? ESCAPE '\'`), append(args, "%"+likeEscape(f.ContentType)+"%")
	}
	if f.GraphQLOp != "" {
		where = append(where, `(',' || json_extract(tags, '$.gql_op') || ',') LIKE ? ESCAPE '\'`)
		args = append(args, "%,"+likeEscape(f.GraphQLOp)+",%")
	}
	if len(f.IDs) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(f.IDs)), ",")
		where = append(where, `id IN (`+ph+`)`)
		for _, id := range f.IDs {
			args = append(args, id)
		}
	}
	if f.Source != "" {
		where, args = append(where, `source = ?`), append(args, f.Source)
	}
	if f.HasError != nil {
		if *f.HasError {
			where = append(where, `error_kind != ''`)
		} else {
			where = append(where, `error_kind = ''`)
		}
	}
	if len(f.States) > 0 {
		ph := make([]string, len(f.States))
		for i, st := range f.States {
			ph[i] = "?"
			args = append(args, st)
		}
		where = append(where, `state IN (`+strings.Join(ph, ",")+`)`)
	}
	if f.Text != "" {
		pat := "%" + likeEscape(f.Text) + "%"
		if f.HeaderSearch {
			where = append(where, `id IN (SELECT flow_id FROM headers WHERE name LIKE ? ESCAPE '\' OR value LIKE ? ESCAPE '\')`)
			args = append(args, pat, pat)
		} else if f.BodySearch {
			// Inline bodies only, so the scan stays bounded by inline_body_limit.
			where = append(where, `(url LIKE ? ESCAPE '\' OR id IN (SELECT flow_id FROM bodies WHERE inline IS NOT NULL AND CAST(inline AS TEXT) LIKE ? ESCAPE '\'))`)
			args = append(args, pat, pat)
		} else {
			where, args = append(where, `url LIKE ? ESCAPE '\'`), append(args, pat)
		}
	}
	if !f.Since.IsZero() {
		where, args = append(where, `created_at >= ?`), append(args, f.Since.UnixNano())
	}
	if !f.Until.IsZero() {
		where, args = append(where, `created_at < ?`), append(args, f.Until.UnixNano())
	}
	return where, args, nil
}

func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func globToLike(g string) string {
	return strings.ReplaceAll(likeEscape(g), "*", "%")
}

// statusSQL parses "2xx", "4xx,5xx", ">=500", "<400", "404", "400-499".
func statusSQL(spec string) (string, []any, error) {
	var parts []string
	var args []any
	for _, raw := range strings.Split(spec, ",") {
		p := strings.ToLower(strings.TrimSpace(raw))
		if p == "" {
			continue
		}
		switch {
		case len(p) == 3 && p[1:] == "xx" && p[0] >= '1' && p[0] <= '5':
			lo := int(p[0]-'0') * 100
			parts, args = append(parts, `(status >= ? AND status < ?)`), append(args, lo, lo+100)
		case strings.HasPrefix(p, ">="), strings.HasPrefix(p, "<="):
			n, err := strconv.Atoi(p[2:])
			if err != nil {
				return "", nil, fmt.Errorf("bad status %q", raw)
			}
			parts, args = append(parts, `status `+p[:2]+` ?`), append(args, n)
		case strings.HasPrefix(p, ">"), strings.HasPrefix(p, "<"):
			n, err := strconv.Atoi(p[1:])
			if err != nil {
				return "", nil, fmt.Errorf("bad status %q", raw)
			}
			parts, args = append(parts, `status `+p[:1]+` ?`), append(args, n)
		case strings.Contains(p, "-"):
			a, b, _ := strings.Cut(p, "-")
			lo, err1 := strconv.Atoi(a)
			hi, err2 := strconv.Atoi(b)
			if err1 != nil || err2 != nil {
				return "", nil, fmt.Errorf("bad status %q", raw)
			}
			parts, args = append(parts, `(status >= ? AND status <= ?)`), append(args, lo, hi)
		default:
			n, err := strconv.Atoi(p)
			if err != nil {
				return "", nil, fmt.Errorf("bad status %q", raw)
			}
			parts, args = append(parts, `status = ?`), append(args, n)
		}
	}
	if len(parts) == 0 {
		return "", nil, fmt.Errorf("empty status filter")
	}
	return `(` + strings.Join(parts, ` OR `) + `)`, args, nil
}

// Summary aggregates traffic since a time (zero = everything).
type Summary struct {
	Total     int            `json:"total"`
	ByStatus  map[string]int `json:"by_status"` // 1xx…5xx, "none"
	Errors    int            `json:"errors"`
	ByError   map[string]int `json:"by_error_kind"`
	TopHosts  []HostCount    `json:"top_hosts"`
	BytesUp   int64          `json:"bytes_up"`
	BytesDown int64          `json:"bytes_down"`
	AvgMs     float64        `json:"avg_total_ms"`
	Failures  []FailureGroup `json:"failures"`
	Slowest   *SlowFlow      `json:"slowest,omitempty"`
	Since     time.Time      `json:"since,omitempty"`
}

// FailureGroup groups failing requests (status >= 400 or a transport error).
type FailureGroup struct {
	Method    string `json:"method"`
	Host      string `json:"host"`
	Path      string `json:"path"`
	Status    int    `json:"status,omitempty"`
	ErrorKind string `json:"error_kind,omitempty"`
	Count     int    `json:"count"`
	LastID    string `json:"last_id"`
}

type SlowFlow struct {
	ID      string  `json:"id"`
	Method  string  `json:"method"`
	URL     string  `json:"url"`
	TotalMs float64 `json:"total_ms"`
}

type HostCount struct {
	Host      string `json:"host"`
	Count     int    `json:"count"`
	BytesDown int64  `json:"bytes_down"`
	Errors    int    `json:"errors"`
}

func (s *Store) Summary(ctx context.Context, since time.Time) (Summary, error) {
	out := Summary{ByStatus: map[string]int{}, ByError: map[string]int{}, Since: since}
	ts := int64(0)
	if !since.IsZero() {
		ts = since.UnixNano()
	}
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(bytes_up),0), COALESCE(SUM(bytes_down),0),
		COALESCE(AVG(CAST(json_extract(timing,'$.total_ms') AS REAL)),0), COALESCE(SUM(error_kind != ''),0)
		FROM flows WHERE created_at >= ?`, ts).Scan(&out.Total, &out.BytesUp, &out.BytesDown, &out.AvgMs, &out.Errors)
	if err != nil {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT CASE WHEN status = 0 THEN 'none' ELSE (status/100) || 'xx' END, COUNT(*)
		FROM flows WHERE created_at >= ? GROUP BY 1 LIMIT 10`, ts)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			rows.Close()
			return out, err
		}
		out.ByStatus[k] = n
	}
	rows.Close()
	rows, err = s.db.QueryContext(ctx, `SELECT error_kind, COUNT(*) FROM flows WHERE created_at >= ? AND error_kind != '' GROUP BY 1 ORDER BY 2 DESC LIMIT 20`, ts)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			rows.Close()
			return out, err
		}
		out.ByError[k] = n
	}
	rows.Close()
	rows, err = s.db.QueryContext(ctx, `SELECT host, COUNT(*), COALESCE(SUM(bytes_down),0), COALESCE(SUM(error_kind != ''),0)
		FROM flows WHERE created_at >= ? GROUP BY host ORDER BY 2 DESC LIMIT 10`, ts)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var h HostCount
		if err := rows.Scan(&h.Host, &h.Count, &h.BytesDown, &h.Errors); err != nil {
			return out, err
		}
		out.TopHosts = append(out.TopHosts, h)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	rows.Close()

	frows, err := s.db.QueryContext(ctx, `SELECT method, host, path, status, error_kind, COUNT(*), MAX(id)
		FROM flows WHERE created_at >= ? AND (status >= 400 OR error_kind != '')
		GROUP BY method, host, path, status, error_kind ORDER BY COUNT(*) DESC, MAX(created_at) DESC LIMIT 10`, ts)
	if err != nil {
		return out, err
	}
	defer frows.Close()
	for frows.Next() {
		var g FailureGroup
		if err := frows.Scan(&g.Method, &g.Host, &g.Path, &g.Status, &g.ErrorKind, &g.Count, &g.LastID); err != nil {
			return out, err
		}
		out.Failures = append(out.Failures, g)
	}
	if err := frows.Err(); err != nil {
		return out, err
	}

	var sl SlowFlow
	err = s.db.QueryRowContext(ctx, `SELECT id, method, url, CAST(json_extract(timing,'$.total_ms') AS REAL) AS t
		FROM flows WHERE created_at >= ? AND state = 'completed' ORDER BY t DESC LIMIT 1`, ts).Scan(&sl.ID, &sl.Method, &sl.URL, &sl.TotalMs)
	if err == nil {
		out.Slowest = &sl
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	return out, nil
}

// MatchStatus compiles the same status grammar as Filter.Status for in-memory
// use (live stream filters).
func MatchStatus(spec string) (func(int) bool, error) {
	type rng struct{ lo, hi int }
	var rs []rng
	for _, raw := range strings.Split(spec, ",") {
		p := strings.ToLower(strings.TrimSpace(raw))
		if p == "" {
			continue
		}
		bad := fmt.Errorf("bad status %q", raw)
		num := func(s string) (int, error) {
			n, err := strconv.Atoi(s)
			if err != nil {
				return 0, bad
			}
			return n, nil
		}
		switch {
		case len(p) == 3 && p[1:] == "xx" && p[0] >= '1' && p[0] <= '5':
			lo := int(p[0]-'0') * 100
			rs = append(rs, rng{lo, lo + 99})
		case strings.HasPrefix(p, ">="):
			n, err := num(p[2:])
			if err != nil {
				return nil, err
			}
			rs = append(rs, rng{n, 1 << 30})
		case strings.HasPrefix(p, "<="):
			n, err := num(p[2:])
			if err != nil {
				return nil, err
			}
			rs = append(rs, rng{-1 << 30, n})
		case strings.HasPrefix(p, ">"):
			n, err := num(p[1:])
			if err != nil {
				return nil, err
			}
			rs = append(rs, rng{n + 1, 1 << 30})
		case strings.HasPrefix(p, "<"):
			n, err := num(p[1:])
			if err != nil {
				return nil, err
			}
			rs = append(rs, rng{-1 << 30, n - 1})
		case strings.Contains(p, "-"):
			a, b, _ := strings.Cut(p, "-")
			lo, err1 := num(a)
			hi, err2 := num(b)
			if err1 != nil || err2 != nil {
				return nil, bad
			}
			rs = append(rs, rng{lo, hi})
		default:
			n, err := num(p)
			if err != nil {
				return nil, err
			}
			rs = append(rs, rng{n, n})
		}
	}
	if len(rs) == 0 {
		return nil, fmt.Errorf("empty status filter")
	}
	return func(s int) bool {
		for _, r := range rs {
			if s >= r.lo && s <= r.hi {
				return true
			}
		}
		return false
	}, nil
}

// MatchHost compiles the Filter.Host grammar (exact or * wildcard).
func MatchHost(pattern string) func(string) bool {
	p := strings.ToLower(pattern)
	if !strings.Contains(p, "*") {
		return func(h string) bool { return strings.ToLower(h) == p }
	}
	parts := strings.Split(p, "*")
	return func(h string) bool {
		h = strings.ToLower(h)
		if !strings.HasPrefix(h, parts[0]) {
			return false
		}
		h = h[len(parts[0]):]
		for i, part := range parts[1:] {
			last := i == len(parts)-2
			if last {
				return strings.HasSuffix(h, part)
			}
			j := strings.Index(h, part)
			if j < 0 {
				return false
			}
			h = h[j+len(part):]
		}
		return true
	}
}
