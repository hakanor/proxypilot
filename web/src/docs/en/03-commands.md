---
title: Commands
section: CLI
---
# Commands

Every command accepts `--json`. Secrets (Authorization, cookies, tokens, passwords) are masked unless you pass `--no-redact`.

| Command | Purpose |
|---|---|
| `proxypilot list --status 5xx --since 10m` | Recent flows, filtered |
| `proxypilot search checkout --in body` | Search URL, body or headers |
| `proxypilot show <id> --body` | Headers, timing and bodies of one flow |
| `proxypilot show <id> --raw > body.bin` | Exact decoded body |
| `proxypilot summary --since 5m` | Totals, failures, slowest request |
| `proxypilot tail --errors` | Live stream (NDJSON with `--json`) |
| `proxypilot wait --url '*/login'` | Block until a matching flow completes |
| `proxypilot clear --yes` | Delete all recorded traffic |
| `proxypilot rule add map-local --url … --file …` | Answer requests with local content |
| `proxypilot rule list\|rm\|toggle\|clear` | Manage rules |
| `proxypilot export har` / `import har` | HAR 1.2 (masked by default) |
| `proxypilot session save\|open <file>` | One-file sessions with bodies |
| `proxypilot repeat <id> [--count 5]` | Send a recorded request again, optionally edited |
| `proxypilot curl <id>` / `compose --from-curl` | cURL export and import |
| `proxypilot rule add rewrite --url … --json-set …` | Edit traffic in flight |
| `proxypilot rule add map-remote --url … --to …` | Send requests to another server |
| `proxypilot rule add breakpoint --url …` | Pause matching flows |
| `proxypilot breakpoint wait\|resume\|edit\|list` | Decide on paused flows |
| `proxypilot system-proxy on\|off\|status` | macOS system proxy |
| `proxypilot setup ios-sim` | List simulators and install the CA |
| `proxypilot setup device` | QR code and steps for a physical phone |
| `proxypilot chrome launch\|attach\|status\|detach` | Capture Chrome tabs over DevTools |
| `proxypilot setup android` | Emulator / USB device over adb (`--ca user\|system`, `--remove`) |
| `proxypilot uninstall` | Show (or, with `--yes`, apply) the cleanup plan |

## Filters

`list` and `tail` share the filter flags:

```bash
proxypilot list --host '*.example.com' --method POST --status 4xx,5xx --type json --errors --gql-op FeedQuery
```

The filter bar in **Traffic** uses the same words: `host:api.* status:4xx method:POST type:json`.

## Global flags

| Flag | Meaning |
|---|---|
| `--json` | Machine-readable output |
| `--data-dir` | Use another data directory (env `PROXYPILOT_HOME`) |
| `--no-redact` | Show secrets in output |
| `--no-color`, `-q` | Plain output, errors only |
