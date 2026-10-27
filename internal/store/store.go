package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Options struct {
	MaxEntries      int   // retention bound (default 10000)
	MaxBytes        int64 // stored body + WebSocket bytes bound (0 = unlimited)
	InlineBodyLimit int64 // bodies up to this size live in SQLite (default 256 KB)
	MaxBodySize     int64 // decoded bodies are truncated beyond this (default 64 MB)
}

type Store struct {
	db      *sql.DB
	dir     string
	bodyDir string
	opts    Options

	mu      sync.RWMutex
	session string
}

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id         TEXT PRIMARY KEY,
	started_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS flows (
	id                TEXT PRIMARY KEY,
	session_id        TEXT NOT NULL,
	source            TEXT NOT NULL,
	client_addr       TEXT NOT NULL DEFAULT '',
	client_label      TEXT NOT NULL DEFAULT '',
	method            TEXT NOT NULL DEFAULT '',
	scheme            TEXT NOT NULL DEFAULT '',
	host              TEXT NOT NULL DEFAULT '',
	port              INTEGER NOT NULL DEFAULT 0,
	path              TEXT NOT NULL DEFAULT '',
	query             TEXT NOT NULL DEFAULT '',
	url               TEXT NOT NULL DEFAULT '',
	status            INTEGER NOT NULL DEFAULT 0,
	req_content_type  TEXT NOT NULL DEFAULT '',
	resp_content_type TEXT NOT NULL DEFAULT '',
	req_size          INTEGER NOT NULL DEFAULT 0,
	resp_size         INTEGER NOT NULL DEFAULT 0,
	bytes_up          INTEGER NOT NULL DEFAULT 0,
	bytes_down        INTEGER NOT NULL DEFAULT 0,
	protocol          TEXT NOT NULL DEFAULT '',
	state             TEXT NOT NULL,
	error_kind        TEXT NOT NULL DEFAULT '',
	error_msg         TEXT NOT NULL DEFAULT '',
	timing            TEXT NOT NULL DEFAULT '{}',
	tags              TEXT NOT NULL DEFAULT '{}',
	matched_rules     TEXT NOT NULL DEFAULT '[]',
	created_at        INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS flows_created ON flows(created_at);
CREATE INDEX IF NOT EXISTS flows_host    ON flows(host);
CREATE INDEX IF NOT EXISTS flows_status  ON flows(status);
CREATE INDEX IF NOT EXISTS flows_session ON flows(session_id);
CREATE TABLE IF NOT EXISTS headers (
	flow_id TEXT NOT NULL,
	side    TEXT NOT NULL,
	seq     INTEGER NOT NULL,
	name    TEXT NOT NULL,
	value   TEXT NOT NULL,
	PRIMARY KEY (flow_id, side, seq)
);
CREATE TABLE IF NOT EXISTS bodies (
	flow_id      TEXT NOT NULL,
	side         TEXT NOT NULL,
	encoding     TEXT NOT NULL DEFAULT '',
	inline       BLOB,
	file_path    TEXT NOT NULL DEFAULT '',
	raw_size     INTEGER NOT NULL DEFAULT 0,
	decoded_size INTEGER NOT NULL DEFAULT 0,
	truncated    INTEGER NOT NULL DEFAULT 0,
	decode_error TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (flow_id, side)
);
`

// Open creates or opens <dir>/proxypilot.db and <dir>/bodies/.
func Open(dir string, opts Options) (*Store, error) {
	if opts.MaxEntries <= 0 {
		opts.MaxEntries = 10000
	}
	if opts.InlineBodyLimit <= 0 {
		opts.InlineBodyLimit = 256 * 1024
	}
	if opts.MaxBodySize <= 0 {
		opts.MaxBodySize = 64 << 20
	}
	bodyDir := filepath.Join(dir, "bodies")
	if err := os.MkdirAll(bodyDir, 0o700); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.Join(dir, "proxypilot.db") +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schema + messagesSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	s := &Store{db: db, dir: dir, bodyDir: bodyDir, opts: opts}
	if err := s.loadOrStartSession(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for read-only tooling (status).
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) loadOrStartSession(ctx context.Context) error {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM sessions ORDER BY started_at DESC, id DESC LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.NewSession(ctx)
		return err
	}
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.session = id
	s.mu.Unlock()
	return nil
}

// NewSession starts a fresh session; new flows belong to it.
func (s *Store) NewSession(ctx context.Context) (Session, error) {
	sess := Session{ID: ulid.Make().String(), StartedAt: time.Now()}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO sessions(id, started_at) VALUES(?, ?)`, sess.ID, sess.StartedAt.UnixNano()); err != nil {
		return sess, err
	}
	s.mu.Lock()
	s.session = sess.ID
	s.mu.Unlock()
	return sess, nil
}

