package mcpserver_test

import (
	"encoding/json"

	"github.com/hakanor/proxypilot/internal/api"
	"github.com/hakanor/proxypilot/internal/rules"
)

func apiRule(u string) api.RuleInput {
	return api.RuleInput{Rule: rules.Rule{Type: rules.TypeMapLocal, Enabled: true, Match: rules.Matcher{URL: u}, Spec: json.RawMessage(`{"body":"user"}`)}}
}
