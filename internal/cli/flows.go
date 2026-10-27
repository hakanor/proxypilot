package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/hakanor/proxypilot/internal/api"
	"github.com/hakanor/proxypilot/internal/client"
	"github.com/hakanor/proxypilot/internal/store"
)

// apiClient builds a client for the running daemon; redaction is on unless --no-redact.
func (a *App) apiClient() (*client.Client, error) {
	if a.Flags.NoRedact {
		fmt.Fprintln(a.ErrOut, "warning: --no-redact: secrets (tokens, cookies, passwords) will be printed in clear text")
	}
	c, err := client.New(client.Options{DataDir: a.Config.DataDir, APIAddr: a.Flags.APIAddr, Token: a.Flags.Token, Redact: !a.Flags.NoRedact})
	return c, mapClientErr(err)
}

// mapClientErr applies the exit code contract to API errors.
func mapClientErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, client.ErrNoDaemon):
		return Exit(ExitNoDaemon, err)
	case errors.Is(err, client.ErrUnauthorized):
		return Exit(ExitUnauthorized, err)
	case errors.Is(err, client.ErrNotFound):
		return Exit(ExitNotFound, err)
	case errors.Is(err, context.DeadlineExceeded):
		return Exit(ExitTimeout, err)
	}
	var ae *client.APIError
	if errors.As(err, &ae) && ae.Status == 400 {
		return Exit(ExitUsage, err)
	}
	return err
}

// filterFlags are shared by list, tail and wait.
type filterFlags struct {
	host, method, status, ctype, source, since, gqlOp string
	errorsOnly                                        bool
}

func (f *filterFlags) add(cmd *cobra.Command, withSince bool) {
	fl := cmd.Flags()
	fl.StringVar(&f.host, "host", "", "host, wildcards allowed (*.example.com)")
	fl.StringVar(&f.method, "method", "", "methods, comma-separated (GET,POST)")
	fl.StringVar(&f.status, "status", "", "status: 2xx | 4xx,5xx | >=500 | 404 | 400-499")
	fl.StringVar(&f.ctype, "type", "", "response content type contains (json, image)")
	fl.StringVar(&f.source, "source", "", "proxy | cdp | composer | import")
	fl.BoolVar(&f.errorsOnly, "errors", false, "only failed requests (transport errors, GraphQL errors)")
	fl.StringVar(&f.gqlOp, "gql-op", "", "GraphQL operation name")
	if withSince {
		fl.StringVar(&f.since, "since", "", "only newer than a duration (5m) or RFC 3339 time")
	}
}

func (f *filterFlags) values() url.Values {
	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("host", f.host)
	set("method", f.method)
	set("status", f.status)
	set("type", f.ctype)
	set("source", f.source)
	set("since", f.since)
	set("gql_op", f.gqlOp)
	if f.errorsOnly {
		q.Set("has_error", "true")
	}
	return q
}

func (a *App) printFlowTable(flows []*store.Flow) {
	if len(flows) == 0 {
		a.Printf("No flows match. Is traffic going through the proxy? See `proxypilot status`.\n")
		return
	}
	tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, a.paint(ansiDim, "ID\tMETHOD\tSTATUS\tREQUEST\tSIZE\tTIME\tAGE"))
	for _, f := range flows {
		size := formatBytes(f.BytesDown)
		if f.State == store.StateTunneled {
			size = "↑" + formatBytes(f.BytesUp) + " ↓" + formatBytes(f.BytesDown)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", f.ID, f.Method, a.statusText(f.Status, f.State, f.ErrorKind),
			truncate(hostPath(f.Host, f.Path, f.URL), 64), size, formatMs(f.Timing.Total), formatAge(f.CreatedAt))
	}
	tw.Flush()
}

func newListCmd(app *App) *cobra.Command {
	var ff filterFlags
	var limit int
	var cursor string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List captured flows, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			q := ff.values()
			q.Set("limit", fmt.Sprint(limit))
			if cursor != "" {
				q.Set("cursor", cursor)
			}
			page, err := c.ListFlows(cmd.Context(), q)
			if err != nil {
				return mapClientErr(err)
			}
			if app.Flags.JSON {
				return app.PrintJSON(page)
			}
			app.printFlowTable(page.Flows)
			if page.HasMore {
				app.Printf("%s\n", app.paint(ansiDim, "more: --cursor "+page.NextCursor))
			}
			return nil
		},
	}
	ff.add(cmd, true)
	cmd.Flags().IntVar(&limit, "limit", 20, "max flows (≤ 500)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "continue from a previous page")
	return cmd
}

