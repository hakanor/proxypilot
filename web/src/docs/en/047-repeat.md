---
title: Repeat, Compose and cURL
section: CLI
---
# Repeat, Compose and cURL

Repeated and composed requests go through the same pipeline as live traffic (rules, breakpoints, recording) and appear in **Traffic** with source **Composer**.

```bash
proxypilot repeat <id>                                         # send it again
proxypilot repeat <id> --set-header 'Authorization: Bearer other' --body @order.json
proxypilot repeat <id> --count 10 --concurrency 5               # race / flakiness repro
proxypilot repeat <id> --bypass-rules                          # skip Map Local, rewrite, breakpoints
```

`--count` is capped at 50 and `--concurrency` at 10, so a typo cannot turn into a load test.

## cURL

```bash
proxypilot curl <id>                 # secrets masked, safe to share
proxypilot --no-redact curl <id>     # the exact request, ready to run
proxypilot compose --from-curl "$(pbpaste)"
proxypilot compose --method POST --url https://api.example.com/v2/orders \
  --header 'Content-Type: application/json' --body @order.json
```

Commands copied from Chrome DevTools ("Copy as cURL") import as they are: headers, `--data-raw`, `-u`, `-b`, `-G`, `--json` and `--compressed` are understood.

The **Compose** page does the same: paste a cURL command, edit method, URL, headers and body, send it once or several times, and export it as cURL, `fetch`, Swift `URLRequest`, Kotlin OkHttp or Go `net/http`. From a request's detail panel, **Repeat** sends it again and **Edit in Composer** opens it there.
