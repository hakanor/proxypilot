package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

// Popular counts what the user works on (opens a flow, pins it, adds a rule for it) per
// host and path, so agents can look at those hosts first. It is kept across Clear.

// PopularPath is one endpoint under a popular host.
type PopularPath struct {
	Path string `json:"path"`
	Hits int    `json:"hits"`
}

// PopularHost groups the endpoints of one host; Hits is their sum.
type PopularHost struct {
	Host  string        `json:"host"`
	Hits  int           `json:"hits"`
	Paths []PopularPath `json:"paths"`
}

// BumpPopular adds one use of host+path. The path is stored without its query.
func (s *Store) BumpPopular(ctx context.Context, host, path string) error {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return nil
	}
	path, _, _ = strings.Cut(path, "?")
	if path == "" {
		path = "/"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO popular(host, path, hits, last_at) VALUES(?, ?, 1, ?)
		ON CONFLICT(host, path) DO UPDATE SET hits = hits + 1, last_at = excluded.last_at`,
		host, path, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// BumpPopularFlow bumps the host and path of a stored flow.
func (s *Store) BumpPopularFlow(ctx context.Context, id string) error {
	var host, path string
	err := s.db.QueryRowContext(ctx, `SELECT host, path FROM flows WHERE id=?`, id).Scan(&host, &path)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return s.BumpPopular(ctx, host, path)
}

// Popular returns the most used hosts (up to limit), each with its top paths (up to perHost).
func (s *Store) Popular(ctx context.Context, limit, perHost int) ([]PopularHost, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT host, path, hits FROM popular`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byHost := map[string]*PopularHost{}
	for rows.Next() {
		var p PopularPath
		var host string
		if err := rows.Scan(&host, &p.Path, &p.Hits); err != nil {
			return nil, err
		}
		h := byHost[host]
		if h == nil {
			h = &PopularHost{Host: host}
			byHost[host] = h
		}
		h.Hits += p.Hits
		h.Paths = append(h.Paths, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]PopularHost, 0, len(byHost))
	for _, h := range byHost {
		sort.Slice(h.Paths, func(i, j int) bool {
			if h.Paths[i].Hits != h.Paths[j].Hits {
				return h.Paths[i].Hits > h.Paths[j].Hits
			}
			return h.Paths[i].Path < h.Paths[j].Path
		})
		if perHost > 0 && len(h.Paths) > perHost {
			h.Paths = h.Paths[:perHost]
		}
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Hits != out[j].Hits {
			return out[i].Hits > out[j].Hits
		}
		return out[i].Host < out[j].Host
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ClearPopular forgets every count.
func (s *Store) ClearPopular(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM popular`)
	return err
}

// PinnedHosts lists the hosts of pinned flows.
func (s *Store) PinnedHosts(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT host FROM flows WHERE id IN (SELECT flow_id FROM pins) AND host != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hosts []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		hosts = append(hosts, h)
	}
	return hosts, rows.Err()
}
