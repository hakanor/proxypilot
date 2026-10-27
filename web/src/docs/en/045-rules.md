---
title: Rules and Map Local
section: CLI
---
# Rules and Map Local

A Map Local rule answers matching requests with local content. In `full` mode the server is never contacted; in `body_only` mode the request goes upstream and only the response body is replaced.

```bash
# A file, read again on every request: edit it and the next request sees the change
proxypilot rule add map-local --url 'https://api.example.com/v2/feed*' --file ./mocks/feed.json

# A folder: the part of the URL after * is looked up in ./dist
proxypilot rule add map-local --url 'https://cdn.example.com/js/*' --dir ./dist

# An inline error, only for POST, removed automatically after 10 minutes
proxypilot rule add map-local --url '*/checkout' --method POST --status 500 \
  --body '{"error":"boom"}' --ttl 10m

# Start from a real response: copies it to a file you can edit
proxypilot rule add map-local --from <flow-id>
```

## Rewrite

Edit requests or responses in flight. Steps run in order; compressed bodies are decoded first and sent back uncompressed.

```bash
proxypilot rule add rewrite --url '*/v2/me' --phase response \
  --json-set '$.user.isPremium=true' --set-header 'X-Debug: 1'
proxypilot rule add rewrite --url '*/api/*' --set-header 'Authorization: Bearer test' --remove-query debug
proxypilot rule add rewrite --url '*/feed' --phase response --replace 'Sold out=>In stock' --status 200
```

| Step | Flag | Phase |
|---|---|---|
| set / add / remove header | `--set-header 'K: V'`, `--add-header`, `--remove-header K` | both |
| set / remove query | `--set-query k=v`, `--remove-query k` | request |
| replace in body | `--replace 'old=>new'`, `--replace-regex 'p=>r'` | both |
| JSON field | `--json-set '$.a.b[0]=value'`, `--json-remove '$.a'` | both |
| status | `--status 503` | response |
| URL / path | `--set-url URL`, `--set-path /v3/x` | request |

Flags run in the order of this table; for any other order pass `--steps-file steps.json` with a list like `[{"type":"json_set","path":"$.a","value":"1"}]`.

## Map Remote

Send matching requests somewhere else; the flow records both the original and the target URL.

```bash
proxypilot rule add map-remote --url 'https://api.example.com/*' --to 'https://staging.example.com/*'
proxypilot rule add map-remote --url 'https://api.example.com/v2/feed*' --to http://localhost:3000/feed --preserve-host
```

## Matching

| Field | Example | Meaning |
|---|---|---|
| `--url` | `https://api.x.com/users/*` | Wildcard `*`; without a scheme it matches http and https; a bare host matches everything on it |
| `--regex` | `^https://api\.x\.com/orders/\d+$` | Treat `--url` as a regular expression |
| `--method` | `POST` | Repeatable; empty means any |
| `--match-header` | `Accept:application/*` | Header value with wildcards |
| `--priority` | `10` | Higher runs first; ties go to the older rule |

The first matching Map Local wins. Every matching rule is recorded on the flow (`matched_rules`) and shown as **R** in the traffic list.

## Managing rules

```bash
proxypilot rule list
proxypilot rule toggle <id> --off
proxypilot rule rm <id>
proxypilot rule clear --owner agent
```

## Rules created by agents

Agents should set an owner and a TTL so their rules never outlive the task:

```bash
PROXYPILOT_OWNER=agent:claude proxypilot rule add map-local --url '*/v2/feed*' --body '[]' --ttl 15m
```

Agent rules live in memory only and show their countdown on the **Rules** page; user rules are saved to `{dataDir}/rules.json`.
