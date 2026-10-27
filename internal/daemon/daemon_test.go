package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hakanor/proxypilot/internal/config"
)

func testConfig(t *testing.T) config.Config {
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	cfg.Proxy.Listen = "127.0.0.1:0"
	cfg.API.Listen = "127.0.0.1:0"
	return cfg
}

func TestRunWritesAndRemovesState(t *testing.T) {
	cfg := testConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan *Daemon, 1)
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, discardLog(), RunOptions{}, func(d *Daemon) { ready <- d }) }()

	var d *Daemon
	select {
	case d = <-ready:
	case err := <-done:
		t.Fatalf("Run exited early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("not ready")
	}
	st, err := Running(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if st.PID != os.Getpid() || st.ProxyAddr != d.Proxy.Addr().String() {
		t.Fatalf("state = %+v", st)
	}

	// A second instance on the same data dir must refuse to start.
	if err := Run(context.Background(), cfg, discardLog(), RunOptions{}, nil); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Run: %v", err)
	}

	tok, err := ReadToken(cfg.DataDir)
	if err != nil || len(tok) != 64 {
		t.Fatalf("token %q: %v", tok, err)
	}
	fi, _ := os.Stat(filepath.Join(cfg.DataDir, TokenFile))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("token mode %#o", fi.Mode().Perm())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown hung")
	}
	if _, err := Running(cfg.DataDir); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("state not cleaned up: %v", err)
	}
}

func TestStaleStateIsIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := writeState(dir, State{PID: 999999, ProxyAddr: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Running(dir); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("stale pid treated as running: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, StateFile)); !os.IsNotExist(err) {
		t.Fatal("stale state file not removed")
	}
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
