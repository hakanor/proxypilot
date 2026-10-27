// Package cli implements the proxypilot command line.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/hakanor/proxypilot/internal/config"
	"github.com/hakanor/proxypilot/internal/logging"
)

// GlobalFlags are available on every command.
type GlobalFlags struct {
	JSON      bool
	Config    string
	DataDir   string
	APIAddr   string
	Token     string
	NoColor   bool
	NoRedact  bool
	Quiet     bool
	LogLevel  string
	LogFormat string
}

// App is the state shared by commands after flags and config are resolved.
type App struct {
	Flags  GlobalFlags
	Config config.Config
	Log    *slog.Logger
	Out    io.Writer
	ErrOut io.Writer
}

func (a *App) overrides(cmd *cobra.Command) config.Overrides {
	var o config.Overrides
	changed := func(name string) bool { return cmd.Flags().Changed(name) }
	if changed("config") {
		o.ConfigFile = &a.Flags.Config
	}
	if changed("data-dir") {
		o.DataDir = &a.Flags.DataDir
	}
	if changed("api-addr") {
		o.APIListen = &a.Flags.APIAddr
	}
	if changed("log-level") {
		o.LogLevel = &a.Flags.LogLevel
	}
	if changed("log-format") {
		o.LogFormat = &a.Flags.LogFormat
	}
	return o
}

// PrintJSON writes v as indented JSON to stdout.
func (a *App) PrintJSON(v any) error {
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Printf writes human output unless --quiet.
func (a *App) Printf(format string, args ...any) {
	if a.Flags.Quiet {
		return
	}
	fmt.Fprintf(a.Out, format, args...)
}

func NewRootCmd(app *App) *cobra.Command {
	root := &cobra.Command{
		Use:           "proxypilot",
		Short:         "AI-ready HTTP/HTTPS debugging proxy",
		Long:          "ProxyPilot captures, inspects and rewrites HTTP(S) and WebSocket traffic from simulators, devices and browsers.\nHumans use the web UI, scripts use --json, agents use the MCP server.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(app.overrides(cmd), nil)
			if err != nil {
				return Exit(ExitUsage, err)
			}
			app.Config = cfg
			lg, err := logging.New(app.ErrOut, cfg.Log.Level, cfg.Log.Format)
			if err != nil {
				return Exit(ExitUsage, err)
			}
			app.Log = lg
			slog.SetDefault(lg)
			return nil
		},
	}
	root.SetOut(app.Out)
	root.SetErr(app.ErrOut)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return Exit(ExitUsage, err) })

	f := root.PersistentFlags()
	f.BoolVar(&app.Flags.JSON, "json", false, "machine-readable JSON output")
	f.StringVar(&app.Flags.Config, "config", "", "config file (default <data-dir>/config.yaml)")
	f.StringVar(&app.Flags.DataDir, "data-dir", "", "data directory (env PROXYPILOT_HOME)")
	f.StringVar(&app.Flags.APIAddr, "api-addr", "", "control API address (default 127.0.0.1:9091)")
	f.StringVar(&app.Flags.Token, "token", "", "control API token (default: read from <data-dir>/token)")
	f.BoolVar(&app.Flags.NoColor, "no-color", false, "disable colored output")
	f.BoolVar(&app.Flags.NoRedact, "no-redact", false, "show secrets (tokens, cookies, passwords) in output")
	f.BoolVarP(&app.Flags.Quiet, "quiet", "q", false, "only print errors")
	f.StringVar(&app.Flags.LogLevel, "log-level", "info", "debug|info|warn|error")
	f.StringVar(&app.Flags.LogFormat, "log-format", "text", "text|json")

	root.AddCommand(
		newStartCmd(app),
		newStopCmd(app),
		newStatusCmd(app),
		newVersionCmd(app),
		newListCmd(app),
		newShowCmd(app),
		newSearchCmd(app),
		newSummaryCmd(app),
		newTailCmd(app),
		newWaitCmd(app),
		newClearCmd(app),
		newUICmd(app),
		newSystemProxyCmd(app),
		newThrottleCmd(app),
		newTUICmd(app),
		newSetupCmd(app),
		newUninstallCmd(app),
		newRuleCmd(app),
		newBreakpointCmd(app),
		newMCPCmd(app),
		newRepeatCmd(app),
		newCurlCmd(app),
		newComposeCmd(app),
		newChromeCmd(app),
		newExportCmd(app),
		newImportCmd(app),
		newSessionCmd(app),
		newCACmd(app),
		newTrustCmd(app, true),
		newTrustCmd(app, false),
	)
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	app := &App{Out: os.Stdout, ErrOut: os.Stderr}
	root := NewRootCmd(app)
	err := root.Execute()
	if err != nil {
		code := codeFor(err)
		if app.Flags.JSON {
			_ = json.NewEncoder(os.Stderr).Encode(map[string]any{"error": err.Error(), "code": code})
		} else {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		return code
	}
	return ExitOK
}
