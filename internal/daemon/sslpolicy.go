package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/hakanor/proxypilot/internal/proxy"
)

// sslPolicyFile keeps the SSL lists changed in the UI or API across
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
	return p, true // an old file's "mode" is ignored: only allowlisted hosts are decrypted
}

// legacyMode is the "mode" an ssl-policy.json from 0.1.11 or older still carries ("" when none).
func legacyMode(dir string) string {
	var old struct {
		Mode string `json:"mode"`
	}
	if b, err := os.ReadFile(filepath.Join(dir, sslPolicyFile)); err == nil {
		_ = json.Unmarshal(b, &old)
	}
	return old.Mode
}

// refreshFavoriteHosts keeps the popular+pinned hosts (include_favorites) current.
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
