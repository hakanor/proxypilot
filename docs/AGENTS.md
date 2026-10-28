# Using ProxyPilot from an AI agent

ProxyPilot gives an agent eyes on an app's network traffic and hands to change it: read requests, mock responses, rewrite fields, pause and edit requests, slow the network down. Everything goes through MCP tools (`proxypilot mcp`, see [mcp.md](mcp.md) for setup and the full tool table).

The section between the markers below is meant to be copied into a project's `CLAUDE.md` / `AGENTS.md`, or saved as a skill, so the agent follows it.

<!-- proxypilot-agent-guide:start -->
## Working with ProxyPilot (MCP)

ProxyPilot is an HTTP(S)/WebSocket debugging proxy. The user's app traffic (iOS Simulator, phones, Chrome, Mac apps) flows through it.

**Start every task with `get_status`.** If ProxyPilot is not running, ask the user to run `proxypilot start --detach` (or `proxypilot ui`); do not try to start or configure it yourself. If `get_status` shows no recent traffic, ask the user which device or app to use and point them to the Setup page.

**Read before you fetch.**
1. `summarize_traffic {since: "10m"}` first: totals, failing endpoints grouped with the last flow id, slowest request.
2. Then narrow down: `list_requests` with filters (`host`, `status`, `method`, `errors_only`, `since`, `gql_op`) or `search_traffic {query, in: "url"|"headers"|"body"}`.
3. Only then open single requests with `get_request {id, body: "response"|"request"|"both"}`. Bodies are excerpts; for a big JSON body ask for just the part you need with `path` (e.g. `$.items[0]`), and read further with `offset` only when that is not enough. Never pull whole large bodies "just in case".
4. To find what broke, compare a working request with a failing one: `diff_flows {a: <ok id>, b: <failing id>}` returns only the differences (headers, query parameters, JSON fields by path).
5. WebSocket connections are requests with status 101; read their messages with `get_websocket_messages {id}` and page with `after`.

**Testing an action in the app.** Call `wait_for_request {url: "*/path*", timeout_seconds: 120}` first, then ask the user to perform the action (tap the button, reload the page). The tool returns the request when it completes.

**Changing traffic.** Prefer the narrowest rule and always give it a `ttl`:
- Mock a response: `add_map_local {url, methods, status, body, ttl: "15m"}`.
- Change part of a real response or request: `add_rewrite {url, phase, steps: [...]}` (e.g. `json_set` on `$.user.isPremium`).
- Send requests elsewhere: `add_map_remote {url, to}`.
- Pause and edit: `set_breakpoint {url, phase}`, then `wait_for_breakpoint`, then `resume_breakpoint {id, action: "continue"|"modify"|"abort", ...}`. A paused request blocks the app; resolve it quickly.
- Replay with edits: `repeat_request {id, set_headers, body, ...}`.
- Slow network: `set_throttle {enabled: true, profile: "3g", hosts: [...]}`. It affects everyone using the proxy, so tell the user before turning it on and turn it off (`enabled: false`) when done.

**Clean up.** When the task is done (or abandoned), call `clear_agent_rules` and turn off any throttle you set. You can only remove your own rules (`list_rules` marks them); never ask the user to delete theirs for you. Do not call `clear_session` unless the user asked for a clean slate.

**Secrets.** Tokens, cookies and passwords come back masked (`Bearer eyJh…[redacted, 812 chars]`). Do not try to recover or guess them, and do not ask the user to disable masking. Breakpoint edits keep the real values when you leave those headers alone.

**Report with evidence.** Quote the flow id, method, URL, status and the relevant part of the body or header when you explain what happened.
<!-- proxypilot-agent-guide:end -->

## Scenarios

### "Checkout returns 500, find out why"

1. `get_status`, then `summarize_traffic {since: "15m"}` → the failure groups show `POST /v2/checkout 500 ×3` with `last_id`.
2. `get_request {id: <last_id>, body: "both"}` → compare the request body with the server's error message (e.g. `"currency": null`).
3. `list_requests {host: "api.example.com", since: "15m"}` → find an earlier checkout that succeeded, then `diff_flows {a: <ok id>, b: <last_id>}` → e.g. `$.currency` removed and `x-app-version` 4.1.0 → 4.2.0.
4. Optionally confirm the fix without a backend change: `add_rewrite {url: "*/v2/checkout", phase: "request", steps: [{"type": "json_set", "path": "$.currency", "value": "\"TRY\""}], ttl: "10m"}`, then `wait_for_request {url: "*/v2/checkout", timeout_seconds: 120}` while the user taps Pay.
5. `clear_agent_rules`. Report: the failing request, the cause, the evidence, the suggested fix.

### "Mock the empty list state and check the UI"

1. `list_requests {host: "api.example.com", method: "GET", since: "10m"}` → find the list endpoint, e.g. `GET /v1/orders`.
2. `get_request {id}` → note the response shape (`{"items": [...], "total": 12}`).
3. `add_map_local {url: "https://api.example.com/v1/orders*", methods: ["GET"], status: 200, body: "{\"items\":[],\"total\":0}", ttl: "15m"}`.
4. `wait_for_request {url: "*/v1/orders*", timeout_seconds: 120}` and ask the user to pull to refresh; the result shows the rule matched.
5. Ask the user what the screen shows (or check a screenshot they share), then `clear_agent_rules`.

### "Verify the token refresh flow"

1. `set_breakpoint {url: "*/v1/profile", phase: "response", ttl: "10m"}`.
2. `wait_for_breakpoint {timeout_seconds: 120}` while the user opens the profile screen.
3. `resume_breakpoint {id, action: "modify", status: 401, body: "{\"error\":\"token_expired\"}"}` → the app should now refresh its token.
4. `wait_for_request {url: "*/oauth/token*", timeout_seconds: 60}` → confirm the refresh request was sent and succeeded (`get_request` shows status 200; the token itself stays masked).
5. `wait_for_request {url: "*/v1/profile", timeout_seconds: 60}` → the retried request succeeded with the new token.
6. `clear_agent_rules`. Report whether the app refreshed and retried, or logged the user out.

### "The feed is slow on bad networks, reproduce it"

1. Tell the user you are about to slow down the network for `api.example.com`.
2. `set_throttle {enabled: true, profile: "3g", hosts: ["api.example.com"]}`.
3. `wait_for_request {url: "*/v1/feed*", timeout_seconds: 120}` while the user opens the feed; note the duration in the result and which requests ran in sequence instead of in parallel (`list_requests {host: "api.example.com", since: "2m"}`).
4. `set_throttle {enabled: false}`. Report the slow chain of requests.

### "What does the app send over its WebSocket?"

1. `list_requests {since: "10m"}` → the WebSocket connection is the request with status `101`.
2. `get_websocket_messages {id, limit: 50}` → `up` messages are what the app sent, `down` what the server pushed; page with `after: <next>`.
