package cli

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hakanor/proxypilot/internal/api"
	"github.com/hakanor/proxypilot/internal/rules"
)

func newRuleCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rule",
		Short: "Manage rules: Map Local, Map Remote, Rewrite, Breakpoint",
	}
	cmd.AddCommand(newRuleAddCmd(app), newRuleListCmd(app), newRuleShowCmd(app), newRuleRmCmd(app), newRuleToggleCmd(app), newRuleClearCmd(app))
	return cmd
}

func newRuleAddCmd(app *App) *cobra.Command {
	add := &cobra.Command{Use: "add", Short: "Add a rule"}
	var (
		urlPat, name, file, dir, body, bodyFile, mode, owner, ttl, from, gqlOp string
		methods, headers, matchHeaders                                         []string
		status, priority                                                       int
		regex, disabled                                                        bool
	)
	ml := &cobra.Command{
		Use:   "map-local",
		Short: "Answer matching requests with a local file, folder or inline body",
		Example: "  proxypilot rule add map-local --url 'https://api.x.com/v2/feed*' --file mock.json\n" +
			"  proxypilot rule add map-local --url 'https://cdn.x.com/js/*' --dir ./dist\n" +
			"  proxypilot rule add map-local --url '*/checkout' --method POST --status 500 --body '{\"error\":\"boom\"}' --ttl 10m\n" +
			"  proxypilot rule add map-local --from <flow-id>     # copy a recorded response to a file and map it",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			if owner == "" {
				owner = os.Getenv("PROXYPILOT_OWNER") // agents set e.g. agent:claude
			}
			if from != "" {
				r, path, err := c.MapLocalFromFlow(cmd.Context(), from, owner, ttl)
				if err != nil {
					return mapClientErr(err)
				}
				if app.Flags.JSON {
					return app.PrintJSON(map[string]any{"rule": r, "file": path})
				}
				app.Printf("Created %s: %s\nEdit %s to change the response.\n", r.ID, r.Name, path)
				return nil
			}
			spec := map[string]any{}
			switch {
			case file != "":
				abs, err := filepath.Abs(file)
				if err != nil {
					return err
				}
				spec["file"] = abs
			case dir != "":
				abs, err := filepath.Abs(dir)
				if err != nil {
					return err
				}
				spec["dir"] = abs
			case bodyFile != "":
				b, err := os.ReadFile(bodyFile)
				if err != nil {
					return err
				}
				spec["body_base64"] = base64.StdEncoding.EncodeToString(b)
			case cmd.Flags().Changed("body"):
				spec["body"] = body
			default:
				return Exitf(ExitUsage, "one of --file, --dir, --body, --body-file or --from is required")
			}
			if status != 0 {
				spec["status"] = status
			}
			if mode != "" {
				spec["mode"] = mode
			}
			if len(headers) > 0 {
				h, err := parseHeaderFlags(headers)
				if err != nil {
					return err
				}
				spec["headers"] = h
			}
			mh, err := parseHeaderFlags(matchHeaders)
			if err != nil {
				return err
			}
			raw, _ := json.Marshal(spec)
			in := api.RuleInput{
				Rule: rules.Rule{
					Name: name, Type: rules.TypeMapLocal, Enabled: !disabled, Owner: owner, Priority: priority,
					Match: rules.Matcher{URL: urlPat, Regex: regex, Methods: methods, Headers: mh, GraphQLOperation: gqlOp}, Spec: raw,
				},
				TTL: ttl,
			}
			r, err := c.AddRule(cmd.Context(), in)
			if err != nil {
				return mapClientErr(err)
			}
			if app.Flags.JSON {
				return app.PrintJSON(r)
			}
			app.Printf("Created %s: %s\n", r.ID, r.Name)
			return nil
		},
	}
	f := ml.Flags()
	f.StringVar(&urlPat, "url", "", "URL pattern (wildcard *, or a regex with --regex); empty matches everything")
	f.BoolVar(&regex, "regex", false, "treat --url as a regular expression")
	f.StringSliceVar(&methods, "method", nil, "HTTP methods (repeatable)")
	f.StringSliceVar(&matchHeaders, "match-header", nil, "only requests with this header, Name:value (wildcards allowed)")
	f.StringVar(&gqlOp, "gql-op", "", "only this GraphQL operation (name, * wildcards)")
	f.StringVar(&file, "file", "", "respond with this file (read on every request)")
	f.StringVar(&dir, "dir", "", "map the part of the URL after '*' to files in this folder")
	f.StringVar(&body, "body", "", "respond with this inline body")
	f.StringVar(&bodyFile, "body-file", "", "embed this file's current content in the rule")
	f.StringVar(&from, "from", "", "create the rule from a recorded flow's response")
	f.IntVar(&status, "status", 0, "status code (default 200)")
	f.StringSliceVar(&headers, "header", nil, "response header, Name:value (repeatable)")
	f.StringVar(&mode, "mode", "", "full (default, never contacts the server) or body_only")
	f.StringVar(&name, "name", "", "rule name")
	f.StringVar(&owner, "owner", "", "user (default) or agent:<name>; env PROXYPILOT_OWNER")
	f.StringVar(&ttl, "ttl", "", "delete the rule after this long, e.g. 30m")
	f.IntVar(&priority, "priority", 0, "higher runs first")
	f.BoolVar(&disabled, "disabled", false, "create it disabled")
	add.AddCommand(ml, newRuleAddBreakpointCmd(app), newRuleAddRewriteCmd(app), newRuleAddMapRemoteCmd(app))
	return add
}

