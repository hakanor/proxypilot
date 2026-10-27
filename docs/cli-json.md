# ProxyPilot CLI — JSON output

Every command accepts `--json`. Output is a single JSON document on stdout (or NDJSON for `tail`).
Errors go to stderr as `{"error": "...", "code": <exit code>}`. Field names are stable; new fields may be added.

Redaction is **on by default**: `Authorization`, `Proxy-Authorization`, `Cookie`, `Set-Cookie`, `X-Api-Key`
(plus `redact.headers` from `config.yaml`), and the keys `password`, `passwd`, `token`, `access_token`,
`refresh_token`, `id_token`, `secret`, `client_secret`, `api_key`, `apikey` (also `accessToken`, `x_auth_token`…)
in JSON bodies, form bodies and query strings. Masked values look like `Bearer eyJh…[redacted, 812 chars]`:
type and length visible, value hidden. `--no-redact` disables it and prints a warning on stderr.
`show --raw` always prints the exact body.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | success (for `wait`: a flow matched) |
| 1 | general error |
| 2 | bad usage / arguments / filter |
| 3 | daemon not running |
| 4 | not found |
| 5 | timeout (`wait`) |
| 6 | token rejected / root required (`trust`) |

## Flow object

Used by `list`, `search`, `show`, `wait` (see `docs/openapi.yaml` → `Flow`).

```json
{
  "id": "01M5W907848HRXQFFFNWJX4MB4",
  "session_id": "01M5W906KG4E4YZKBET57AQ3ZS",
  "source": "proxy",
  "client_addr": "127.0.0.1:61670",
  "client_label": "iPhone",
  "method": "GET",
  "scheme": "https",
  "host": "api.example.com",
  "port": 443,
  "path": "/v2/feed",
  "query": "page=2",
  "url": "https://api.example.com/v2/feed?page=2",
  "status": 200,
  "req_content_type": "",
  "resp_content_type": "application/json",
  "req_size": 0,
  "resp_size": 24812,
  "bytes_up": 0,
  "bytes_down": 6120,
  "protocol": "HTTP/1.1",
  "state": "completed",
  "error_kind": "",
  "error_msg": "",
  "timing": { "dns_ms": 4, "connect_ms": 12, "tls_ms": 61, "ttfb_ms": 188, "total_ms": 341, "reused": false },
  "tags": {},
  "created_at": "2026-10-26T14:02:28.904Z"
}
```

- `bytes_up` / `bytes_down`: bytes on the wire (compressed). `req_size` / `resp_size`: decoded body bytes.
- `state`: `pending` | `completed` | `failed` | `tunneled` | `paused`.
- `error_kind`: `client_tls_rejected` | `client_tls_error` | `upstream_tls_error` | `upstream_connect_error` | `timeout` | `client_aborted` | `proxy_loop` | `aborted`.
- `req_headers` / `resp_headers` (`[{"name","value"}]`) are present in `show` and `wait` only.

## Commands

### `list` / `search`

```bash
proxypilot list [--limit 20] [--host] [--method] [--status 5xx] [--type json] [--source] [--errors] [--since 5m] [--cursor] --json
proxypilot search <text> [--in url|body|headers] [--limit 20] --json
```

```json
{ "flows": [ <Flow>, ... ], "next_cursor": "MTc…", "has_more": true, "limit": 20 }
```

Pass `next_cursor` back with `--cursor` for the next page. `--limit` is capped at 500.

### `show <id>`

```bash
proxypilot show <id> [--body] [--side req|resp|both] [--max-body 4kb] [--raw] --json
```

```json
{
  ...<Flow with req_headers/resp_headers>,
  "bodies": {
    "req":  { "side": "req",  "encoding": "", "raw_size": 0, "decoded_size": 0, "truncated": false, "on_disk": false },
    "resp": { "side": "resp", "encoding": "gzip", "raw_size": 6120, "decoded_size": 24812, "truncated": false, "on_disk": false }
  },
  "body": {
    "resp": {
      "flow_id": "01M5…", "side": "resp",
      "size": 24812, "returned": 4096, "truncated": true,
      "content_type": "application/json", "content_encoding": "gzip",
      "encoding": "utf8",
      "data": "{\"items\":[…"
    }
  }
}
```

