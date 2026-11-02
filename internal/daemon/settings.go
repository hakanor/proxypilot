package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/hakanor/proxypilot/internal/api"
	"github.com/hakanor/proxypilot/internal/capture"
	"github.com/hakanor/proxypilot/internal/store"
)

// settingsFile keeps what the Settings page changes; when present it wins over config.yaml.
const settingsFile = "settings.json"

type settingsStore struct {
	path  string
	store *store.Store
	rec   *capture.Recorder

	mu  sync.Mutex
	cur api.Settings
}

// newSettings loads saved settings (falling back to maxDiskMB) and applies them.
func newSettings(dir string, maxDiskMB int, s *store.Store, rec *capture.Recorder) *settingsStore {
	st := &settingsStore{path: filepath.Join(dir, settingsFile), store: s, rec: rec, cur: api.Settings{MaxDiskMB: maxDiskMB}}
	if b, err := os.ReadFile(st.path); err == nil {
		var saved api.Settings
		if json.Unmarshal(b, &saved) == nil && saved.MaxDiskMB >= 0 {
			st.cur = saved
		}
	}
	st.apply()
	return st
}

func (st *settingsStore) apply() {
	st.store.SetMaxBytes(int64(st.cur.MaxDiskMB) << 20)
	st.rec.SetFilter(st.cur.Capture)
}

func (st *settingsStore) Settings() api.Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.cur
}

func (st *settingsStore) SetSettings(in api.Settings) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	if err := os.WriteFile(st.path, b, 0o600); err != nil {
		return err
	}
	st.cur = in
	st.apply()
	return nil
}
