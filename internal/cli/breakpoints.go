package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hakanor/proxypilot/internal/breakpoints"
	"github.com/hakanor/proxypilot/internal/client"
)

func newBreakpointCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "breakpoint",
		Aliases: []string{"bp"},
		Short:   "Inspect and resolve paused flows",
		Long: "Create breakpoints with `proxypilot rule add breakpoint`. Matching requests (or responses) wait here\n" +
			"until someone decides: continue unchanged, continue with edits, or abort. On timeout they continue.",
	}
	cmd.AddCommand(newBpListCmd(app), newBpWaitCmd(app), newBpResumeCmd(app), newBpEditCmd(app), newBpExtendCmd(app))
	return cmd
}

func printPaused(app *App, p *breakpoints.Paused) {
	left := time.Until(p.ExpiresAt).Round(time.Second)
	app.Printf("%s  %s  %s %s  (%s left, rule %q, owner %s)\n", p.ID, strings.ToUpper(p.Phase[:3]), p.Request.Method, p.Request.URL, left, p.RuleName, p.Owner)
}

func newBpListCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List paused flows",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			list, err := c.ListBreakpoints(cmd.Context())
			if err != nil {
				return mapClientErr(err)
			}
			if app.Flags.JSON {
				return app.PrintJSON(map[string]any{"breakpoints": list})
			}
			if len(list) == 0 {
				app.Printf("Nothing is paused.\n")
			}
			for _, p := range list {
				printPaused(app, p)
			}
			return nil
		},
	}
}

func newBpWaitCmd(app *App) *cobra.Command {
	var timeout time.Duration
	var urlPat, phase string
	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Block until a flow is paused, then print it (exit 5 on timeout)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			p, err := c.WaitBreakpoint(cmd.Context(), timeout, urlPat, phase)
			var ae *client.APIError
			if errors.As(err, &ae) && ae.Status == 408 {
				if app.Flags.JSON {
					_ = app.PrintJSON(map[string]any{"paused": false, "timeout": timeout.String()})
				}
				return Exitf(ExitTimeout, "no breakpoint hit within %s", timeout)
			}
			if err != nil {
				return mapClientErr(err)
			}
			if app.Flags.JSON {
				return app.PrintJSON(p)
			}
			printPaused(app, p)
			app.Printf("Resolve with: proxypilot breakpoint resume %s [--status 500] [--set-header K:V] [--body @file] [--abort]\n", p.ID)
			return nil
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "how long to wait")
	cmd.Flags().StringVar(&urlPat, "url", "", "only flows whose URL contains this (or matches a * pattern)")
	cmd.Flags().StringVar(&phase, "phase", "", "request or response")
	return cmd
}

func readBodyArg(v string) (string, error) {
	if strings.HasPrefix(v, "@") {
		b, err := os.ReadFile(v[1:])
		return string(b), err
	}
	return v, nil
}

func setHeader(hs []breakpoints.Header, name, value string) []breakpoints.Header {
	out := hs[:0:0]
	for _, h := range hs {
		if !strings.EqualFold(h.Name, name) {
			out = append(out, h)
		}
	}
	return append(out, breakpoints.Header{Name: name, Value: value})
}

func removeHeader(hs []breakpoints.Header, name string) []breakpoints.Header {
	out := hs[:0:0]
	for _, h := range hs {
		if !strings.EqualFold(h.Name, name) {
			out = append(out, h)
		}
	}
	return out
}