`body` appears only with `--body`. `encoding` is `utf8` or `base64` (binary). `--raw` writes the full decoded body bytes (no JSON).

### `summary`

```bash
proxypilot summary [--since 5m | 1h | all] --json
```

```json
{
  "total": 47, "errors": 1,
  "by_status": { "2xx": 40, "4xx": 4, "5xx": 2, "none": 1 },
  "by_error_kind": { "client_tls_rejected": 1 },
  "top_hosts": [ { "host": "api.x.com", "count": 30, "bytes_down": 1048576, "errors": 3 } ],
  "bytes_up": 30720, "bytes_down": 1258291,
  "avg_total_ms": 140.2,
  "failures": [
    { "method": "POST", "host": "api.x.com", "path": "/checkout", "status": 500, "count": 2, "last_id": "01M5…" },
    { "method": "CONNECT", "host": "pinning.bank.com", "path": "", "error_kind": "client_tls_rejected", "count": 1, "last_id": "01M5…" }
  ],
  "slowest": { "id": "01M5…", "method": "GET", "url": "https://cdn.x.com/big.json", "total_ms": 3200 },
  "since": "2026-10-26T14:00:00Z"
}
```

### `tail`

```bash
proxypilot tail [--host] [--method] [--status] [--type] [--errors] [--events flow.completed,flow.failed] --json
```

NDJSON, one event per line:

```json
{"seq":12,"type":"flow.completed","time":"2026-10-26T14:02:29Z","data":{"id":"01M5…","method":"GET","scheme":"https","host":"api.x.com","path":"/v2/feed","url":"…","status":200,"state":"completed","content_type":"application/json","bytes_up":0,"bytes_down":6120,"resp_size":24812,"total_ms":341,"client_label":"iPhone","source":"proxy","created_at":"…"}}
{"seq":0,"type":"dropped","time":"0001-01-01T00:00:00Z","count":37}
```

`dropped` means output could not keep up; re-sync with `list`.

### `wait`

```bash
proxypilot wait --url '<substring or * pattern>' [--method POST] [--status 2xx] [--timeout 60s] [--since 10s] --json
```

Waits for the first **completed** flow whose URL matches. Exit 0:

```json
{ "matched": true, "flow": <Flow with headers and "bodies"> }
```

Exit 5 on timeout: `{ "matched": false, "timeout": "1m0s" }`.

Agent loop: start `wait` (in the background or with `--since`), trigger the action in the app, read the flow, then `show <id> --body`.

### `status`

```json
{ "running": true, "pid": 17395, "proxy_addr": "127.0.0.1:9090", "api_addr": "127.0.0.1:9091",
  "started_at": "…", "uptime_seconds": 120, "flows": 47, "active_rules": 0, "pending_breakpoints": 0,
  "ca_fingerprint": "57:14:…", "session": "01M5…", "stream_subscribers": 1, "version": "v0.1.0" }
```

Not running: `{ "running": false, "data_dir": "…" }` with exit 3.

### `clear --yes`

`{ "cleared": true }`. Without `--yes`: exit 2.

### `rule`

```bash
proxypilot rule add map-local --url <pattern> (--file F | --dir D | --body B | --body-file F | --from <flow-id>)
    [--regex] [--method M] [--match-header Name:value] [--status N] [--header Name:value]
    [--mode full|body_only] [--priority N] [--owner user|agent:<name>] [--ttl 30m] [--disabled] --json
proxypilot rule list [--owner agent] --json
proxypilot rule show <id>
proxypilot rule toggle <id> [--on|--off] --json
proxypilot rule rm <id>... --json
proxypilot rule clear --owner agent|agent:<name>|user|all --json
```

Rule object:

```json
{ "id": "r_01m5…", "name": "Feed from file", "type": "map_local", "enabled": true, "owner": "agent:claude",
  "expires_at": "2026-10-26T18:50:00Z",
  "match": { "url": "https://api.x.com/v2/feed*", "regex": false, "methods": ["GET"], "headers": {"Accept": "application/*"} },
  "phase": "request",
  "spec": { "file": "/abs/mock.json", "status": 200, "headers": {"X-Mock": "1"}, "mode": "full" },
  "priority": 0, "created_at": "…", "hits": 3 }
```

