package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/hakanor/proxypilot/internal/proxy"
)

// sslPolicyFile keeps SSL mode and lists changed in the UI or API across
// restarts; when present it wins over the ssl section of config.yaml.
const sslPolicyFile = "ssl-policy.json"

func (d *Daemon) saveSSLPolicy(p proxy.PolicySnapshot) {
	p.Learned, p.FavoriteHosts = nil, nil // per run
	b, err := json.Marshal(p)
	if err == nil {
		err = os.WriteFile(filepath.Join(d.Config.DataDir, sslPolicyFile), b, 0o600)
	}
	if err != nil {
		d.Log.Warn("save ssl policy", "err", err)
	}
}

// loadSSLPolicy returns the saved policy, or ok=false when there is none (or it is unreadable).
func loadSSLPolicy(dir string) (p proxy.PolicySnapshot, ok bool) {
	b, err := os.ReadFile(filepath.Join(dir, sslPolicyFile))
	if err != nil || json.Unmarshal(b, &p) != nil {
		return p, false
	}
	if _, err := proxy.ParseSSLMode(string(p.Mode)); err != nil {
		return p, false
	}
	return p, true
}

// refreshFavoriteHosts keeps the SSL allowlist mode's popular+pinned hosts current.
func (d *Daemon) refreshFavoriteHosts(ctx context.Context, every time.Duration) {
	for {
		var hosts []string
		if pop, err := d.Store.Popular(ctx, 20, 1); err == nil {
			for _, h := range pop {
				hosts = append(hosts, h.Host)
			}
		}
		if pinned, err := d.Store.PinnedHosts(ctx); err == nil {
			hosts = append(hosts, pinned...)
		}
		d.Policy.SetFavoriteHosts(hosts)
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