func newBpResumeCmd(app *App) *cobra.Command {
	var (
		status              int
		setHeaders, removes []string
		body, method, url   string
		abort               bool
	)
	cmd := &cobra.Command{
		Use:   "resume <id>",
		Short: "Continue a paused flow, optionally with edits, or abort it",
		Example: "  proxypilot breakpoint resume bp_01… \n" +
			"  proxypilot breakpoint resume bp_01… --status 500 --body '{\"error\":\"boom\"}'\n" +
			"  proxypilot breakpoint resume bp_01… --set-header 'Authorization: Bearer test' --body @req.json\n" +
			"  proxypilot breakpoint resume bp_01… --abort",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			id := args[0]
			res := breakpoints.Resolution{Action: breakpoints.ActionContinue, By: os.Getenv("PROXYPILOT_OWNER")}
			edits := status != 0 || len(setHeaders) > 0 || len(removes) > 0 || cmd.Flags().Changed("body") || method != "" || url != ""
			switch {
			case abort && edits:
				return Exitf(ExitUsage, "--abort cannot be combined with edits")
			case abort:
				res.Action = breakpoints.ActionAbort
			case edits:
				// Edit the unredacted original; a masked header must never be sent upstream.
				p, err := c.GetBreakpoint(cmd.Context(), id, true)
				if err != nil {
					return mapClientErr(err)
				}
				res.Action = breakpoints.ActionModify
				if p.Phase == breakpoints.PhaseRequest {
					if status != 0 {
						return Exitf(ExitUsage, "--status only applies in the response phase")
					}
					r := p.Request
					if method != "" {
						r.Method = strings.ToUpper(method)
					}
					if url != "" {
						r.URL = url
					}
					hs, err := editHeaders(r.Headers, setHeaders, removes)
					if err != nil {
						return err
					}
					r.Headers = hs
					if cmd.Flags().Changed("body") {
						if r.Body, err = readBodyArg(body); err != nil {
							return err
						}
						r.BodyBase64 = ""
					}
					res.Request = &r
				} else {
					if method != "" || url != "" {
						return Exitf(ExitUsage, "--method/--url only apply in the request phase")
					}
					r := *p.Response
					if status != 0 {
						r.Status = status
					}
					hs, err := editHeaders(r.Headers, setHeaders, removes)
					if err != nil {
						return err
					}
					r.Headers = hs
					if cmd.Flags().Changed("body") {
						if r.Body, err = readBodyArg(body); err != nil {
							return err
						}
						r.BodyBase64 = ""
					}
					res.Response = &r
				}
			}
			if err := c.ResumeBreakpoint(cmd.Context(), id, res); err != nil {
				var ae *client.APIError
				if errors.As(err, &ae) && ae.Status == 409 {
					return Exitf(ExitError, "%s was already resolved", id)
				}
				return mapClientErr(err)
			}
			if app.Flags.JSON {
				return app.PrintJSON(map[string]any{"resumed": true, "id": id, "action": res.Action})
			}
			app.Printf("%s: %s\n", id, res.Action)
			return nil
		},
	}
	f := cmd.Flags()
	f.IntVar(&status, "status", 0, "response status (response phase)")
	f.StringSliceVar(&setHeaders, "set-header", nil, "set a header, Name:value (repeatable)")
	f.StringSliceVar(&removes, "remove-header", nil, "remove a header (repeatable)")
	f.StringVar(&body, "body", "", "replace the body (text, or @file)")
	f.StringVar(&method, "method", "", "change the method (request phase)")
	f.StringVar(&url, "url", "", "change the URL (request phase, same scheme)")
	f.BoolVar(&abort, "abort", false, "abort: 502 in the request phase, connection closed in the response phase")
	return cmd
}

func editHeaders(hs []breakpoints.Header, set, remove []string) ([]breakpoints.Header, error) {
	for _, r := range remove {
		hs = removeHeader(hs, strings.TrimSpace(r))
	}
	m, err := parseHeaderFlags(set)
	if err != nil {
		return nil, err
	}
	for k, v := range m {
		hs = setHeader(hs, k, v)
	}
	return hs, nil
}

// newBpEditCmd opens the editable part as JSON in $EDITOR; saving resumes with the edits.
func newBpEditCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit a paused request/response as JSON in $EDITOR, then resume with it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			p, err := c.GetBreakpoint(cmd.Context(), args[0], true)
			if err != nil {
				return mapClientErr(err)
			}
			var doc any = p.Request
			if p.Phase == breakpoints.PhaseResponse {
				doc = p.Response
			}
			orig, _ := json.MarshalIndent(doc, "", "  ")
			tmp := filepath.Join(os.TempDir(), "proxypilot-"+p.ID+".json")
			if err := os.WriteFile(tmp, orig, 0o600); err != nil {
				return err
			}
			defer os.Remove(tmp)
			editor := os.Getenv("VISUAL")
			if editor == "" {
				editor = os.Getenv("EDITOR")
			}
			if editor == "" {
				editor = "vi"
			}
			parts := strings.Fields(editor)
			ed := exec.CommandContext(cmd.Context(), parts[0], append(parts[1:], tmp)...)
			ed.Stdin, ed.Stdout, ed.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := ed.Run(); err != nil {
				return Exitf(ExitError, "editor: %v", err)
			}
			edited, err := os.ReadFile(tmp)
			if err != nil {
				return err
			}
			res := breakpoints.Resolution{Action: breakpoints.ActionContinue, By: os.Getenv("PROXYPILOT_OWNER")}
			if !bytes.Equal(bytes.TrimSpace(edited), bytes.TrimSpace(orig)) {
				res.Action = breakpoints.ActionModify
				if p.Phase == breakpoints.PhaseRequest {
					res.Request = &breakpoints.Request{}
					err = json.Unmarshal(edited, res.Request)
				} else {
					res.Response = &breakpoints.Response{}
					err = json.Unmarshal(edited, res.Response)
				}
				if err != nil {
					return Exitf(ExitUsage, "invalid JSON: %v", err)
				}
			}
			if err := c.ResumeBreakpoint(cmd.Context(), p.ID, res); err != nil {
				return mapClientErr(err)
			}
			app.Printf("%s: %s\n", p.ID, res.Action)
			return nil
		},
	}
}

func newBpExtendCmd(app *App) *cobra.Command {
	var seconds int
	cmd := &cobra.Command{
		Use:   "extend <id>",
		Short: "Give a paused flow more time",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			p, err := c.ExtendBreakpoint(cmd.Context(), args[0], seconds)
			if err != nil {
				return mapClientErr(err)
			}
			if app.Flags.JSON {
				return app.PrintJSON(p)
			}
			printPaused(app, p)
			return nil
		},
	}
	cmd.Flags().IntVar(&seconds, "seconds", 30, "seconds to add")
	return cmd
}