Map Local `spec` has exactly one of `file`, `dir`, `body`, `body_base64`.
Rewrite `spec`: `{ "steps": [ { "type": "set_header|add_header|remove_header|set_query|remove_query|replace_body|json_set|json_remove|set_status|set_url|set_path", "name", "value", "pattern", "regex", "path", "status" } ] }`.
Map Remote `spec`: `{ "to": "https://staging.x.com/*", "preserve_host_header": false }`; flows get `tags.original_url` and `tags.map_remote_to`.
Commands: `rule add rewrite` (`--set-header`, `--json-set`, `--replace`, `--status`, `--steps-file`…) and `rule add map-remote --to URL [--preserve-host]`. Invalid rules exit 2. `--owner` defaults to `$PROXYPILOT_OWNER`, then `user`.
Agent-owned rules are kept in memory only; user rules persist in `<data-dir>/rules.json`. `--from` prints `{ "rule": <Rule>, "file": "<data-dir>/maplocal/<flow>.json" }`.
Flows record matching rule ids in `matched_rules`.

### `repeat`, `compose`, `curl`

```bash
proxypilot repeat <id> [--set-header 'K: V'] [--remove-header K] [--set-query k=v] [--body text|@file] [--method M] [--url U]
    [--count 1..50] [--concurrency 1..10] [--bypass-rules] --json
proxypilot compose (--from-curl "<cmd>"|@file | --method M --url U --header 'K: V' --body …) [--count] [--concurrency] --json
proxypilot curl <id> --json          # masked unless --no-redact
```

`repeat` / `compose`: `{ "results": [ { "flow_id", "status", "ms", "aborted", "error" } ], "by_status": { "2xx": 3 }, "avg_ms": 271 }`.
Over the limits: exit 2 (`limit`). New flows have `source: "composer"`; repeats carry `tags.repeat_of`.
`curl`: `{ "curl": "curl -X PATCH '…' \\\n  -H '…'", "binary_body": false }`.

### `export`, `import`, `session`

```bash
proxypilot export har [--since 10m] [--host …] [--gql-op …] [--ids a,b] [-o out.har]
proxypilot import har <file> --json
proxypilot session save <file.proxypilot> [filters]
proxypilot session open <file.proxypilot> --json
```

`import` / `session open`: `{ "session": "01M5…", "imported": 42, "skipped": 0 }` (new session, `source: "import"`).
Exports are masked unless `--no-redact`. HAR entries carry ProxyPilot extras as `_id`, `_source`, `_client`, `_tags`, `_error`.
A session file is a zip: `manifest.json`, `flows.ndjson`, `bodies/<id>.req|resp`.

GraphQL: flows get `tags.gql_op` (comma-separated for batches) and `tags.gql_type`; responses with `errors` get `error_kind: "graphql_error"`.
Filter with `--gql-op`; rules match with `--gql-op` (`match.graphql_operation`).

### `breakpoint`

```bash
proxypilot rule add breakpoint --url <pattern> [--phase request|response|both] [--timeout 60s] [--on-timeout continue|abort] --json
proxypilot breakpoint list --json
proxypilot breakpoint wait [--timeout 60s] [--url <substring|*pattern>] [--phase request|response] --json
proxypilot breakpoint resume <id> [--status N] [--set-header Name:value] [--remove-header Name] [--body text|@file] [--method M] [--url U] [--abort] --json
proxypilot breakpoint edit <id>
proxypilot breakpoint extend <id> [--seconds 30] --json
```

Paused object:

```json
{ "id": "bp_01m5…", "flow_id": "01M5…", "phase": "response", "rule_id": "r_…", "rule_name": "Checkout", "owner": "user",
  "created_at": "…", "expires_at": "…", "on_timeout": "continue", "truncated": false,
  "request":  { "method": "POST", "url": "https://api.x.com/checkout", "headers": [{"name","value"}], "body": "" },
  "response": { "status": 200, "headers": [...], "body": "{…}", "encoding": "gzip" } }
```

`wait` exits 5 with `{ "paused": false, "timeout": "1m0s" }`. `resume` prints `{ "resumed": true, "id", "action": "continue|modify|abort" }`; an already resolved id exits 1.
Bodies are UTF-8 text, or `body_base64` for binary. Output is redacted by default; edits are built from the unredacted original.

