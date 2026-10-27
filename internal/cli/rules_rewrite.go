package cli

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hakanor/proxypilot/internal/api"
	"github.com/hakanor/proxypilot/internal/rules"
)

// ruleMatchFlags are shared by the rewrite and map-remote commands.
type ruleMatchFlags struct {
	url, name, owner, ttl, gqlOp string
	methods, headers             []string
	regex, disabled              bool
	priority                     int
}

func (m *ruleMatchFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&m.url, "url", "", "URL pattern (wildcard *, or a regex with --regex)")
	f.BoolVar(&m.regex, "regex", false, "treat --url as a regular expression")
	f.StringVar(&m.gqlOp, "gql-op", "", "only this GraphQL operation (name, * wildcards)")
	f.StringSliceVar(&m.methods, "method", nil, "HTTP methods (repeatable)")
	f.StringSliceVar(&m.headers, "match-header", nil, "only requests with this header, Name:value")
	f.StringVar(&m.name, "name", "", "rule name")
	f.StringVar(&m.owner, "owner", "", "user (default) or agent:<name>; env PROXYPILOT_OWNER")
	f.StringVar(&m.ttl, "ttl", "", "delete the rule after this long")
	f.IntVar(&m.priority, "priority", 0, "higher runs first")
	f.BoolVar(&m.disabled, "disabled", false, "create it disabled")
}

func (m *ruleMatchFlags) add(app *App, cmd *cobra.Command, typ string, phase rules.Phase, spec any) error {
	c, err := app.apiClient()
	if err != nil {
		return err
	}
	if m.owner == "" {
		m.owner = os.Getenv("PROXYPILOT_OWNER")
	}
	mh, err := parseHeaderFlags(m.headers)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(spec)
	r, err := c.AddRule(cmd.Context(), api.RuleInput{
		Rule: rules.Rule{Name: m.name, Type: typ, Enabled: !m.disabled, Owner: m.owner, Priority: m.priority, Phase: phase,
			Match: rules.Matcher{URL: m.url, Regex: m.regex, Methods: m.methods, Headers: mh, GraphQLOperation: m.gqlOp}, Spec: raw},
		TTL: m.ttl,
	})
	if err != nil {
		return mapClientErr(err)
	}
	if app.Flags.JSON {
		return app.PrintJSON(r)
	}
	app.Printf("Created %s: %s\n", r.ID, r.Name)
	return nil
}