func (s *Store) CurrentSession() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.session
}

const flowCols = `id, session_id, source, client_addr, client_label, method, scheme, host, port, path, query, url,
	status, req_content_type, resp_content_type, req_size, resp_size, bytes_up, bytes_down, protocol, state,
	error_kind, error_msg, timing, tags, matched_rules, created_at`

func flowArgs(f *Flow) ([]any, error) {
	timing, err := json.Marshal(f.Timing)
	if err != nil {
		return nil, err
	}
	tags := []byte("{}")
	if len(f.Tags) > 0 {
		if tags, err = json.Marshal(f.Tags); err != nil {
			return nil, err
		}
	}
	rules := []byte("[]")
	if len(f.MatchedRules) > 0 {
		if rules, err = json.Marshal(f.MatchedRules); err != nil {
			return nil, err
		}
	}
	return []any{f.ID, f.SessionID, f.Source, f.ClientAddr, f.ClientLabel, f.Method, f.Scheme, f.Host, f.Port, f.Path, f.Query, f.URL,
		f.Status, f.ReqContentType, f.RespContentType, f.ReqSize, f.RespSize, f.BytesUp, f.BytesDown, f.Protocol, f.State,
		f.ErrorKind, f.ErrorMsg, string(timing), string(tags), string(rules), f.CreatedAt.UnixNano()}, nil
}

// Insert stores a new flow. Missing ID, session, source, state and time are filled in.
func (s *Store) Insert(ctx context.Context, f *Flow) error {
	if f.ID == "" {
		f.ID = ulid.Make().String()
	}
	if f.SessionID == "" {
		f.SessionID = s.CurrentSession()
	}
	if f.Source == "" {
		f.Source = SourceProxy
	}
	if f.State == "" {
		f.State = StatePending
	}
	if f.CreatedAt.IsZero() {
		f.CreatedAt = time.Now()
	}
	args, err := flowArgs(f)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ph := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")
	if _, err := tx.ExecContext(ctx, `INSERT INTO flows(`+flowCols+`) VALUES(`+ph+`)`, args...); err != nil {
		return err
	}
	if err := writeHeaders(ctx, tx, f); err != nil {
		return err
	}
	return tx.Commit()
}

