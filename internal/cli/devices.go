package cli

import (
	"errors"
	"net"
	"strconv"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
	"github.com/spf13/cobra"

	"github.com/hakanor/proxypilot/internal/api"

	"github.com/hakanor/proxypilot/internal/daemon"
	"github.com/hakanor/proxypilot/internal/devices"
	"github.com/hakanor/proxypilot/internal/devices/execx"
	"github.com/hakanor/proxypilot/internal/devices/macproxy"
	"github.com/hakanor/proxypilot/internal/devices/simctl"
)

func (a *App) devices() (*devices.Service, error) {
	root, err := a.loadCA()
	if err != nil {
		return nil, err
	}
	s := devices.New(root, a.Config.DataDir)
	s.ADBPath = a.Config.Android.ADB
	target, _ := a.proxyTarget()
	_, s.ProxyPort, _ = net.SplitHostPort(target)
	return s, nil
}

// proxyTarget is the running daemon's proxy address, else the configured one.
func (a *App) proxyTarget() (target, apiAddr string) {
	if st, err := daemon.Running(a.Config.DataDir); err == nil {
		return st.ProxyAddr, st.APIAddr
	}
	return a.Config.Proxy.Listen, a.Config.API.Listen
}

func deviceExit(err error) error {
	switch {
	case errors.Is(err, simctl.ErrNoXcode), errors.Is(err, macproxy.ErrNotMac):
		return Exit(ExitError, err)
	case errors.Is(err, macproxy.ErrNeedsAdmin):
		return Exit(ExitUnauthorized, err)
	case errors.Is(err, macproxy.ErrNoBackup):
		return Exit(ExitNotFound, err)
	}
	return err
}

func quoteCmds(cmds [][]string, prefix string) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = prefix + execx.Quote(c[0], c[1:]...)
	}
	return out
}

func newSystemProxyCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "system-proxy",
		Short: "Point the macOS system proxy at ProxyPilot (on|off|status)",
	}
	status := func(sv *devices.Service, cmd *cobra.Command) error {
		target, _ := app.proxyTarget()
		st, err := sv.Proxy.Status(cmd.Context(), target)
		if err != nil {
			return deviceExit(err)
		}
		if app.Flags.JSON {
			return app.PrintJSON(st)
		}
		state := app.paint("33", "off")
		if st.Enabled {
			state = app.paint("32", "on")
		}
		app.Printf("System proxy: %s (target %s)\n", state, target)
		for _, s := range st.Services {
			app.Printf("  %-22s http %s  https %s\n", s.Name, proxyText(s.Web), proxyText(s.Secure))
		}
		if st.Managed {
			app.Printf("Backup of the previous settings: %s (restored by `proxypilot system-proxy off`)\n", st.BackupAt.Local().Format("2006-01-02 15:04"))
		}
		return nil
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show the current macOS proxy settings",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sv, err := app.devices()
			if err != nil {
				return err
			}
			return status(sv, cmd)
		},
	}, &cobra.Command{
		Use:   "on",
		Short: "Back up the current settings and send HTTP/HTTPS of every network service to ProxyPilot",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sv, err := app.devices()
			if err != nil {
				return err
			}
			target, apiAddr := app.proxyTarget()
			if _, err := sv.Proxy.On(cmd.Context(), target, apiAddr); err != nil {
				if errors.Is(err, macproxy.ErrNeedsAdmin) {
					names, _ := sv.Proxy.Services(cmd.Context())
					app.printAdminHelp(quoteCmds(sv.Proxy.Commands(names, target, apiAddr), "sudo "))
				}
				return deviceExit(err)
			}
			if _, err := daemon.Running(app.Config.DataDir); err != nil {
				app.Log.Warn("ProxyPilot is not running: apps will have no network until you start it or run `proxypilot system-proxy off`")
			}
			return status(sv, cmd)
		},
	}, &cobra.Command{
		Use:   "off",
		Short: "Restore the network proxy settings saved by `system-proxy on`",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sv, err := app.devices()
			if err != nil {
				return err
			}
			restore, _ := sv.Proxy.RestoreCommands()
			if err := sv.Proxy.Off(cmd.Context()); err != nil {
				if errors.Is(err, macproxy.ErrNeedsAdmin) {
					app.printAdminHelp(quoteCmds(restore, "sudo "))
				}
				return deviceExit(err)
			}
			return status(sv, cmd)
		},
	})
	return cmd
}

func proxyText(p macproxy.Proxy) string {
	if !p.Enabled {
		return "off"
	}
	return p.Server + ":" + strconv.Itoa(p.Port)
}

