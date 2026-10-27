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
		Mode: proxy.ModeAllowlist, Allowlist: []string{"api.example.com"}, Denylist: []string{"*.apple.com"},
		Learned: map[string]string{"pinned.example.com": "client rejected"},
	})
	p, ok := loadSSLPolicy(d.Config.DataDir)
	if !ok || p.Mode != proxy.ModeAllowlist || len(p.Allowlist) != 1 || len(p.Denylist) != 1 {
		t.Fatalf("loaded %+v ok=%v", p, ok)
	}
	if len(p.Learned) != 0 {
		t.Fatal("learned tunnels must not be saved")
	}

	if err := os.WriteFile(filepath.Join(d.Config.DataDir, sslPolicyFile), []byte(`{"mode":"sometimes"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadSSLPolicy(d.Config.DataDir); ok {
		t.Fatal("an invalid saved mode must fall back to config.yaml")
	}
}
