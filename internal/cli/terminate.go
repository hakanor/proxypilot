package cli

import (
	"bufio"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hakanor/proxypilot/internal/daemon"
)

func newTerminateCmd(app *App) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "terminate",
		Short: "Clean up and stop: release paused requests, delete agent and breakpoint rules, restore the system proxy",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := daemon.Running(app.Config.DataDir)
			if err != nil {
				return Exit(ExitNoDaemon, err)
			}
			if !yes {
				app.Printf("This lets paused requests continue, deletes agent and breakpoint rules, restores the system proxy and stops ProxyPilot (pid %d).\nTerminate? [y/N] ", st.PID)
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
					return Exitf(ExitUsage, "cancelled")
				}
			}
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			var out struct {
				Result  daemon.TerminateResult `json:"result"`
				Warning string                 `json:"warning"`
			}
			if err := c.Do(cmd.Context(), http.MethodPost, "/terminate", map[string]bool{"delete_breakpoint_rules": true}, &out); err != nil {
				return mapClientErr(err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for daemon.Alive(st.PID) {
				if time.Now().After(deadline) {
					return Exitf(ExitTimeout, "pid %d did not stop within 10s", st.PID)
				}
				time.Sleep(100 * time.Millisecond)
			}
			if app.Flags.JSON {
				return app.PrintJSON(map[string]any{"stopped": true, "pid": st.PID, "result": out.Result, "warning": out.Warning})
			}
			r := out.Result
			app.Printf("Released %d paused requests, deleted %d agent rules and %d breakpoint rules.\n", r.Released, r.AgentRules, r.BreakpointRules)
			if r.SystemProxyRestored {
				app.Printf("Restored the previous system proxy settings.\n")
			}
			if out.Warning != "" {
				app.Log.Warn("some cleanup failed", "err", out.Warning)
			}
			app.Printf("Stopped (pid %d)\n", st.PID)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation")
	return cmd
}