func (a *App) printAdminHelp(cmds []string) {
	if a.Flags.JSON {
		_ = a.PrintJSON(map[string]any{"error": "needs_admin", "commands": cmds})
		return
	}
	a.Printf("macOS needs administrator rights for this. Run:\n\n")
	for _, c := range cmds {
		a.Printf("  %s\n", c)
	}
	a.Printf("\n")
}

func newSetupCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Configure simulators, devices and browsers",
	}
	var udids []string
	var allBooted bool
	ios := &cobra.Command{
		Use:   "ios-sim",
		Short: "Install the ProxyPilot CA into iOS simulators",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sv, err := app.devices()
			if err != nil {
				return err
			}
			if len(udids) == 0 && !allBooted {
				list, err := sv.Sim.List(cmd.Context())
				if err != nil {
					return deviceExit(err)
				}
				if app.Flags.JSON {
					return app.PrintJSON(map[string]any{"simulators": list})
				}
				app.Printf("%-38s %-24s %-10s %-9s %s\n", "UDID", "NAME", "RUNTIME", "STATE", "CA")
				for _, d := range list {
					app.Printf("%-38s %-24s %-10s %-9s %s\n", d.UDID, truncate(d.Name, 24), d.Runtime, d.State, d.CA)
				}
				app.Printf("\nInstall with `proxypilot setup ios-sim --all-booted` or `--udid <udid>`.\n")
				return nil
			}
			res, err := sv.InstallCA(cmd.Context(), udids...)
			if err != nil {
				return deviceExit(err)
			}
			proxyOn := false
			target, _ := app.proxyTarget()
			if st, err := sv.Proxy.Status(cmd.Context(), target); err == nil {
				proxyOn = st.Enabled
			}
			failed := 0
			for _, r := range res {
				if !r.OK {
					failed++
				}
			}
			if app.Flags.JSON {
				_ = app.PrintJSON(map[string]any{"results": res, "system_proxy": proxyOn})
			} else {
				if len(res) == 0 {
					app.Printf("No booted simulators. Boot one in Xcode or with `xcrun simctl boot <udid>`.\n")
				}
				for _, r := range res {
					if r.OK {
						app.Printf("%s %s: CA installed\n", app.paint("32", "✓"), r.Name)
					} else {
						app.Printf("%s %s %s: %s\n", app.paint("31", "✗"), r.Name, r.UDID, r.Error)
					}
				}
				if !proxyOn {
					app.Printf("\nSimulators use the Mac's network settings. Turn on the system proxy:\n  proxypilot system-proxy on\n")
				}
			}
			if failed > 0 || len(res) == 0 {
				return Exitf(ExitError, "%d of %d simulators failed", failed, len(res))
			}
			return nil
		},
	}
	ios.Flags().StringSliceVar(&udids, "udid", nil, "simulator UDID (repeatable)")
	ios.Flags().BoolVar(&allBooted, "all-booted", false, "install into every booted simulator")
	cmd.AddCommand(ios, newSetupDeviceCmd(app), newSetupAndroidCmd(app))
	return cmd
}

func newSetupDeviceCmd(app *App) *cobra.Command {
	var noQR bool
	cmd := &cobra.Command{
		Use:   "device",
		Short: "Connect a physical iPhone or Android phone (QR code + steps)",
		RunE: func(*cobra.Command, []string) error {
			target, _ := app.proxyTarget()
			d := api.DeviceSetupFor(target)
			if app.Flags.JSON {
				return app.PrintJSON(d)
			}
			if !d.Reachable {
				app.Printf("%s The proxy listens on %s only, so phones cannot reach it. Restart it on all interfaces:\n\n", app.paint("33", "!"), target)
				app.Printf("  proxypilot stop && proxypilot start --detach --proxy-listen 0.0.0.0:%s\n\n", d.Port)
				app.Printf("  Only do this on a network you trust: anyone on it can use the proxy.\n\n")
			}
			if len(d.LANIPs) == 0 {
				return Exitf(ExitError, "no LAN address found: connect this Mac to the same Wi-Fi as the phone")
			}
			ip := d.LANIPs[0]
			if !noQR {
				q, err := qrcode.New(d.PageURL, qrcode.Medium)
				if err == nil {
					app.Printf("Scan with the phone camera (opens %s):\n\n%s\n", d.PageURL, q.ToSmallString(false))
				}
			}
			app.Printf("Proxy for the phone: %s  port %s", ip, d.Port)
			if len(d.LANIPs) > 1 {
				app.Printf("   (other addresses: %s)", strings.Join(d.LANIPs[1:], ", "))
			}
			app.Printf("\n\niPhone / iPad\n")
			app.Printf("  1. Settings → Wi-Fi → (i) next to your network → Configure Proxy → Manual → %s, port %s\n", ip, d.Port)
			app.Printf("  2. In Safari open http://proxypilot.cert and allow the profile download\n")
			app.Printf("  3. Settings → Profile Downloaded → Install\n")
			app.Printf("  4. Settings → General → About → Certificate Trust Settings → enable full trust for the ProxyPilot CA\n")
			app.Printf("\nAndroid\n")
			app.Printf("  1. Settings → Network → Wi-Fi → your network → Edit → Advanced → Proxy: Manual, %s, port %s\n", ip, d.Port)
			app.Printf("  2. In the browser open http://proxypilot.cert and download the .crt\n")
			app.Printf("  3. Settings → Security → Encryption & credentials → Install a certificate → CA certificate\n")
			app.Printf("     Apps trust user CAs only in debug builds with a network_security_config (see the Android setup guide).\n")
			if d.Firewall == "on" {
				app.Printf("\n%s The macOS firewall is on. If the phone cannot connect, allow incoming connections for proxypilot\n", app.paint("33", "!"))
				app.Printf("  (System Settings → Network → Firewall → Options).\n")
			}
			app.Printf("\nThe Setup page in `proxypilot ui` shows when the phone connects and when HTTPS works.\n")
			return nil
		},
	}
	cmd.Flags().BoolVar(&noQR, "no-qr", false, "do not print the QR code")
	return cmd
}

