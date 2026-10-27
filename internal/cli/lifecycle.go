package cli

import (
	"errors"
	"time"

	"github.com/spf13/cobra"

	"github.com/hakanor/proxypilot/internal/client"
	"github.com/hakanor/proxypilot/internal/daemon"
	"github.com/hakanor/proxypilot/internal/devices/macproxy"
)

func newStopCmd(app *App) *cobra.Command {
	var keepProxy bool
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the running daemon (and restore the macOS system proxy if ProxyPilot set it)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := daemon.Running(app.Config.DataDir)
			if errors.Is(err, daemon.ErrNotRunning) {
				return Exit(ExitNoDaemon, err)
			}
			if err != nil {
				return err
			}
			if err := stopDaemon(st); err != nil {
				return err
			}
			// A system proxy pointing at a stopped daemon leaves the Mac without network.
			restored := false
			if !keepProxy {
				mgr := macproxy.New(app.Config.DataDir)
				if err := mgr.Off(cmd.Context()); err == nil {
					restored = true
				} else if !errors.Is(err, macproxy.ErrNoBackup) && !errors.Is(err, macproxy.ErrNotMac) {
					app.Log.Warn("could not restore the system proxy; run `proxypilot system-proxy off`", "err", err)
				}
			}
			if app.Flags.JSON {
				return app.PrintJSON(map[string]any{"stopped": true, "pid": st.PID, "system_proxy_restored": restored})
			}
			app.Printf("Stopped (pid %d)\n", st.PID)
			if restored {
				app.Printf("Restored the previous macOS proxy settings.\n")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&keepProxy, "keep-system-proxy", false, "leave the macOS system proxy pointing at ProxyPilot")
	return cmd
}

// stopDaemon sends SIGTERM and waits up to 10s.
func stopDaemon(st daemon.State) error {
	if err := terminate(st.PID); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for daemon.Alive(st.PID) {
		if time.Now().After(deadline) {
			return Exitf(ExitTimeout, "pid %d did not stop within 10s", st.PID)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func newStatusCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			notRunning := func() error {
				if app.Flags.JSON {
					_ = app.PrintJSON(map[string]any{"running": false, "data_dir": app.Config.DataDir})
				} else {
					app.Printf("ProxyPilot is not running. Start it with `proxypilot start --detach`.\n")
				}
				if left, target := macproxy.New(app.Config.DataDir).LeftOn(cmd.Context()); left {
					app.Log.Warn("the macOS system proxy still points at " + target + " but ProxyPilot is not running: apps have no network. Run `proxypilot system-proxy off`")
				}
				return Exit(ExitNoDaemon, daemon.ErrNotRunning)
			}
			c, err := client.New(client.Options{DataDir: app.Config.DataDir, APIAddr: app.Flags.APIAddr, Token: app.Flags.Token})
			if errors.Is(err, client.ErrNoDaemon) {
				return notRunning()
			}
			if err != nil {
				return mapClientErr(err)
			}
			info, err := c.Status(cmd.Context())
			if errors.Is(err, client.ErrNoDaemon) {
				return notRunning()
			}
			if err != nil {
				return mapClientErr(err)
			}
			if app.Flags.JSON {
				return app.PrintJSON(info)
			}
			app.Printf("Running     pid %d, up %s\nProxy       %s\nAPI         http://%s\nFlows       %d\nRules       %d active\nBreakpoints %d pending\nCA          %s\n",
				info.PID, (time.Duration(info.UptimeSeconds) * time.Second).String(), info.ProxyAddr, info.APIAddr,
				info.Flows, info.ActiveRules, info.PendingBreakpoints, info.CAFingerprint)
			return nil
		},
	}
}