func newSearchCmd(app *App) *cobra.Command {
	var in string
	var limit int
	cmd := &cobra.Command{
		Use:   "search <text>",
		Short: "Search flows by URL, body or headers",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			q := url.Values{"q": {args[0]}, "in": {in}, "limit": {fmt.Sprint(limit)}}
			page, err := c.ListFlows(cmd.Context(), q)
			if err != nil {
				return mapClientErr(err)
			}
			if app.Flags.JSON {
				return app.PrintJSON(page)
			}
			app.printFlowTable(page.Flows)
			return nil
		},
	}
	cmd.Flags().StringVar(&in, "in", "url", "where to search: url | body | headers")
	cmd.Flags().IntVar(&limit, "limit", 20, "max flows (≤ 500)")
	return cmd
}

// ShowJSON is the `show --json` schema.
type ShowJSON struct {
	client.FlowDetail
	Body     *ShowBodies     `json:"body,omitempty"`
	Messages []api.WSMessage `json:"messages,omitempty"` // --messages
}

type ShowBodies struct {
	Req  *api.BodyResponse `json:"req,omitempty"`
	Resp *api.BodyResponse `json:"resp,omitempty"`
}

func newShowCmd(app *App) *cobra.Command {
	var withBody, raw, messages bool
	var side, maxBody string
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one flow (headers; --body for bodies)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if side != "req" && side != "resp" && side != "both" {
				return Exitf(ExitUsage, "--side must be req, resp or both")
			}
			max, err := parseBytes(maxBody)
			if err != nil {
				return Exit(ExitUsage, err)
			}
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if raw {
				s := side
				if s == "both" {
					s = "resp"
				}
				rc, err := c.RawBody(ctx, args[0], s)
				if err != nil {
					return mapClientErr(err)
				}
				defer rc.Close()
				_, err = io.Copy(app.Out, rc)
				return err
			}
			d, err := c.GetFlow(ctx, args[0])
			if err != nil {
				return mapClientErr(err)
			}
			out := ShowJSON{FlowDetail: d}
			if withBody {
				out.Body = &ShowBodies{}
				for _, s := range []string{"req", "resp"} {
					if side != "both" && side != s {
						continue
					}
					b, err := c.GetBody(ctx, args[0], s, max)
					if err != nil {
						return mapClientErr(err)
					}
					if s == "req" {
						out.Body.Req = &b
					} else {
						out.Body.Resp = &b
					}
				}
			}
			if messages {
				for after := int64(0); ; {
					p, err := c.Messages(ctx, args[0], after, 1000)
					if err != nil {
						return mapClientErr(err)
					}
					out.Messages = append(out.Messages, p.Messages...)
					if len(p.Messages) == 0 || p.Next == after {
						break
					}
					after = p.Next
				}
			}
			if app.Flags.JSON {
				return app.PrintJSON(out)
			}
			app.printShow(out, side)
			if messages {
				app.printMessages(out.Messages, max)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&messages, "messages", false, "include WebSocket messages")
	cmd.Flags().BoolVar(&withBody, "body", false, "include bodies")
	cmd.Flags().StringVar(&side, "side", "both", "req | resp | both")
	cmd.Flags().StringVar(&maxBody, "max-body", "4kb", "truncate bodies to this size")
	cmd.Flags().BoolVar(&raw, "raw", false, "write the full decoded body to stdout (one side, default resp)")
	return cmd
}

