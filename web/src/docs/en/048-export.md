---
title: Export, import and GraphQL
section: CLI
---
# Export, import and GraphQL

## HAR and session files

```bash
proxypilot export har --since 10m > out.har           # secrets masked
proxypilot --no-redact export har --host api.example.com -o api.har
proxypilot import har recording.har                   # Chrome DevTools, Proxyman, Charles…
proxypilot session save bug-1234.proxypilot --since 30m
proxypilot session open bug-1234.proxypilot
```

- HAR 1.2 opens in Chrome DevTools (Network → Import HAR). Text bodies stay text; binary bodies are base64.
- A `.proxypilot` session file packs flows, headers and decoded bodies into one zip — handy to attach to a bug.
- Imports land in a separate session with source **Import**; the original source is kept in `tags.original_source`.
- Exports mask secrets unless you pass `--no-redact` (or tick **Include secrets** in the Export menu).

The **Export** and **Import** buttons in Traffic do the same; Export can take only the rows that match the current filter.

## GraphQL

Requests with a JSON `query`, `Content-Type: application/graphql`, or `GET ?query=` are recognized:

- The list shows the operation (`mutation Like`) instead of the path; the detail panel gets a **GraphQL** tab with the formatted query, variables, and `data` / `errors` side by side.
- A `200` response whose `errors` is not empty counts as an error (`graphql_error`), so it shows up in `summary` and `--errors`.
- Filter by operation, and mock one operation of a shared `/graphql` endpoint:

```bash
proxypilot list --gql-op FeedQuery
proxypilot rule add map-local --url '*/graphql' --gql-op Like --body '{"errors":[{"message":"rate limited"}]}'
proxypilot rule add breakpoint --url '*/graphql' --gql-op Checkout --phase response
```
