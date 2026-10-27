---
title: Breakpoints
section: CLI
---
# Breakpoints

A breakpoint rule pauses matching requests before they reach the server (request phase), or responses before they reach the client (response phase). Other traffic keeps flowing while a flow waits.

```bash
proxypilot rule add breakpoint --url '*/checkout' --phase response --timeout 2m
```

Decide on the **Breakpoints** page, or from the terminal:

```bash
proxypilot breakpoint wait --timeout 60s --json      # blocks until something pauses (exit 5 on timeout)
proxypilot breakpoint resume <id>                    # continue unchanged
proxypilot breakpoint resume <id> --status 500 --body '{"error":"boom"}'
proxypilot breakpoint resume <id> --set-header 'X-Debug: 1' --body @request.json
proxypilot breakpoint resume <id> --abort            # request phase: 502, response phase: connection closed
proxypilot breakpoint edit <id>                      # edit as JSON in $EDITOR
proxypilot breakpoint extend <id> --seconds 60
```

## Rules of the game

- When time runs out the flow continues unchanged (`--on-timeout abort` to abort instead) and is tagged `breakpoint_timed_out`.
- The first decision wins; a second one gets **409 already_resolved**.
- Compressed responses are shown decoded. An edited body is sent uncompressed.
- Edits always start from the real values, even though `wait` output is redacted by default.
- Stopping the daemon continues every paused flow.

## For agents

```bash
PROXYPILOT_OWNER=agent:claude proxypilot rule add breakpoint --url '*/v2/feed*' --phase response --ttl 15m
proxypilot breakpoint wait --url /v2/feed --json
proxypilot breakpoint resume <id> --status 503
```

Breakpoints from agent rules show who holds them; a person can **Take over** at any time.