func newRuleAddRewriteCmd(app *App) *cobra.Command {
	var m ruleMatchFlags
	var phase, stepsFile, setPath, setURL string
	var setH, addH, rmH, setQ, rmQ, repl, replRe, jsonSet, jsonRm []string
	var status int
	cmd := &cobra.Command{
		Use:   "rewrite",
		Short: "Edit matching requests or responses: headers, query, body, JSON fields, status, URL",
		Long: "Steps run in this order: --set-url, --set-path, --set-query, --remove-query, --set-header, --add-header,\n" +
			"--remove-header, --replace, --replace-regex, --json-set, --json-remove, --status.\n" +
			"For another order use --steps-file with a JSON list of steps. Compressed bodies are decoded first.",
		Example: "  proxypilot rule add rewrite --url '*/v2/me' --phase response --set-header 'X-Debug: 1' --json-set '$.user.isPremium=true'\n" +
			"  proxypilot rule add rewrite --url '*/api/*' --set-header 'Authorization: Bearer test' --remove-query debug\n" +
			"  proxypilot rule add rewrite --url '*/feed' --phase response --replace 'Sold out=>In stock' --status 200",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var steps []rules.Step
			if stepsFile != "" {
				b, err := os.ReadFile(stepsFile)
				if err != nil {
					return err
				}
				if err := json.Unmarshal(b, &steps); err != nil {
					return Exitf(ExitUsage, "%s: %v", stepsFile, err)
				}
			}
			add := func(s rules.Step) { steps = append(steps, s) }
			if setURL != "" {
				add(rules.Step{Type: rules.StepSetURL, Value: setURL})
			}
			if setPath != "" {
				add(rules.Step{Type: rules.StepSetPath, Value: setPath})
			}
			for _, kv := range setQ {
				k, v, _ := strings.Cut(kv, "=")
				add(rules.Step{Type: rules.StepSetQuery, Name: k, Value: v})
			}
			for _, k := range rmQ {
				add(rules.Step{Type: rules.StepRemoveQuery, Name: k})
			}
			for _, list := range []struct {
				typ  string
				vals []string
			}{{rules.StepSetHeader, setH}, {rules.StepAddHeader, addH}} {
				for _, h := range list.vals {
					k, v, ok := strings.Cut(h, ":")
					if !ok {
						return Exitf(ExitUsage, "header %q must look like Name: value", h)
					}
					add(rules.Step{Type: list.typ, Name: strings.TrimSpace(k), Value: strings.TrimSpace(v)})
				}
			}
			for _, k := range rmH {
				add(rules.Step{Type: rules.StepRemoveHeader, Name: k})
			}
			for _, list := range []struct {
				vals  []string
				regex bool
			}{{repl, false}, {replRe, true}} {
				for _, r := range list.vals {
					old, nw, ok := strings.Cut(r, "=>")
					if !ok {
						return Exitf(ExitUsage, "replacement %q must look like old=>new", r)
					}
					add(rules.Step{Type: rules.StepReplaceBody, Pattern: old, Value: nw, Regex: list.regex})
				}
			}
			for _, js := range jsonSet {
				p, v, ok := strings.Cut(js, "=")
				if !ok {
					return Exitf(ExitUsage, "--json-set %q must look like $.path=value", js)
				}
				add(rules.Step{Type: rules.StepJSONSet, Path: p, Value: v})
			}
			for _, p := range jsonRm {
				add(rules.Step{Type: rules.StepJSONRemove, Path: p})
			}
			if status != 0 {
				add(rules.Step{Type: rules.StepSetStatus, Status: status})
			}
			if len(steps) == 0 {
				return Exitf(ExitUsage, "no steps: pass at least one of --set-header, --json-set, --replace, --status… or --steps-file")
			}
			return m.add(app, cmd, rules.TypeRewrite, rules.Phase(phase), rules.RewriteSpec{Steps: steps})
		},
	}
	m.register(cmd)
	f := cmd.Flags()
	f.StringVar(&phase, "phase", "request", "request, response or both")
	f.StringSliceVar(&setH, "set-header", nil, "set a header, 'Name: value' (repeatable)")
	f.StringSliceVar(&addH, "add-header", nil, "add a header value, 'Name: value'")
	f.StringSliceVar(&rmH, "remove-header", nil, "remove a header")
	f.StringSliceVar(&setQ, "set-query", nil, "set a query parameter, name=value (request)")
	f.StringSliceVar(&rmQ, "remove-query", nil, "remove a query parameter (request)")
	f.StringArrayVar(&repl, "replace", nil, "replace text in the body, 'old=>new'")
	f.StringArrayVar(&replRe, "replace-regex", nil, "regex replace in the body, 'pattern=>replacement' ($1 works)")
	f.StringArrayVar(&jsonSet, "json-set", nil, "set a JSON field, '$.path=value' (value is JSON, else a string)")
	f.StringArrayVar(&jsonRm, "json-remove", nil, "remove a JSON field, '$.path'")
	f.IntVar(&status, "status", 0, "set the response status (response phase)")
	f.StringVar(&setPath, "set-path", "", "replace the path (request)")
	f.StringVar(&setURL, "set-url", "", "replace the whole URL (request)")
	f.StringVar(&stepsFile, "steps-file", "", "JSON file with an ordered list of steps")
	return cmd
}

func newRuleAddMapRemoteCmd(app *App) *cobra.Command {
	var m ruleMatchFlags
	var to string
	var preserve bool
	cmd := &cobra.Command{
		Use:   "map-remote",
		Short: "Send matching requests to another server (prod → staging, or localhost)",
		Example: "  proxypilot rule add map-remote --url 'https://api.example.com/*' --to 'https://staging.example.com/*'\n" +
			"  proxypilot rule add map-remote --url 'https://api.example.com/v2/feed*' --to http://localhost:3000/feed",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if to == "" {
				return Exitf(ExitUsage, "--to is required")
			}
			return m.add(app, cmd, rules.TypeMapRemote, rules.PhaseRequest, rules.MapRemoteSpec{To: to, PreserveHostHeader: preserve})
		},
	}
	m.register(cmd)
	cmd.Flags().StringVar(&to, "to", "", "target URL; a trailing * keeps the rest of the original path")
	cmd.Flags().BoolVar(&preserve, "preserve-host", false, "keep the original Host header")
	return cmd
}
