package cli

import (
	"net/url"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/hakanor/proxypilot/internal/daemon"
)

func newUICmd(app *App) *cobra.Command {
	var noOpen bool
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Open the web UI (starts the daemon if needed)",
		RunE: func(*cobra.Command, []string) error {
			dir := app.Config.DataDir
			st, err := daemon.Running(dir)
			if err != nil {
				args := []string{"start", "--data-dir", dir}
				if app.Flags.Config != "" {
					args = append(args, "--config", app.Flags.Config)
				}
				started, _, err := spawnDaemon(app, dir, args)
				if err != nil {
					return err
				}
				st = *started
			}
			tok := app.Flags.Token
			if tok == "" {
				if tok, err = daemon.ReadToken(dir); err != nil {
					return Exitf(ExitError, "read token: %v", err)
				}
			}
			u := "http://" + st.APIAddr + "/?token=" + url.QueryEscape(tok)
			opened := false
			if !noOpen {
				opened = openBrowser(u) == nil
			}
			if app.Flags.JSON {
				return app.PrintJSON(map[string]any{"url": u, "opened": opened, "pid": st.PID})
			}
			if !opened {
				app.Printf("%s\n", u)
			} else {
				app.Printf("Opened %s\n", "http://"+st.APIAddr+"/")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "only print the URL")
	return cmd
}

func openBrowser(u string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	return exec.Command(name, u).Start()
}