func (a *App) printShow(s ShowJSON, side string) {
	f := s.Flow
	a.Printf("%s %s  %s %s\n", a.paint(ansiBold, f.Method), f.URL, a.statusText(f.Status, f.State, f.ErrorKind), a.paint(ansiDim, "("+f.State+")"))
	a.Printf("%s %s\n", a.paint(ansiDim, "ID     "), f.ID)
	client := f.ClientAddr
	if f.ClientLabel != "" {
		client += " (" + f.ClientLabel + ")"
	}
	a.Printf("%s %s · %s\n", a.paint(ansiDim, "Client "), client, f.CreatedAt.Format(time.RFC3339))
	if f.ErrorKind != "" {
		a.Printf("%s %s: %s\n", a.paint(ansiRed, "Error  "), f.ErrorKind, f.ErrorMsg)
	}
	t := f.Timing
	a.Printf("%s dns %s · connect %s · tls %s · ttfb %s · total %s\n", a.paint(ansiDim, "Timing "),
		formatMs(t.DNS), formatMs(t.Connect), formatMs(t.TLS), formatMs(t.TTFB), formatMs(t.Total))
	bytes := fmt.Sprintf("↑ %s  ↓ %s", formatBytes(f.BytesUp), formatBytes(f.BytesDown))
	if r := s.Bodies.Resp; r != nil && r.DecodedSize != f.BytesDown {
		bytes += fmt.Sprintf("  (response decoded %s", formatBytes(r.DecodedSize))
		if r.Encoding != "" {
			bytes += ", " + r.Encoding
		}
		bytes += ")"
	}
	a.Printf("%s %s\n", a.paint(ansiDim, "Bytes  "), bytes)

	printHeaders := func(title string, hs []store.Header) {
		if len(hs) == 0 {
			return
		}
		a.Printf("\n%s\n", a.paint(ansiBold, title))
		for _, h := range hs {
			a.Printf("  %s: %s\n", a.paint(ansiDim, h.Name), h.Value)
		}
	}
	if side != "resp" {
		printHeaders("Request headers", f.ReqHeaders)
	}
	if side != "req" {
		printHeaders("Response headers", f.RespHeaders)
	}
	if s.Body == nil {
		return
	}
	printBody := func(title string, b *api.BodyResponse) {
		if b == nil {
			return
		}
		if b.Size == 0 {
			a.Printf("\n%s\n", a.paint(ansiDim, "— "+title+": empty —"))
			return
		}
		note := fmt.Sprintf("%s of %s", formatBytes(int64(b.Returned)), formatBytes(b.Size))
		if b.Truncated {
			note += ", truncated — use --max-body or --raw"
		}
		a.Printf("\n%s\n", a.paint(ansiDim, "— "+title+" ("+note+") —"))
		if b.Encoding == "base64" {
			raw, _ := base64.StdEncoding.DecodeString(b.Data)
			a.Printf("[binary %s, %d bytes shown as hex]\n% x\n", b.ContentType, len(raw), raw[:min(len(raw), 256)])
			return
		}
		data := b.Data
		if strings.Contains(b.ContentType, "json") {
			var v any
			if json.Unmarshal([]byte(data), &v) == nil {
				if pretty, err := json.MarshalIndent(v, "", "  "); err == nil {
					data = string(pretty)
				}
			}
		}
		a.Printf("%s\n", data)
	}
	printBody("request body", s.Body.Req)
	printBody("response body", s.Body.Resp)
}

func newSummaryCmd(app *App) *cobra.Command {
	var since string
	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Short traffic summary: counts, failures, slowest request",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			q := since
			if q == "all" {
				q = ""
			}
			s, err := c.Summary(cmd.Context(), q)
			if err != nil {
				return mapClientErr(err)
			}
			if app.Flags.JSON {
				return app.PrintJSON(s)
			}
			label := "All traffic"
			if q != "" {
				label = "Last " + q
			}
			failed := 0
			for _, g := range s.Failures {
				failed += g.Count
			}
			app.Printf("%s: %d requests, %d failed, ↓ %s ↑ %s\n", label, s.Total, failed, formatBytes(s.BytesDown), formatBytes(s.BytesUp))
			tw := tabwriter.NewWriter(app.Out, 0, 0, 2, ' ', 0)
			for _, g := range s.Failures {
				what := app.statusText(g.Status, "", "")
				if g.ErrorKind != "" {
					what = app.paint(ansiRed, g.ErrorKind)
				}
				fmt.Fprintf(tw, "  %s\t%s\t%s\t×%d\n", g.Method, truncate(g.Host+g.Path, 60), what, g.Count)
			}
			tw.Flush()
			if s.Slowest != nil {
				app.Printf("Slowest: %s %s %s\n", s.Slowest.Method, truncate(s.Slowest.URL, 70), formatMs(s.Slowest.TotalMs))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&since, "since", "5m", "window: duration (5m, 1h), RFC 3339, or all")
	return cmd
}

func newClearCmd(app *App) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Delete captured flows and start a new session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !yes {
				return Exitf(ExitUsage, "this deletes all captured flows; re-run with --yes")
			}
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			if err := c.Clear(cmd.Context()); err != nil {
				return mapClientErr(err)
			}
			if app.Flags.JSON {
				return app.PrintJSON(map[string]bool{"cleared": true})
			}
			app.Printf("Cleared.\n")
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm")
	return cmd
}

func signalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

