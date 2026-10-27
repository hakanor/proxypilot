# ProxyPilot MCP server

`proxypilot mcp` serves ProxyPilot's tools to AI agents over MCP (stdio). It talks to a running daemon
through the control API; start the daemon first (`proxypilot start --detach` or `proxypilot ui`), or pass `--auto-start`.

## Setup

**Claude Code**

```bash
claude mcp add -s user proxypilot -- proxypilot mcp --agent-name claude
```

**Cursor / any client with an `mcpServers` config**

```json
{
  "mcpServers": {
    "proxypilot": { "command": "proxypilot", "args": ["mcp", "--agent-name", "cursor"] }
  }
}
```

Use the absolute path of the binary if it is not on `PATH`. A custom data directory is passed with `--data-dir`.

| Flag | Meaning |
|---|---|
| `--agent-name <name>` | Rules the agent creates are owned by `agent:<name>` |
| `--read-only` | Only read tools are registered |
| `--allow-secrets` | Do not mask secrets (only the user can set this; agents cannot change it) |
| `--auto-start` | Start the daemon if it is not running |

## Tools

| Tool | Writes | Purpose |
|---|---|---|
| `get_status` | | Daemon, proxy, rules, paused breakpoints, who you are |
| `summarize_traffic` | | Start here: totals, failure groups (with last flow id), slowest request |
| `list_requests` | | Compact rows, filter by host / method / status / type / errors / since / gql_op; default 20 |
| `search_traffic` | | Text in URL, headers or body |
| `get_request` | | Timing, headers and a body excerpt (`max_body_bytes`, `offset` to read on) |
| `wait_for_request` | | Wait for a matching flow to complete (`timeout_seconds` 1–300, optional `since_seconds`) |
| `get_websocket_messages` | | Messages of a WebSocket connection (status 101): direction, type, size, text; page with `after` |
| `list_rules` | | All rules, marking yours |
| `wait_for_breakpoint` | | Wait for a flow paused by one of your breakpoints |
| `add_map_local` | ✓ | Mock a response (body or file, status, headers, `full`/`body_only`) |
| `add_rewrite` | ✓ | Ordered edit steps: headers, query, body replace, JSON set/remove, status, URL |
| `add_map_remote` | ✓ | Send matching requests to another server (`to`, trailing `*` keeps the path) |
| `repeat_request` | ✓ | Send a recorded request again, optionally edited (count ≤ 10) |
| `set_breakpoint` | ✓ | Pause requests and/or responses |
| `resume_breakpoint` | ✓ | `continue`, `modify` (status, headers, body, method, URL) or `abort` |
| `remove_rule` | ✓ | Delete one of your rules |
| `clear_agent_rules` | ✓ | Delete all your rules |
| `set_throttle` | ✓ | Simulate a slow network (`edge`, `3g`, `lte`, `bad-wifi`, `custom`), optionally only for some hosts; `enabled: false` turns it off |
| `clear_session` | ✓ | Delete all recorded traffic |


## Guarantees

- **Small answers.** No tool result is larger than 8 KB. Lists are cut with a hint; bodies are excerpts with `more: true` and the next `offset`.
- **Masking.** Authorization, cookies, API keys, tokens and passwords are shown as `Bearer eyJh…[redacted, 812 chars]`.
  Breakpoint edits are built from the unmasked original, so a masked value is never sent upstream.
- **Ownership.** Rules created through MCP are owned by `agent:<name>` and expire after 1 hour unless `ttl` says otherwise (max 24h).
  An agent can only remove its own rules and resume breakpoints its own rules created; the user's rules and breakpoints are off limits.
  Agent rules live in memory only and disappear when they expire or the daemon stops.
- **Errors explain the next step**, e.g. "ProxyPilot is not running. Ask the user to run `proxypilot start --detach`".

## Example session

1. `summarize_traffic {since: "10m"}`: `POST /v2/checkout 500 ×3, last_id 01M5…`
2. `get_request {id: "01M5…", body: "both"}`: request body and the server's error message
3. `add_map_local {url: "https://api.example.com/v2/checkout", methods: ["POST"], status: 200, body: "{\"ok\":true}", ttl: "15m"}`
4. `wait_for_request {url: "*/v2/checkout", timeout_seconds: 120}` while the user taps Pay
5. `clear_agent_rules` when done

For step-by-step agent guidance and more scenarios, see [AGENTS.md](AGENTS.md).

## Testing with MCP Inspector

```bash
npx @modelcontextprotocol/inspector proxypilot mcp --agent-name inspector
```