### `setup ios-sim`

```bash
proxypilot setup ios-sim --json                 # list
proxypilot setup ios-sim --all-booted --json    # install the CA
proxypilot setup ios-sim --udid <udid> --json
```

List: `{ "simulators": [ { "udid", "name", "runtime": "iOS 26.3", "state": "Booted", "booted": true, "ca": "installed|missing|unknown" } ] }`.
Install: `{ "results": [ { "udid", "name", "ok": true, "error": "" } ], "system_proxy": false }`; exit 1 if any failed. No Xcode: exit 1 with an explanation.

### `setup device`

```bash
proxypilot setup device [--no-qr] --json
```

```json
{ "proxy_addr": "0.0.0.0:9090", "port": "9090", "lan_ips": ["192.168.1.66"], "reachable": true,
  "page_url": "http://192.168.1.66:9090/", "firewall": "off", "warnings": [], "magic_url": "http://proxypilot.cert" }
```

`warnings`: `listening_loopback` (start with `--proxy-listen 0.0.0.0:9090`), `no_lan`, `firewall`.
Without `--json` it prints an ASCII QR code for `page_url` and the iOS / Android steps.

### `chrome`

```bash
proxypilot chrome launch [--url URL] --json
proxypilot chrome attach [--port 9222|host:port] --json
proxypilot chrome status --json
proxypilot chrome detach --json
```

`{ "connected": true, "launched": true, "endpoint": "127.0.0.1:53111", "browser": "Chrome/141.0…", "tabs": [ { "id", "title", "url", "watching": true, "requests": 12 } ] }`.
Chrome flows have `source: "cdp"` and `tags.tab_id`, `tags.tab_title`, `tags.tab_url`; WebSockets carry `tags.ws_frames`.
The end-to-end test against a real Chrome runs only with `PROXYPILOT_CHROME_TEST=1 go test ./internal/cdp`.

### `setup android`

```bash
proxypilot setup android --list --json
proxypilot setup android [--serial S] [--mode emulator|usb|wifi] [--ca user|system|none] --json
proxypilot setup android --remove [--serial S] --json
```

List: `{ "adb": "/path/adb", "devices": [ { "serial", "state", "model", "emulator", "api_level", "proxy", "ours", "user_ca_pushed", "system_ca" } ] }`.
Setup / remove: `{ "device": {…}, "mode": "emulator", "proxy": "10.0.2.2:9090", "ca": "user", "done": [], "manual": [], "warnings": ["user_ca_apps"] }`.
Errors (exit 1): adb not found, no device, system CA without root. Several devices without `--serial`: exit 2.
The certificate is installed before the proxy is changed, so a failed `--ca system` leaves the device untouched.

### `system-proxy on|off|status`

`status` / `on` / `off` print `{ "services": [ { "name": "Wi-Fi", "web": {"enabled","server","port"}, "secure": {...}, "bypass": [...] } ], "enabled": true, "managed": true, "backup_at": "…", "target": "127.0.0.1:9090" }`.
`on` saves the previous settings to `<data-dir>/system-proxy-backup.json` once; `off` restores them exactly and deletes the backup (exit 4 if there is none).
When macOS requires an administrator: exit 6 and `{ "error": "needs_admin", "commands": ["sudo networksetup …"] }`.
`stop` restores the system proxy automatically (`"system_proxy_restored": true`); `--keep-system-proxy` skips it.

### `uninstall`

```bash
proxypilot uninstall --json              # plan only
proxypilot uninstall --yes --json        # stop daemon, restore system proxy, remove simulator CAs
proxypilot uninstall --yes --purge       # … and delete the data directory
```

Plan: `{ "dry_run": true, "steps": [ { "id": "system_proxy|simulators|mac_trust|devices|data", "status": "pending|clean|manual|unavailable", "auto": true, "details": [], "commands": [] } ] }`.
Apply: `{ "stopped_daemon": true, "results": [ { "id", "ok", "done": [], "error" } ], "steps": [...], "purged": false }`. Admin-only steps (macOS keychain) are never run; their `commands` are listed.

### `ca info`, `ca export`, `ca regenerate`, `trust`, `untrust`, `start`, `stop`, `version`

See `proxypilot <command> --help`; all print a single JSON object with `--json`.
