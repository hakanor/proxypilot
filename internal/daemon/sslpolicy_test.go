package daemon

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/hakanor/proxypilot/internal/proxy"
)

func TestSSLPolicySurvivesRestart(t *testing.T) {
	d := &Daemon{Config: testConfig(t), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if _, ok := loadSSLPolicy(d.Config.DataDir); ok {
		t.Fatal("no saved policy expected yet")
	}
	d.saveSSLPolicy(proxy.PolicySnapshot{
		Allowlist: []string{"api.example.com"}, Denylist: []string{"*.apple.com"},
		Learned: map[string]string{"pinned.example.com": "client rejected"},
	})
	p, ok := loadSSLPolicy(d.Config.DataDir)
	if !ok || len(p.Allowlist) != 1 || len(p.Denylist) != 1 {
		t.Fatalf("loaded %+v ok=%v", p, ok)
	}
	if len(p.Learned) != 0 {
		t.Fatal("learned tunnels must not be saved")
	}

	// A file from an older version still loads; its mode is ignored.
	if err := os.WriteFile(filepath.Join(d.Config.DataDir, sslPolicyFile), []byte(`{"mode":"all","allowlist":["a.com"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if p, ok := loadSSLPolicy(d.Config.DataDir); !ok || len(p.Allowlist) != 1 {
		t.Fatalf("old file: %+v ok=%v", p, ok)
	}
	if m := legacyMode(d.Config.DataDir); m != "all" {
		t.Fatalf("legacy mode = %q", m)
	}
	d.saveSSLPolicy(proxy.PolicySnapshot{Allowlist: []string{"a.com"}})
	if m := legacyMode(d.Config.DataDir); m != "" {
		t.Fatalf("re-saved file still has mode %q", m)
	}
}
