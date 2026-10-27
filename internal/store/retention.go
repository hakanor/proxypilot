package store

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"
)

// EnforceRetention deletes the oldest flows (and their body files) beyond
// MaxEntries, then beyond MaxBytes of stored bodies and WebSocket messages.
func (s *Store) EnforceRetention(ctx context.Context) (int, error) {
	n, err := s.Count(ctx)
	if err != nil {
		return 0, err
	}
	removed := 0
	if n > s.opts.MaxEntries {
		if removed, err = s.deleteWhere(ctx, `id IN (SELECT id FROM flows ORDER BY created_at ASC, id ASC LIMIT ?)`, n-s.opts.MaxEntries); err != nil {
			return removed, err
		}
	}
	if s.opts.MaxBytes <= 0 {
		return removed, nil
	}
	// Newest first with a running total; the first flow over the limit and everything older goes.
	var createdAt int64
	var id string
	err = s.db.QueryRowContext(ctx, `SELECT created_at, id FROM (
		SELECT f.id, f.created_at, SUM(COALESCE(b.sz, 0) + COALESCE(m.sz, 0)) OVER (ORDER BY f.created_at DESC, f.id DESC) AS run
		FROM flows f
		LEFT JOIN (SELECT flow_id, SUM(decoded_size) AS sz FROM bodies GROUP BY flow_id) b ON b.flow_id = f.id
		LEFT JOIN (SELECT flow_id, SUM(size) AS sz FROM ws_messages GROUP BY flow_id) m ON m.flow_id = f.id
	) WHERE run > ? ORDER BY created_at DESC, id DESC LIMIT 1`, s.opts.MaxBytes).Scan(&createdAt, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return removed, nil
	}
	if err != nil {
		return removed, err
	}
	m, err := s.deleteWhere(ctx, `created_at < ? OR (created_at = ? AND id <= ?)`, createdAt, createdAt, id)
	return removed + m, err
}

// RunRetention enforces retention every interval until ctx is done.
func (s *Store) RunRetention(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := s.EnforceRetention(ctx); err != nil {
				slog.Warn("retention failed", "err", err)
			} else if n > 0 {
				slog.Debug("retention removed flows", "count", n)
			}
		}
	}
}
