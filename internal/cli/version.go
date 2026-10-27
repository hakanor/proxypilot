package cli

import (
	"runtime"

	"github.com/spf13/cobra"

	"github.com/hakanor/proxypilot/internal/version"
)

func newVersionCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(*cobra.Command, []string) error {
			info := map[string]string{
				"version": version.Version,
				"commit":  version.Commit,
				"date":    version.Date,
				"go":      runtime.Version(),
				"os":      runtime.GOOS + "/" + runtime.GOARCH,
			}
			if app.Flags.JSON {
				return app.PrintJSON(info)
			}
			app.Printf("proxypilot %s (commit %s, built %s, %s, %s)\n", info["version"], info["commit"], info["date"], info["go"], info["os"])
			return nil
		},
	}
}
