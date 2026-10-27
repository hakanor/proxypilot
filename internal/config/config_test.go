package config

import (
	"os"
	"path/filepath"
	"testing"
)

func mapEnv(m map[string]string) Env {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func ptr(s string) *string { return &s }

func TestDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(Overrides{DataDir: ptr(dir)}, mapEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy.Listen != "127.0.0.1:9090" || cfg.API.Listen != "127.0.0.1:9091" {
		t.Fatalf("unexpected listen defaults: %+v", cfg)
	}
	if cfg.Store.MaxEntries != 10000 || cfg.Store.InlineBodyLimit != 256*1024 {
		t.Fatalf("unexpected store defaults: %+v", cfg.Store)
	}
	if cfg.File != "" {
		t.Fatalf("no file expected, got %s", cfg.File)
	}
}

func TestPrecedence(t *testing.T) {
	dir := t.TempDir()
	yaml := "proxy:\n  listen: 0.0.0.0:7000\napi:\n  listen: 127.0.0.1:7001\nstore:\n  max_entries: 50\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	// file only
	cfg, err := Load(Overrides{DataDir: ptr(dir)}, mapEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy.Listen != "0.0.0.0:7000" || cfg.Store.MaxEntries != 50 {
		t.Fatalf("file not applied: %+v", cfg)
	}

	// env beats file
	env := mapEnv(map[string]string{"PROXYPILOT_PROXY_LISTEN": "127.0.0.1:8000", "PROXYPILOT_STORE_MAX_ENTRIES": "70"})
	cfg, err = Load(Overrides{DataDir: ptr(dir)}, env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy.Listen != "127.0.0.1:8000" || cfg.Store.MaxEntries != 70 {
		t.Fatalf("env did not win over file: %+v", cfg)
	}
	if cfg.API.Listen != "127.0.0.1:7001" {
		t.Fatalf("file value lost: %s", cfg.API.Listen)
	}

	// flag beats env
	cfg, err = Load(Overrides{DataDir: ptr(dir), ProxyListen: ptr("127.0.0.1:9999")}, env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy.Listen != "127.0.0.1:9999" {
		t.Fatalf("flag did not win over env: %s", cfg.Proxy.Listen)
	}
}

func TestDataDirPrecedence(t *testing.T) {
	envDir, flagDir := t.TempDir(), t.TempDir()
	env := mapEnv(map[string]string{"PROXYPILOT_HOME": envDir})
	cfg, err := Load(Overrides{}, env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != envDir {
		t.Fatalf("PROXYPILOT_HOME ignored: %s", cfg.DataDir)
	}
	cfg, err = Load(Overrides{DataDir: ptr(flagDir)}, env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != flagDir {
		t.Fatalf("--data-dir did not win: %s", cfg.DataDir)
	}
}

func TestExplicitMissingConfigFails(t *testing.T) {
	_, err := Load(Overrides{DataDir: ptr(t.TempDir()), ConfigFile: ptr("/nonexistent/proxypilot.yaml")}, mapEnv(nil))
	if err == nil {
		t.Fatal("expected error for missing explicit config file")
	}
}

func TestInvalidSSLMode(t *testing.T) {
	_, err := Load(Overrides{DataDir: ptr(t.TempDir())}, mapEnv(map[string]string{"PROXYPILOT_SSL_MODE": "sometimes"}))
	if err == nil {
		t.Fatal("expected validation error")
	}
}
