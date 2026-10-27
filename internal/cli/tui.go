package cli

import (
	"github.com/spf13/cobra"

	"github.com/hakanor/proxypilot/internal/tui"
)

func newTUICmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Terminal UI: live traffic, detail, repeat, breakpoints (? for keys)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			if _, err := c.Status(cmd.Context()); err != nil {
				return mapClientErr(err)
			}
			return tui.Run(cmd.Context(), c)
		},
	}
}
