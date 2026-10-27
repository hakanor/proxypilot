package daemon

import (
	"os"
	"path/filepath"
	"strings"
)

// noCacheFile remembers the toolbar's "No caching" switch across restarts.
const noCacheFile = "nocache"

// noCacheSwitch turns No caching on or off and saves the choice.
type noCacheSwitch struct{ d *Daemon }

func (n noCacheSwitch) Enabled() bool { return n.d.NoCache.Enabled() }

func (n noCacheSwitch) SetEnabled(on bool) {
	n.d.NoCache.SetEnabled(on)
	if err := writeFlag(n.d.Config.DataDir, noCacheFile, on); err != nil {
		n.d.Log.Warn("save no caching", "err", err)
	}
}

func readFlag(dir, name string) bool {
	b, err := os.ReadFile(filepath.Join(dir, name))
	return err == nil && strings.TrimSpace(string(b)) == "on"
}

func writeFlag(dir, name string, on bool) error {
	v := "off\n"
	if on {
		v = "on\n"
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(v), 0o600)
}
