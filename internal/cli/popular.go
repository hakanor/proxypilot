package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newPopularCmd(app *App) *cobra.Command {
	var limit, paths int
	cmd := &cobra.Command{
		Use:   "popular",
		Short: "Hosts you work on most (opened, pinned, given a rule), with their top paths",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			hosts, err := c.Popular(cmd.Context(), limit, paths)
			if err != nil {
				return mapClientErr(err)
			}
			if app.Flags.JSON {
				return app.PrintJSON(map[string]any{"hosts": hosts})
			}
			if len(hosts) == 0 {
				app.Printf("Nothing yet. Open, pin or add a rule for a request and its host shows up here.\n")
				return nil
			}
			tw := tabwriter.NewWriter(app.Out, 0, 0, 2, ' ', 0)
			for _, h := range hosts {
				fmt.Fprintf(tw, "%s\t×%d\n", h.Host, h.Hits)
				for _, p := range h.Paths {
					fmt.Fprintf(tw, "  %s\t×%d\n", truncate(p.Path, 70), p.Hits)
				}
			}
			return tw.Flush()
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 10, "how many hosts")
	cmd.Flags().IntVar(&paths, "paths", 5, "paths per host")
	cmd.AddCommand(&cobra.Command{
		Use:   "clear",
		Short: "Forget the popular list",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			if err := c.ClearPopular(cmd.Context()); err != nil {
				return mapClientErr(err)
			}
			app.Printf("Popular list cleared.\n")
			return nil
		},
	})
	return cmd
}
