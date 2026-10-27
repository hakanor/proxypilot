package cli

import (
	"github.com/spf13/cobra"

	"github.com/hakanor/proxypilot/internal/cdp"
)

func newChromeCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chrome",
		Short: "Capture Chrome tabs over DevTools (no proxy or certificate needed)",
		Long: "launch opens Chrome with a separate ProxyPilot profile (Chrome does not allow debugging your main profile).\n" +
			"attach connects to a Chrome you started with --remote-debugging-port=9222.\n" +
			"Rules (Map Local, rewrite, map remote, breakpoints) apply to Chrome traffic too.",
	}
	print := func(s cdp.Status) error {
		if app.Flags.JSON {
			return app.PrintJSON(s)
		}
		if !s.Connected {
			app.Printf("Not connected. Start with `proxypilot chrome launch`.\n")
			return nil
		}
		how := "attached to"
		if s.Launched {
			how = "launched"
		}
		app.Printf("Chrome %s (%s, %s)\n", how, s.Browser, s.Endpoint)
		for _, t := range s.Tabs {
			mark := app.paint("32", "●")
			if !t.Watching {
				mark = app.paint("90", "○")
			}
			app.Printf("  %s %-38s %s  (%d requests)\n", mark, truncate(t.Title, 38), truncate(t.URL, 60), t.Requests)
		}
		return nil
	}
	var startURL, addr string
	launch := &cobra.Command{
		Use:   "launch",
		Short: "Open Chrome with a ProxyPilot profile and capture its tabs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			s, err := c.ChromeLaunch(cmd.Context(), startURL)
			if err != nil {
				return mapClientErr(err)
			}
			return print(s)
		},
	}
	launch.Flags().StringVar(&startURL, "url", "", "page to open")
	attach := &cobra.Command{
		Use:   "attach",
		Short: "Connect to a running Chrome started with --remote-debugging-port",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			s, err := c.ChromeAttach(cmd.Context(), addr)
			if err != nil {
				return mapClientErr(err)
			}
			return print(s)
		},
	}
	attach.Flags().StringVar(&addr, "port", "9222", "debugging port or host:port")
	status := &cobra.Command{
		Use:   "status",
		Short: "Show the connection and the watched tabs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			s, err := c.ChromeStatus(cmd.Context())
			if err != nil {
				return mapClientErr(err)
			}
			return print(s)
		},
	}
	detach := &cobra.Command{
		Use:   "detach",
		Short: "Stop capturing (a launched Chrome stays open)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			s, err := c.ChromeDetach(cmd.Context())
			if err != nil {
				return mapClientErr(err)
			}
			return print(s)
		},
	}
	cmd.AddCommand(launch, attach, status, detach)
	return cmd
}