// Update rewrites every column of an existing flow and replaces non-nil header sets.
func (s *Store) Update(ctx context.Context, f *Flow) error {
	args, err := flowArgs(f)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cols := strings.Split(strings.Join(strings.Fields(flowCols), ""), ",")
	sets := make([]string, 0, len(cols)-1)
	for _, c := range cols[1:] {
		sets = append(sets, c+"=?")
	}
	res, err := tx.ExecContext(ctx, `UPDATE flows SET `+strings.Join(sets, ",")+` WHERE id=?`, append(args[1:], f.ID)...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := writeHeaders(ctx, tx, f); err != nil {
		return err
	}
	return tx.Commit()
}

// writeHeaders replaces the header sets that are non-nil on f.
func writeHeaders(ctx context.Context, tx *sql.Tx, f *Flow) error {
	for _, side := range []struct {
		name string
		hs   []Header
	}{{SideReq, f.ReqHeaders}, {SideResp, f.RespHeaders}} {
		if side.hs == nil {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM headers WHERE flow_id=? AND side=?`, f.ID, side.name); err != nil {
			return err
		}
		for i, h := range side.hs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO headers(flow_id, side, seq, name, value) VALUES(?,?,?,?,?)`, f.ID, side.name, i, h.Name, h.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

type scanner interface{ Scan(dest ...any) error }

func scanFlow(row scanner) (*Flow, error) {
	var f Flow
	var timing, tags, rules string
	var created int64
	err := row.Scan(&f.ID, &f.SessionID, &f.Source, &f.ClientAddr, &f.ClientLabel, &f.Method, &f.Scheme, &f.Host, &f.Port, &f.Path, &f.Query, &f.URL,
		&f.Status, &f.ReqContentType, &f.RespContentType, &f.ReqSize, &f.RespSize, &f.BytesUp, &f.BytesDown, &f.Protocol, &f.State,
		&f.ErrorKind, &f.ErrorMsg, &timing, &tags, &rules, &created)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(timing), &f.Timing)
	_ = json.Unmarshal([]byte(tags), &f.Tags)
	_ = json.Unmarshal([]byte(rules), &f.MatchedRules)
	f.CreatedAt = time.Unix(0, created)
	return &f, nil
}

// Get returns a flow with headers.
func (s *Store) Get(ctx context.Context, id string) (*Flow, error) {
	f, err := scanFlow(s.db.QueryRowContext(ctx, `SELECT `+flowCols+` FROM flows WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT side, name, value FROM headers WHERE flow_id=? ORDER BY side, seq LIMIT 2000`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var side string
		var h Header
		if err := rows.Scan(&side, &h.Name, &h.Value); err != nil {
			return nil, err
		}
		if side == SideReq {
			f.ReqHeaders = append(f.ReqHeaders, h)
		} else {
			f.RespHeaders = append(f.RespHeaders, h)
		}
	}
	return f, rows.Err()
}

// Count returns the number of stored flows.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM flows`).Scan(&n)
	return n, err
}

// Delete removes flows, their headers, bodies and body files.
func (s *Store) Delete(ctx context.Context, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return s.deleteWhere(ctx, `id IN (`+ph+`)`, args...)
}

// Clear deletes every flow of session ("" = all sessions) and starts a new session.
func (s *Store) Clear(ctx context.Context, session string) (Session, error) {
	var err error
	if session == "" {
		_, err = s.deleteWhere(ctx, `1=1`)
	} else {
		_, err = s.deleteWhere(ctx, `session_id=?`, session)
	}
	if err != nil {
		return Session{}, err
	}
	return s.NewSession(ctx)
}

func (s *Store) deleteWhere(ctx context.Context, where string, args ...any) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	sub := `SELECT id FROM flows WHERE ` + where
	rows, err := tx.QueryContext(ctx, `SELECT file_path FROM bodies WHERE file_path != '' AND flow_id IN (`+sub+`)`, args...)
	if err != nil {
		return 0, err
	}
	var files []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return 0, err
		}
		files = append(files, p)
	}
	rows.Close()
	if _, err := tx.ExecContext(ctx, `DELETE FROM headers WHERE flow_id IN (`+sub+`)`, args...); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM bodies WHERE flow_id IN (`+sub+`)`, args...); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM ws_messages WHERE flow_id IN (`+sub+`)`, args...); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM flows WHERE `+where, args...)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	for _, p := range files {
		_ = os.Remove(p)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// AddSession records a session without making it current (imports).
func (s *Store) AddSession(ctx context.Context) (Session, error) {
	sess := Session{ID: ulid.Make().String(), StartedAt: time.Now()}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(id, started_at) VALUES(?, ?)`, sess.ID, sess.StartedAt.UnixNano())
	return sess, err
}