func newUninstallCmd(app *App) *cobra.Command {
	var yes, purge bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Undo everything ProxyPilot changed (system proxy, simulator CAs) and show what needs manual steps",
		Long: "Without --yes it only shows the plan. With --yes it stops the daemon, restores the system proxy,\n" +
			"removes the CA from iOS simulators and prints the admin commands for the macOS keychain.\n" +
			"--purge also deletes the data directory (CA key, flows, token).",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sv, err := app.devices()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if !yes {
				plan := sv.Plan(ctx)
				if app.Flags.JSON {
					return app.PrintJSON(map[string]any{"dry_run": true, "steps": plan})
				}
				app.printPlan(plan)
				app.Printf("\nRun `proxypilot uninstall --yes` to apply the automatic steps (add --purge to delete %s).\n", app.Config.DataDir)
				return nil
			}
			stopped := false
			if st, err := daemon.Running(app.Config.DataDir); err == nil {
				if err := stopDaemon(st); err != nil {
					return err
				}
				stopped = true
			}
			res := sv.Clean(ctx)
			plan := sv.Plan(ctx)
			purged := false
			if purge {
				if err := sv.Purge(); err != nil {
					return err
				}
				purged = true
			}
			if app.Flags.JSON {
				return app.PrintJSON(map[string]any{"stopped_daemon": stopped, "results": res, "steps": plan, "purged": purged})
			}
			if stopped {
				app.Printf("%s Stopped the daemon\n", app.paint("32", "✓"))
			}
			for _, r := range res {
				for _, d := range r.Done {
					app.Printf("%s %s: %s\n", app.paint("32", "✓"), stepTitle(r.ID), d)
				}
				if r.Error != "" {
					app.Printf("%s %s: %s\n", app.paint("31", "✗"), stepTitle(r.ID), r.Error)
				}
			}
			if purged {
				app.Printf("%s Deleted %s\n", app.paint("32", "✓"), app.Config.DataDir)
			}
			app.Printf("\n")
			app.printPlan(plan)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "apply the automatic steps")
	cmd.Flags().BoolVar(&purge, "purge", false, "also delete the data directory (with --yes)")
	return cmd
}

func stepTitle(id string) string {
	switch id {
	case "system_proxy":
		return "macOS system proxy"
	case "simulators":
		return "iOS simulators"
	case "mac_trust":
		return "macOS keychain trust"
	case "android":
		return "Android devices (adb)"
	case "devices":
		return "Physical devices"
	case "data":
		return "Data directory"
	}
	return id
}

func (a *App) printPlan(steps []devices.Step) {
	for _, s := range steps {
		mark := map[string]string{"pending": a.paint("33", "•"), "clean": a.paint("32", "✓"), "manual": "·", "unavailable": "–"}[s.Status]
		a.Printf("%s %s", mark, stepTitle(s.ID))
		switch s.Status {
		case "clean":
			a.Printf(": nothing to remove\n")
		case "unavailable":
			a.Printf(": not available on this machine\n")
		default:
			a.Printf("\n")
		}
		for _, d := range s.Details {
			a.Printf("    %s\n", d)
		}
		if s.ID == "devices" {
			a.Printf("    iOS: Settings → General → VPN & Device Management → ProxyPilot CA → Remove Profile\n")
			a.Printf("    Android: Settings → Security → Encryption & credentials → User credentials → ProxyPilot CA → Remove\n")
		}
		for _, c := range s.Commands {
			a.Printf("    $ %s\n", c)
		}
	}
}
