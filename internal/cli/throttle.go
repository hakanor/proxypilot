package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hakanor/proxypilot/internal/api"
	"github.com/hakanor/proxypilot/internal/proxy"
)

func newThrottleCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "throttle",
		Short: "Simulate a slow network (on <profile>|off|status)",
		Long: "Profiles: edge, 3g, lte, bad-wifi, or custom with --down/--up/--latency.\n" +
			"Every request waits the profile's latency, then bodies flow at its bandwidth.\n" +
			"--host limits it to some hosts (repeat, or comma-separate; *.example.com works).",
	}
	print := func(t api.ThrottleInfo) error {
		if app.Flags.JSON {
			return app.PrintJSON(t)
		}
		if !t.Enabled {
			app.Printf("Throttle: %s\n", app.paint("33", "off"))
			return nil
		}
		scope := "all traffic"
		if len(t.Hosts) > 0 {
			scope = strings.Join(t.Hosts, ", ")
		}
		app.Printf("Throttle: %s %s — down %s, up %s, latency %d ms (%s)\n", app.paint("32", "on"), t.Name,
			kbps(t.DownKbps), kbps(t.UpKbps), t.LatencyMs, scope)
		return nil
	}
	run := func(fn func(*cobra.Command) (api.ThrottleInfo, error)) func(*cobra.Command, []string) error {
		return func(c *cobra.Command, _ []string) error {
			t, err := fn(c)
			if err != nil {
				return mapClientErr(err)
			}
			return print(t)
		}
	}

	var hosts []string
	var down, up, latency int
	on := &cobra.Command{
		Use:       "on <edge|3g|lte|bad-wifi|custom>",
		Short:     "Turn throttling on with a profile",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"edge", "3g", "lte", "bad-wifi", "custom"},
		RunE: func(c *cobra.Command, args []string) error {
			cl, err := app.apiClient()
			if err != nil {
				return err
			}
			s := proxy.ThrottleSettings{Enabled: true, Hosts: splitList(hosts)}
			s.Name = args[0]
			if s.Name == "custom" {
				s.DownKbps, s.UpKbps, s.LatencyMs = down, up, latency
			}
			return run(func(c *cobra.Command) (api.ThrottleInfo, error) { return cl.SetThrottle(c.Context(), s) })(c, args)
		},
	}
	on.Flags().StringSliceVar(&hosts, "host", nil, "only throttle these hosts")
	on.Flags().IntVar(&down, "down", 0, "custom: download kbit/s")
	on.Flags().IntVar(&up, "up", 0, "custom: upload kbit/s")
	on.Flags().IntVar(&latency, "latency", 0, "custom: added latency in ms")

	cmd.AddCommand(on, &cobra.Command{
		Use:   "off",
		Short: "Turn throttling off",
		RunE: func(c *cobra.Command, args []string) error {
			cl, err := app.apiClient()
			if err != nil {
				return err
			}
			return run(func(c *cobra.Command) (api.ThrottleInfo, error) {
				return cl.SetThrottle(c.Context(), proxy.ThrottleSettings{})
			})(c, args)
		},
	}, &cobra.Command{
		Use:   "status",
		Short: "Show the current throttle",
		RunE: func(c *cobra.Command, args []string) error {
			cl, err := app.apiClient()
			if err != nil {
				return err
			}
			return run(func(c *cobra.Command) (api.ThrottleInfo, error) { return cl.Throttle(c.Context()) })(c, args)
		},
	})
	return cmd
}

func kbps(n int) string {
	switch {
	case n <= 0:
		return "unlimited"
	case n >= 1000:
		return fmt.Sprintf("%g Mbit/s", float64(n)/1000)
	}
	return fmt.Sprintf("%d kbit/s", n)
}

func splitList(in []string) []string {
	var out []string
	for _, s := range in {
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}
