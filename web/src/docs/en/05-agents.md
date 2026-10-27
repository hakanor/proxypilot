---
title: AI agents
section: AI agents
---
# AI agents

Agents use ProxyPilot through the MCP server or the CLI with `--json`.

## MCP

```bash
claude mcp add -s user proxypilot -- proxypilot mcp --agent-name claude
```

Other clients (Cursor, etc.) take the same command in their `mcpServers` config. Tools: `summarize_traffic`, `list_requests`, `get_request`, `search_traffic`, `wait_for_request`, `add_map_local`, `add_rewrite`, `add_map_remote`, `repeat_request`, `set_breakpoint`, `wait_for_breakpoint`, `resume_breakpoint`, `list_rules`, `remove_rule`, `clear_agent_rules`, `clear_session`, `get_status`.

- Every answer stays under 8 KB; bodies are excerpts you can page with `offset`.
- Rules an agent creates belong to `agent:<name>`, expire after 1h by default, and cannot touch your rules.
- `--read-only` exposes only the read tools; `--allow-secrets` (yours to set, not the agent's) turns masking off.

## CLI

Good habits for agents:

- Start with `proxypilot summary --since 5m --json`: it groups failures and names the slowest request, so the agent rarely needs the full list.
- Fetch bodies only for the flow you care about: `show <id> --body --max-body 4kb`.
- Keep redaction on. Masked values keep their type and length: `Bearer eyJh…[redacted, 812 chars]`.
- Use `wait --since` instead of polling `list`.
- Mock with rules owned by the agent and a TTL: `PROXYPILOT_OWNER=agent:claude proxypilot rule add map-local … --ttl 15m`; clean up with `rule clear --owner agent`.

## Example prompt

```text
Run `proxypilot summary --since 10m --json`. For each failure, run
`proxypilot show <last_id> --body --json` and explain the cause.
```
