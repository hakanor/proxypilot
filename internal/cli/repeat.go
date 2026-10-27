package cli

import (
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hakanor/proxypilot/internal/api"
	"github.com/hakanor/proxypilot/internal/repeat"
	"github.com/hakanor/proxypilot/internal/store"
)

func (a *App) printSummary(s repeat.Summary) error {
	if a.Flags.JSON {
		return a.PrintJSON(s)
	}
	for _, r := range s.Results {
		switch {
		case r.Error != "":
			a.Printf("%s  %s\n", a.paint("31", "error"), r.Error)
		case r.Aborted:
			a.Printf("%s  aborted by a rule or breakpoint\n", r.FlowID)
		default:
			a.Printf("%s  %s  %.0f ms\n", r.FlowID, a.statusText(r.Status, store.StateCompleted, ""), r.Ms)
		}
	}
	if len(s.Results) > 1 {
		parts := []string{}
		for k, v := range s.ByStatus {
			parts = append(parts, k+"×"+strconv.Itoa(v))
		}
		a.Printf("%d requests, %s, avg %.0f ms\n", len(s.Results), strings.Join(parts, " "), s.AvgMs)
	}
	return nil
}

func newRepeatCmd(app *App) *cobra.Command {
	var setH, rmH, setQ []string
	var body, method, url string
	var count, conc int
	var bypass bool
	cmd := &cobra.Command{
		Use:   "repeat <id>",
		Short: "Send a recorded request again, optionally edited (results are recorded as source=composer)",
		Example: "  proxypilot repeat 01M5…\n" +
			"  proxypilot repeat 01M5… --set-header 'Authorization: Bearer other' --body @order.json\n" +
			"  proxypilot repeat 01M5… --count 10 --concurrency 5      # race / flakiness repro",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			in := api.RepeatRequest{Options: repeat.Options{Count: count, Concurrency: conc, BypassRules: bypass}}
			in.Method, in.URL = method, url
			if len(setH) > 0 {
				in.SetHeaders = map[string]string{}
				for _, h := range setH {
					k, v, ok := strings.Cut(h, ":")
					if !ok {
						return Exitf(ExitUsage, "header %q must look like Name: value", h)
					}
					in.SetHeaders[strings.TrimSpace(k)] = strings.TrimSpace(v)
				}
			}
			in.RemoveHeaders = rmH
			if len(setQ) > 0 {
				in.SetQuery = map[string]string{}
				for _, kv := range setQ {
					k, v, _ := strings.Cut(kv, "=")
					in.SetQuery[k] = v
				}
			}
			if cmd.Flags().Changed("body") {
				b, err := readBodyArg(body)
				if err != nil {
					return err
				}
				in.Body = &b
			}
			s, err := c.Repeat(cmd.Context(), args[0], in)
			if err != nil {
				return mapClientErr(err)
			}
			return app.printSummary(s)
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&setH, "set-header", nil, "set a header, 'Name: value' (repeatable)")
	f.StringSliceVar(&rmH, "remove-header", nil, "remove a header")
	f.StringSliceVar(&setQ, "set-query", nil, "set a query parameter, name=value")
	f.StringVar(&body, "body", "", "replace the body (text or @file)")
	f.StringVar(&method, "method", "", "change the method")
	f.StringVar(&url, "url", "", "change the URL")
	f.IntVar(&count, "count", 1, "send it this many times (max 50)")
	f.IntVar(&conc, "concurrency", 1, "how many at once (max 10)")
	f.BoolVar(&bypass, "bypass-rules", false, "skip rules (Map Local, rewrite, breakpoints)")
	return cmd
}

func newCurlCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "curl <id>",
		Short: "Print a recorded request as a cURL command (secrets masked unless --no-redact)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			s, binary, err := c.Curl(cmd.Context(), args[0])
			if err != nil {
				return mapClientErr(err)
			}
			if app.Flags.JSON {
				return app.PrintJSON(map[string]any{"curl": s, "binary_body": binary})
			}
			app.Printf("%s\n", s)
			if binary {
				app.Log.Warn("the body is binary; save it with `proxypilot show " + args[0] + " --raw --side req > body.bin`")
			}
			if !app.Flags.NoRedact {
				app.Log.Info("secrets are masked; use --no-redact for a command that works as is")
			}
			return nil
		},
	}
}

func newComposeCmd(app *App) *cobra.Command {
	var fromCurl, method, url, body string
	var headers []string
	var count, conc int
	var bypass bool
	cmd := &cobra.Command{
		Use:   "compose",
		Short: "Send a new request through ProxyPilot (from flags or a pasted cURL command)",
		Example: "  proxypilot compose --from-curl \"$(pbpaste)\"\n" +
			"  proxypilot compose --method POST --url https://api.x.com/v2/orders --header 'Content-Type: application/json' --body @order.json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			in := api.ComposeRequest{Curl: fromCurl, Options: repeat.Options{Count: count, Concurrency: conc, BypassRules: bypass}}
			if fromCurl == "" {
				if url == "" {
					return Exitf(ExitUsage, "--url or --from-curl is required")
				}
				in.Method, in.URL = method, url
				for _, h := range headers {
					k, v, ok := strings.Cut(h, ":")
					if !ok {
						return Exitf(ExitUsage, "header %q must look like Name: value", h)
					}
					in.Headers = append(in.Headers, store.Header{Name: strings.TrimSpace(k), Value: strings.TrimSpace(v)})
				}
				if cmd.Flags().Changed("body") {
					if in.Body, err = readBodyArg(body); err != nil {
						return err
					}
				}
			} else if strings.HasPrefix(fromCurl, "@") {
				b, err := os.ReadFile(fromCurl[1:])
				if err != nil {
					return err
				}
				in.Curl = string(b)
			}
			s, err := c.Compose(cmd.Context(), in)
			if err != nil {
				return mapClientErr(err)
			}
			return app.printSummary(s)
		},
	}
	f := cmd.Flags()
	f.StringVar(&fromCurl, "from-curl", "", "a cURL command (or @file)")
	f.StringVar(&method, "method", "GET", "method")
	f.StringVar(&url, "url", "", "absolute URL")
	f.StringSliceVar(&headers, "header", nil, "'Name: value' (repeatable)")
	f.StringVar(&body, "body", "", "body (text or @file)")
	f.IntVar(&count, "count", 1, "send it this many times (max 50)")
	f.IntVar(&conc, "concurrency", 1, "how many at once (max 10)")
	f.BoolVar(&bypass, "bypass-rules", false, "skip rules")
	return cmd
}
