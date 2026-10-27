---
title: Wait for a request
section: CLI
---
# Wait for a request from your app

Start waiting, trigger the action in the app, then read the flow. This is the loop an AI agent uses to check its own changes.

```bash
proxypilot wait --url '*/checkout/*' --timeout 60s --json > flow.json &
# … tap "Pay" in the simulator …
wait
proxypilot show "$(jq -r .flow.id flow.json)" --body --side resp
```

If the action may already have happened, `--since 10s` also matches flows that completed just before.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success (`wait`: a flow matched) |
| 1 | General error |
| 2 | Bad usage or filter |
| 3 | Daemon not running |
| 4 | Not found |
| 5 | Timeout (`wait`) |
| 6 | Token rejected / administrator required |
