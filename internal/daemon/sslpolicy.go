package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/hakanor/proxypilot/internal/proxy"
)

// sslPolicyFile keeps SSL mode and lists changed in the UI or API across
// restarts; when present it wins over the ssl section of config.yaml.
const sslPolicyFile = "ssl-policy.json"

func (d *Daemon) saveSSLPolicy(p proxy.PolicySnapshot) {
	p.Learned = nil // learned tunnels are per run
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
