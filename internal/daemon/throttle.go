package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/hakanor/proxypilot/internal/events"
	"github.com/hakanor/proxypilot/internal/proxy"
)

// throttleFile keeps the network throttle across restarts.
const throttleFile = "throttle.json"

// throttleSwitch applies throttle settings and saves them.
type throttleSwitch struct{ d *Daemon }

func (t throttleSwitch) Settings() proxy.ThrottleSettings { return t.d.Throttle.Settings() }

func (t throttleSwitch) Set(s proxy.ThrottleSettings) {
	t.d.Throttle.Set(s)
	if b, err := json.Marshal(s); err == nil {
		if err := os.WriteFile(filepath.Join(t.d.Config.DataDir, throttleFile), b, 0o600); err != nil {
			t.d.Log.Warn("save throttle", "err", err)
		}
	}
	t.d.Bus.Publish(events.ProxyState, map[string]any{"state": "running", "throttle": s})
}

func loadThrottle(dir string) proxy.ThrottleSettings {
	var s proxy.ThrottleSettings
	if b, err := os.ReadFile(filepath.Join(dir, throttleFile)); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

// throttleName is the active profile for the status bar ("" when off).
func (d *Daemon) throttleName() string {
	if s := d.Throttle.Settings(); s.Enabled {
		return s.Name
	}
	return ""
}
