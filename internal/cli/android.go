package cli

import (
	"errors"
	"net"

	"github.com/spf13/cobra"

	"github.com/hakanor/proxypilot/internal/certpage"
	"github.com/hakanor/proxypilot/internal/devices/android"
)

func newSetupAndroidCmd(app *App) *cobra.Command {
	var serial, mode, caMode string
	var remove, list bool
	cmd := &cobra.Command{
		Use:   "android",
		Short: "Point an Android emulator or USB device at ProxyPilot and install the CA",
		Long: "Emulators use 10.0.2.2 (the host). USB devices use `adb reverse`, so Wi-Fi is not needed.\n" +
			"--ca user copies the certificate to Downloads for a manual install (apps trust it only in debug builds);\n" +
			"--ca system installs it as a system CA on emulators without Google Play or rooted devices (until reboot).",
		Example: "  proxypilot setup android --list\n" +
			"  proxypilot setup android                       # the only connected device, default mode\n" +
			"  proxypilot setup android --serial emulator-5554 --ca system\n" +
			"  proxypilot setup android --remove",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sv, err := app.devices()
			if err != nil {
				return err
			}
			a, err := sv.Android()
			if err != nil {
				return Exit(ExitError, err)
			}
			ctx := cmd.Context()
			target, _ := app.proxyTarget()
			host, port, _ := net.SplitHostPort(target)

			if list {
				devs, err := a.Devices(ctx, port, sv.AndroidHash())
				if err != nil {
					return err
				}
				if app.Flags.JSON {
					return app.PrintJSON(map[string]any{"adb": a.Path, "devices": devs})
				}
				if len(devs) == 0 {
					app.Printf("No devices. Start an emulator or connect a phone with USB debugging on.\n")
					return nil
				}
				app.Printf("%-22s %-12s %-24s %-4s %-9s %s\n", "SERIAL", "STATE", "MODEL", "API", "KIND", "PROXY")
				for _, d := range devs {
					kind := "device"
					if d.Emulator {
						kind = "emulator"
					}
					proxy := d.Proxy
					if d.Ours {
						proxy += " (ProxyPilot)"
					}
					app.Printf("%-22s %-12s %-24s %-4d %-9s %s\n", d.Serial, d.State, truncate(d.Model, 24), d.API, kind, proxy)
				}
				return nil
			}

			if remove {
				r, err := a.Remove(ctx, serial, port)
				if err != nil {
					return androidExit(err)
				}
				return app.printAndroid(r)
			}

			if mode == android.ModeWiFi && (host == "127.0.0.1" || host == "localhost") {
				return Exitf(ExitUsage, "wifi mode needs the proxy on all interfaces: proxypilot start --detach --proxy-listen 0.0.0.0:%s", port)
			}
			o := sv.AndroidOptions(android.SetupOptions{Serial: serial, Mode: mode, CA: caMode, Port: port})
			if ips := certpage.LANAddrs(); len(ips) > 0 {
				o.LANIP = ips[0]
			}
			r, err := a.Setup(ctx, o)
			if err != nil {
				return androidExit(err)
			}
			return app.printAndroid(r)
		},
	}
	f := cmd.Flags()
	f.StringVar(&serial, "serial", "", "device serial (from --list); needed when several are connected")
	f.StringVar(&mode, "mode", "", "emulator, usb or wifi (default: emulator for emulators, usb for devices)")
	f.StringVar(&caMode, "ca", "user", "user, system or none")
	f.BoolVar(&remove, "remove", false, "undo: clear the proxy and the USB port forward")
	f.BoolVar(&list, "list", false, "list connected devices")
	return cmd
}

func androidExit(err error) error {
	switch {
	case errors.Is(err, android.ErrNoADB), errors.Is(err, android.ErrNoDevice), errors.Is(err, android.ErrNoRoot):
		return Exit(ExitError, err)
	case errors.Is(err, android.ErrMany):
		return Exit(ExitUsage, err)
	}
	return err
}

func (a *App) printAndroid(r *android.Result) error {
	if a.Flags.JSON {
		return a.PrintJSON(r)
	}
	a.Printf("%s %s\n", r.Device.Serial, r.Device.Model)
	for _, d := range r.Done {
		a.Printf("  %s %s\n", a.paint("32", "✓"), d)
	}
	for _, m := range r.Manual {
		a.Printf("  %s %s\n", a.paint("33", "→"), m)
	}
	for _, w := range r.Warnings {
		if w == "user_ca_apps" {
			a.Printf("\nApps trust user certificates only in debug builds. Add this to your app:\n\n%s\n\nFlutter ignores the Android proxy; in debug builds:\n\n%s\n", android.DevNote, android.FlutterNote)
		}
	}
	return nil
}
