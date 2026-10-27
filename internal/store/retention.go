package store

import (
	"context"
	"log/slog"
	"time"
)

// EnforceRetention deletes the oldest flows (and their body files) beyond MaxEntries.
func (s *Store) EnforceRetention(ctx context.Context) (int, error) {
	n, err := s.Count(ctx)
	if err != nil || n <= s.opts.MaxEntries {
		return 0, err
	}
	excess := n - s.opts.MaxEntries
	return s.deleteWhere(ctx, `id IN (SELECT id FROM flows ORDER BY created_at ASC, id ASC LIMIT ?)`, excess)
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