func newTailCmd(app *App) *cobra.Command {
	var ff filterFlags
	var evs string
	cmd := &cobra.Command{
		Use:   "tail",
		Short: "Stream flows live (--json: NDJSON)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			ctx, stop := signalContext(cmd.Context())
			defer stop()
			q := ff.values()
			q.Set("events", evs)
			enc := json.NewEncoder(app.Out)
			err = c.Stream(ctx, q, func(e client.StreamEvent) bool {
				if app.Flags.JSON {
					_ = enc.Encode(e)
					return true
				}
				if e.Type == "dropped" {
					fmt.Fprintf(app.ErrOut, "… %d events dropped (output too slow)\n", e.Count)
					return true
				}
				if r, ok := e.Flow(); ok {
					if ff.errorsOnly && r.ErrorKind == "" {
						return true
					}
					app.Printf("%s  %-7s %s  %s  %s  %s\n", e.Time.Local().Format("15:04:05"), r.Method, app.statusText(r.Status, r.State, r.ErrorKind),
						truncate(hostPath(r.Host, r.Path, r.URL), 70), formatBytes(r.BytesDown), formatMs(r.TotalMs))
					return true
				}
				app.Printf("%s  %s %s\n", e.Time.Local().Format("15:04:05"), e.Type, string(e.Data))
				return true
			})
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return mapClientErr(err)
		},
	}
	ff.add(cmd, false)
	cmd.Flags().StringVar(&evs, "events", "flow.completed,flow.failed", "event patterns (flow.*, breakpoint.*, suggestion)")
	return cmd
}

// urlMatcher: "*" wildcards, otherwise substring; case-insensitive.
func urlMatcher(pattern string) func(string) bool { return client.URLMatcher(pattern) }

func newWaitCmd(app *App) *cobra.Command {
	var urlPat, method, status, since string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Wait for the first completed flow that matches, print it and exit",
		Long: "Wait blocks until a matching flow completes (exit 0) or the timeout passes (exit 5).\n" +
			"Typical agent loop: start waiting, trigger the action in the app, then inspect the printed flow.\n" +
			"--since also accepts flows that completed shortly before wait started.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if urlPat == "" {
				return Exitf(ExitUsage, "--url is required (substring or * pattern)")
			}
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			ctx, stop := signalContext(ctx)
			defer stop()
			found, err := c.WaitFlow(ctx, client.WaitOptions{URL: urlPat, Method: method, Status: status, Since: since})
			if errors.Is(err, client.ErrWaitTimeout) {
				if app.Flags.JSON {
					_ = app.PrintJSON(map[string]any{"matched": false, "timeout": timeout.String()})
				}
				return Exitf(ExitTimeout, "no matching flow within %s", timeout)
			}
			if errors.Is(err, context.Canceled) {
				return Exitf(ExitError, "interrupted")
			}
			if err != nil {
				return mapClientErr(err)
			}
			d, err := c.GetFlow(context.Background(), found)
			if err != nil {
				return mapClientErr(err)
			}
			if app.Flags.JSON {
				return app.PrintJSON(map[string]any{"matched": true, "flow": d})
			}
			app.printShow(ShowJSON{FlowDetail: d}, "both")
			return nil
		},
	}
	cmd.Flags().StringVar(&urlPat, "url", "", "URL substring or * pattern (required)")
	cmd.Flags().StringVar(&method, "method", "", "method(s), comma-separated")
	cmd.Flags().StringVar(&status, "status", "", "status filter (2xx, >=400, 404)")
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "give up after this long (exit 5)")
	cmd.Flags().StringVar(&since, "since", "", "also match flows completed within this window before waiting (e.g. 10s)")
	return cmd
}

func (a *App) printMessages(ms []api.WSMessage, max int64) {
	a.Printf("\n%s (%d)\n", a.paint(ansiBold, "WebSocket messages"), len(ms))
	for _, m := range ms {
		arrow := a.paint(ansiGreen, "↑")
		if m.Dir == "down" {
			arrow = a.paint(ansiYellow, "↓")
		}
		body := ""
		switch {
		case m.Text != nil:
			body = *m.Text
		case m.Base64 != "":
			body = "(binary, base64) " + m.Base64
		}
		if max > 0 && int64(len(body)) > max {
			body = body[:max] + "…"
		}
		a.Printf("%s %s %-6s %8s  %s\n", a.paint(ansiDim, m.At.Format("15:04:05.000")), arrow, m.Opcode, formatBytes(m.Size), body)
	}
}
