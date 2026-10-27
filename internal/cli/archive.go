package cli

import (
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func writeExport(app *App, cmd *cobra.Command, kind string, f *filterFlags, ids, out string) error {
	c, err := app.apiClient()
	if err != nil {
		return err
	}
	q := f.values()
	if ids != "" {
		q.Set("ids", ids)
	}
	rc, err := c.Export(cmd.Context(), kind, q)
	if err != nil {
		return mapClientErr(err)
	}
	defer rc.Close()
	var w io.Writer = app.Out
	if out != "" && out != "-" {
		file, err := os.Create(out)
		if err != nil {
			return err
		}
		defer file.Close()
		w = file
	}
	n, err := io.Copy(w, rc)
	if err != nil {
		return err
	}
	if out != "" && out != "-" {
		app.Log.Info("exported", "file", out, "bytes", n)
	}
	if app.Flags.NoRedact {
		app.Log.Warn("secrets are NOT masked in this export")
	}
	return nil
}

func newExportCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "export", Short: "Export flows (HAR, session file); secrets masked unless --no-redact"}
	var f filterFlags
	var ids, out string
	har := &cobra.Command{
		Use:     "har",
		Short:   "Export as HAR 1.2 (Chrome DevTools, Proxyman, Charles can open it)",
		Example: "  proxypilot export har --since 10m > out.har\n  proxypilot export har --host api.example.com -o api.har",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return writeExport(app, cmd, "har", &f, ids, out)
		},
	}
	f.add(har, true)
	har.Flags().StringVar(&ids, "ids", "", "only these flow ids, comma-separated")
	har.Flags().StringVarP(&out, "output", "o", "", "write to a file instead of stdout")
	cmd.AddCommand(har)
	return cmd
}

func newImportCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "import", Short: "Import a HAR or session file into a new, separate session"}
	cmd.AddCommand(&cobra.Command{
		Use:   "har <file>",
		Short: "Import a HAR file (source=import)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return importFile(app, cmd, args[0])
		},
	})
	return cmd
}

func importFile(app *App, cmd *cobra.Command, path string) error {
	c, err := app.apiClient()
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	res, err := c.Import(cmd.Context(), file)
	if err != nil {
		return mapClientErr(err)
	}
	if app.Flags.JSON {
		return app.PrintJSON(res)
	}
	app.Printf("Imported %d flows into session %s", res.Imported, res.Session)
	if res.Skipped > 0 {
		app.Printf(" (%d skipped)", res.Skipped)
	}
	app.Printf("\nList them with: proxypilot list --source import\n")
	return nil
}

func newSessionCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "session", Short: "Save or open .proxypilot session files (flows + bodies in one file)"}
	var f filterFlags
	var ids string
	save := &cobra.Command{
		Use:   "save <file.proxypilot>",
		Short: "Save flows and their bodies to one file (secrets masked unless --no-redact)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			if !strings.HasSuffix(path, ".proxypilot") {
				path += ".proxypilot"
			}
			return writeExport(app, cmd, "session", &f, ids, path)
		},
	}
	f.add(save, true)
	save.Flags().StringVar(&ids, "ids", "", "only these flow ids, comma-separated")
	open := &cobra.Command{
		Use:   "open <file.proxypilot>",
		Short: "Open a session file as a new, separate session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return importFile(app, cmd, args[0])
		},
	}
	cmd.AddCommand(save, open)
	return cmd
}
