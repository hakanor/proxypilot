package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/hakanor/proxypilot/internal/client"
	"github.com/hakanor/proxypilot/internal/daemon"
	"github.com/hakanor/proxypilot/internal/mcpserver"
)

func newMCPCmd(app *App) *cobra.Command {
	var readOnly, allowSecrets, autoStart bool
	var agent string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server on stdio (for Claude Code, Cursor and other agents)",
		Long: "Serves ProxyPilot tools over MCP on stdin/stdout. Configure it in your agent, e.g.\n" +
			"  claude mcp add proxypilot -- proxypilot mcp --agent-name claude\n" +
			"Rules the agent creates are owned by agent:<name> and expire after 1h by default.\n" +
			"Secrets are always masked unless you pass --allow-secrets (agents cannot change it).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := app.Config.DataDir
			if _, err := daemon.Running(dir); errors.Is(err, daemon.ErrNotRunning) && autoStart {
				if _, _, err := spawnDaemon(app, dir, []string{"start", "--data-dir", dir}); err != nil {
					return err
				}
			}
			if allowSecrets {
				app.Log.Warn("--allow-secrets: the agent will see tokens, cookies and passwords")
			}
			dial := func() (*client.Client, *client.Client, error) {
				opts := client.Options{DataDir: dir, APIAddr: app.Flags.APIAddr, Token: app.Flags.Token}
				raw, err := client.New(opts)
				if err != nil {
					return nil, nil, err
				}
				opts.Redact = !allowSecrets
				c, err := client.New(opts)
				return c, raw, err
			}
			return mcpserver.New(dial, mcpserver.Options{ReadOnly: readOnly, Agent: agent, AllowSecrets: allowSecrets}).ServeStdio()
		},
	}
	cmd.Flags().BoolVar(&readOnly, "read-only", false, "only register tools that do not change anything")
	cmd.Flags().StringVar(&agent, "agent-name", "agent", "owner name for rules the agent creates (agent:<name>)")
	cmd.Flags().BoolVar(&allowSecrets, "allow-secrets", false, "do not mask secrets in tool results")
	cmd.Flags().BoolVar(&autoStart, "auto-start", false, "start the daemon if it is not running")
	return cmd
}