func newRuleAddBreakpointCmd(app *App) *cobra.Command {
	var (
		urlPat, name, phase, timeout, onTimeout, owner, ttl, gqlOp string
		methods, matchHeaders                                      []string
		regex, disabled                                            bool
		priority                                                   int
	)
	cmd := &cobra.Command{
		Use:   "breakpoint",
		Short: "Pause matching requests and/or responses until someone decides",
		Example: "  proxypilot rule add breakpoint --url '*/checkout' --phase response --timeout 2m\n" +
			"  proxypilot breakpoint wait --json   # then: proxypilot breakpoint resume <id> --status 500",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			if owner == "" {
				owner = os.Getenv("PROXYPILOT_OWNER")
			}
			mh, err := parseHeaderFlags(matchHeaders)
			if err != nil {
				return err
			}
			spec, _ := json.Marshal(map[string]string{"timeout": timeout, "on_timeout": onTimeout})
			r, err := c.AddRule(cmd.Context(), api.RuleInput{
				Rule: rules.Rule{
					Name: name, Type: rules.TypeBreakpoint, Enabled: !disabled, Owner: owner, Priority: priority,
					Phase: rules.Phase(phase), Match: rules.Matcher{URL: urlPat, Regex: regex, Methods: methods, Headers: mh, GraphQLOperation: gqlOp}, Spec: spec,
				},
				TTL: ttl,
			})
			if err != nil {
				return mapClientErr(err)
			}
			if app.Flags.JSON {
				return app.PrintJSON(r)
			}
			app.Printf("Created %s: %s (%s phase)\n", r.ID, r.Name, r.Phase)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&urlPat, "url", "", "URL pattern (wildcard *, or a regex with --regex)")
	f.BoolVar(&regex, "regex", false, "treat --url as a regular expression")
	f.StringSliceVar(&methods, "method", nil, "HTTP methods (repeatable)")
	f.StringSliceVar(&matchHeaders, "match-header", nil, "only requests with this header, Name:value")
	f.StringVar(&phase, "phase", "request", "request, response or both")
	f.StringVar(&gqlOp, "gql-op", "", "only this GraphQL operation (name, * wildcards)")
	f.StringVar(&timeout, "timeout", "", "how long a flow waits for a decision (default 60s, max 30m)")
	f.StringVar(&onTimeout, "on-timeout", "", "continue (default) or abort")
	f.StringVar(&name, "name", "", "rule name")
	f.StringVar(&owner, "owner", "", "user (default) or agent:<name>; env PROXYPILOT_OWNER")
	f.StringVar(&ttl, "ttl", "", "delete the rule after this long")
	f.IntVar(&priority, "priority", 0, "higher runs first")
	f.BoolVar(&disabled, "disabled", false, "create it disabled")
	return cmd
}

