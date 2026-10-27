package cli

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/hakanor/proxypilot/internal/daemon"
)

const envDetached = "PROXYPILOT_DETACHED"

func newStartCmd(app *App) *cobra.Command {
	var listen string
	var detach, unsafeRemote, disableH2 bool
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the proxy daemon (foreground; --detach for background)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := app.Config
			if cmd.Flags().Changed("proxy-listen") {
				cfg.Proxy.Listen = listen
			}
			if disableH2 {
				cfg.Proxy.DisableH2 = true
			}
			if st, err := daemon.Running(cfg.DataDir); err == nil {
				if app.Flags.JSON {
					_ = app.PrintJSON(map[string]any{"running": true, "pid": st.PID, "proxy_addr": st.ProxyAddr})
				}
				return Exitf(ExitError, "already running (pid %d, proxy %s)", st.PID, st.ProxyAddr)
			}
			if !isLoopbackAddr(cfg.Proxy.Listen) {
				app.Log.Warn("proxy is reachable from your network; only do this on a trusted network", "listen", cfg.Proxy.Listen)
			}
			if detach {
				return startDetached(app, cfg.DataDir)
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			err := daemon.Run(ctx, cfg, app.Log, daemon.RunOptions{UnsafeRemoteAPI: unsafeRemote}, func(d *daemon.Daemon) {
				if os.Getenv(envDetached) == "" {
					app.Printf("ProxyPilot proxy on %s, control API on http://%s (Ctrl+C to stop)\n", d.Proxy.Addr(), d.API.Addr())
				}
			})
			if errors.Is(err, daemon.ErrAlreadyRunning) {
				return Exit(ExitError, err)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&listen, "proxy-listen", "", "proxy listen address (default 127.0.0.1:9090; 0.0.0.0:9090 for devices)")
	cmd.Flags().BoolVar(&detach, "detach", false, "run in the background")
	cmd.Flags().BoolVar(&disableH2, "disable-h2", false, "speak only HTTP/1.1 on intercepted connections (if an app misbehaves with HTTP/2)")
	cmd.Flags().BoolVar(&unsafeRemote, "unsafe-remote-api", false, "allow the control API on a non-loopback address (dangerous)")
	return cmd
}

// startDetached re-executes this binary without --detach in a new session,
// logs to <data>/proxypilot.log and waits until the daemon writes its state.
func startDetached(app *App, dataDir string) error {
	args := make([]string, 0, len(os.Args))
	for _, a := range os.Args[1:] {
		if a != "--detach" && a != "--detach=true" {
			args = append(args, a)
		}
	}
	st, logPath, err := spawnDaemon(app, dataDir, args)
	if err != nil {
		return err
	}
	if app.Flags.JSON {
		return app.PrintJSON(map[string]any{"running": true, "pid": st.PID, "proxy_addr": st.ProxyAddr, "api_addr": st.APIAddr, "log": logPath})
	}
	app.Printf("ProxyPilot started in the background (pid %d)\nProxy: %s\nAPI:   http://%s\nLog:   %s\n", st.PID, st.ProxyAddr, st.APIAddr, logPath)
	return nil
}

// spawnDaemon runs `<exe> args...` detached and waits for its state file.
func spawnDaemon(app *App, dataDir string, args []string) (*daemon.State, string, error) {
	if err := app.Config.EnsureDataDir(); err != nil {
		return nil, "", err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, "", err
	}
	logPath := filepath.Join(dataDir, daemon.LogFile)
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, "", err
	}
	defer logf.Close()

	c := exec.Command(exe, args...)
	c.Env = append(os.Environ(), envDetached+"=1")
	c.Stdout, c.Stderr, c.Stdin = logf, logf, nil
	c.SysProcAttr = detachAttr()
	if err := c.Start(); err != nil {
		return nil, "", err
	}
	pid := c.Process.Pid
	exited := make(chan error, 1)
	go func() { exited <- c.Wait() }()

	deadline := time.After(10 * time.Second)
	for {
		select {
		case err := <-exited:
			return nil, "", Exitf(ExitError, "daemon exited during startup (%v); see %s", err, logPath)
		case <-deadline:
			return nil, "", Exitf(ExitTimeout, "daemon did not become ready in 10s; see %s", logPath)
		case <-time.After(100 * time.Millisecond):
		}
		st, err := daemon.Running(dataDir)
		if err != nil || st.PID != pid {
			continue
		}
		_ = c.Process.Release()
		return &st, logPath, nil
	}
}

func isLoopbackAddr(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}