func parseHeaderFlags(list []string) (map[string]string, error) {
	if len(list) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, h := range list {
		k, v, ok := strings.Cut(h, ":")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, Exitf(ExitUsage, "header %q must look like Name:value", h)
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, nil
}

func newRuleListCmd(app *App) *cobra.Command {
	var owner string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List rules in evaluation order",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			list, err := c.ListRules(cmd.Context(), ownerPattern(owner))
			if err != nil {
				return mapClientErr(err)
			}
			if app.Flags.JSON {
				return app.PrintJSON(map[string]any{"rules": list})
			}
			if len(list) == 0 {
				app.Printf("No rules. Add one with `proxypilot rule add map-local --url ... --file ...`.\n")
				return nil
			}
			app.Printf("%-29s %-3s %-10s %-14s %-9s %5s  %s\n", "ID", "ON", "TYPE", "OWNER", "EXPIRES", "HITS", "MATCH")
			for _, r := range list {
				on := app.paint("32", "on")
				if !r.Enabled {
					on = app.paint("90", "off")
				}
				exp := "—"
				if r.ExpiresAt != nil {
					exp = time.Until(*r.ExpiresAt).Round(time.Second).String()
				}
				app.Printf("%-29s %-3s %-10s %-14s %-9s %5d  %s\n", r.ID, on, r.Type, truncate(r.Owner, 14), exp, r.Hits, matchText(r))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&owner, "owner", "", "filter: user, agent, agent:<name>")
	return cmd
}

func matchText(r *rules.Rule) string {
	s := r.Match.URL
	if s == "" {
		s = "*"
	}
	if len(r.Match.Methods) > 0 {
		s = strings.Join(r.Match.Methods, ",") + " " + s
	}
	return s
}

// ownerPattern turns "agent" into "agent:*"; "all" into "*".
func ownerPattern(o string) string {
	switch o {
	case "agent", "agents":
		return "agent:*"
	case "all":
		return "*"
	}
	return o
}

func newRuleShowCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show one rule as JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			r, err := c.GetRule(cmd.Context(), args[0])
			if err != nil {
				return mapClientErr(err)
			}
			return app.PrintJSON(r)
		},
	}
}

func newRuleRmCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <id>...",
		Aliases: []string{"delete"},
		Short:   "Delete rules",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			for _, id := range args {
				if err := c.DeleteRule(cmd.Context(), id); err != nil {
					return mapClientErr(err)
				}
			}
			if app.Flags.JSON {
				return app.PrintJSON(map[string]any{"deleted": len(args)})
			}
			app.Printf("Deleted %d rule(s)\n", len(args))
			return nil
		},
	}
}

func newRuleToggleCmd(app *App) *cobra.Command {
	var on, off bool
	cmd := &cobra.Command{
		Use:   "toggle <id>",
		Short: "Enable or disable a rule (flips it without --on/--off)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			var want *bool
			if on || off {
				v := on
				want = &v
			}
			r, err := c.ToggleRule(cmd.Context(), args[0], want)
			if err != nil {
				return mapClientErr(err)
			}
			if app.Flags.JSON {
				return app.PrintJSON(r)
			}
			state := "disabled"
			if r.Enabled {
				state = "enabled"
			}
			app.Printf("%s %s\n", r.ID, state)
			return nil
		},
	}
	cmd.Flags().BoolVar(&on, "on", false, "enable")
	cmd.Flags().BoolVar(&off, "off", false, "disable")
	cmd.MarkFlagsMutuallyExclusive("on", "off")
	return cmd
}

func newRuleClearCmd(app *App) *cobra.Command {
	var owner string
	cmd := &cobra.Command{
		Use:   "clear --owner agent|agent:<name>|user|all",
		Short: "Delete every rule of an owner",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if owner == "" {
				return Exitf(ExitUsage, "--owner is required (agent, agent:<name>, user or all)")
			}
			c, err := app.apiClient()
			if err != nil {
				return err
			}
			n, err := c.DeleteRules(cmd.Context(), ownerPattern(owner))
			if err != nil {
				return mapClientErr(err)
			}
			if app.Flags.JSON {
				return app.PrintJSON(map[string]any{"deleted": n})
			}
			app.Printf("Deleted %d rule(s)\n", n)
			return nil
		},
	}
	cmd.Flags().StringVar(&owner, "owner", "", "agent, agent:<name>, user or all")
	return cmd
}
