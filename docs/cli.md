# ProxyPilot CLI reference

Generated from `proxypilot <command> --help` (regenerate: `make docs`). Every command takes the global flags at the end; `--json` gives machine-readable output (schemas in [cli-json.md](cli-json.md)).

## `proxypilot breakpoint`

```text
Create breakpoints with `proxypilot rule add breakpoint`. Matching requests (or responses) wait here
until someone decides: continue unchanged, continue with edits, or abort. On timeout they continue.

Usage:
  proxypilot breakpoint [command]

Aliases:
  breakpoint, bp

Available Commands:
  edit        Edit a paused request/response as JSON in $EDITOR, then resume with it
  extend      Give a paused flow more time
  list        List paused flows
  resume      Continue a paused flow, optionally with edits, or abort it
  wait        Block until a flow is paused, then print it (exit 5 on timeout)

Flags:
  -h, --help   help for breakpoint
```

### `proxypilot breakpoint edit`

```text
Edit a paused request/response as JSON in $EDITOR, then resume with it

Usage:
  proxypilot breakpoint edit <id> [flags]

Flags:
  -h, --help   help for edit
```

### `proxypilot breakpoint extend`

```text
Give a paused flow more time

Usage:
  proxypilot breakpoint extend <id> [flags]

Flags:
  -h, --help          help for extend
      --seconds int   seconds to add (default 30)
```

### `proxypilot breakpoint list`

```text
List paused flows

Usage:
  proxypilot breakpoint list [flags]

Aliases:
  list, ls

Flags:
  -h, --help   help for list
```

### `proxypilot breakpoint resume`

```text
Continue a paused flow, optionally with edits, or abort it

Usage:
  proxypilot breakpoint resume <id> [flags]

Examples:
  proxypilot breakpoint resume bp_01… 
  proxypilot breakpoint resume bp_01… --status 500 --body '{"error":"boom"}'
  proxypilot breakpoint resume bp_01… --set-header 'Authorization: Bearer test' --body @req.json
  proxypilot breakpoint resume bp_01… --abort

Flags:
      --abort                   abort: 502 in the request phase, connection closed in the response phase
      --body string             replace the body (text, or @file)
  -h, --help                    help for resume
      --method string           change the method (request phase)
      --remove-header strings   remove a header (repeatable)
      --set-header strings      set a header, Name:value (repeatable)
      --status int              response status (response phase)
      --url string              change the URL (request phase, same scheme)
```

### `proxypilot breakpoint wait`

```text
Block until a flow is paused, then print it (exit 5 on timeout)

Usage:
  proxypilot breakpoint wait [flags]

Flags:
  -h, --help               help for wait
      --phase string       request or response
      --timeout duration   how long to wait (default 1m0s)
      --url string         only flows whose URL contains this (or matches a * pattern)
```

## `proxypilot ca`

```text
Manage the ProxyPilot root certificate

Usage:
  proxypilot ca [command]

Available Commands:
  export      Write the CA certificate (pem or der) to a file
  info        Show CA details and trust status
  regenerate  Create a new CA (every device must reinstall it)

Flags:
  -h, --help   help for ca
```

### `proxypilot ca export`

```text
Write the CA certificate (pem or der) to a file

Usage:
  proxypilot ca export [flags]

Flags:
      --format string   pem|der (default "pem")
  -h, --help            help for export
      --out string      output file
```

### `proxypilot ca info`

```text
Show CA details and trust status

Usage:
  proxypilot ca info [flags]

Flags:
  -h, --help   help for info
```

### `proxypilot ca regenerate`

```text
Create a new CA (every device must reinstall it)

Usage:
  proxypilot ca regenerate [flags]

Flags:
  -h, --help   help for regenerate
      --yes    confirm regeneration
```

## `proxypilot chrome`

```text
launch opens Chrome with a separate ProxyPilot profile (Chrome does not allow debugging your main profile).
attach connects to a Chrome you started with --remote-debugging-port=9222.
Rules (Map Local, rewrite, map remote, breakpoints) apply to Chrome traffic too.

Usage:
  proxypilot chrome [command]

Available Commands:
  attach      Connect to a running Chrome started with --remote-debugging-port
  detach      Stop capturing (a launched Chrome stays open)
  launch      Open Chrome with a ProxyPilot profile and capture its tabs
  status      Show the connection and the watched tabs

Flags:
  -h, --help   help for chrome
```

### `proxypilot chrome attach`

```text
Connect to a running Chrome started with --remote-debugging-port

Usage:
  proxypilot chrome attach [flags]

Flags:
  -h, --help          help for attach
      --port string   debugging port or host:port (default "9222")
```

### `proxypilot chrome detach`

```text
Stop capturing (a launched Chrome stays open)

Usage:
  proxypilot chrome detach [flags]

Flags:
  -h, --help   help for detach
```

### `proxypilot chrome launch`

```text
Open Chrome with a ProxyPilot profile and capture its tabs

Usage:
  proxypilot chrome launch [flags]

Flags:
  -h, --help         help for launch
      --url string   page to open
```

### `proxypilot chrome status`

```text
Show the connection and the watched tabs

Usage:
  proxypilot chrome status [flags]

Flags:
  -h, --help   help for status
```

## `proxypilot clear`

```text
Delete captured flows and start a new session

Usage:
  proxypilot clear [flags]

Flags:
  -h, --help   help for clear
      --yes    confirm
```

## `proxypilot compose`

```text
Send a new request through ProxyPilot (from flags or a pasted cURL command)

Usage:
  proxypilot compose [flags]

Examples:
  proxypilot compose --from-curl "$(pbpaste)"
  proxypilot compose --method POST --url https://api.x.com/v2/orders --header 'Content-Type: application/json' --body @order.json

Flags:
      --body string        body (text or @file)
      --bypass-rules       skip rules
      --concurrency int    how many at once (max 10) (default 1)
      --count int          send it this many times (max 50) (default 1)
      --from-curl string   a cURL command (or @file)
      --header strings     'Name: value' (repeatable)
  -h, --help               help for compose
      --method string      method (default "GET")
      --url string         absolute URL
```

## `proxypilot curl`

```text
Print a recorded request as a cURL command (secrets masked unless --no-redact)

Usage:
  proxypilot curl <id> [flags]

Flags:
  -h, --help   help for curl
```

## `proxypilot export`

```text
Export flows (HAR, session file); secrets masked unless --no-redact

Usage:
  proxypilot export [command]

Available Commands:
  har         Export as HAR 1.2 (Chrome DevTools, Proxyman, Charles can open it)

Flags:
  -h, --help   help for export
```

### `proxypilot export har`

```text
Export as HAR 1.2 (Chrome DevTools, Proxyman, Charles can open it)

Usage:
  proxypilot export har [flags]

Examples:
  proxypilot export har --since 10m > out.har
  proxypilot export har --host api.example.com -o api.har

Flags:
      --errors          only failed requests (transport errors, GraphQL errors)
      --gql-op string   GraphQL operation name
  -h, --help            help for har
      --host string     host, wildcards allowed (*.example.com)
      --ids string      only these flow ids, comma-separated
      --method string   methods, comma-separated (GET,POST)
  -o, --output string   write to a file instead of stdout
      --since string    only newer than a duration (5m) or RFC 3339 time
      --source string   proxy | cdp | composer | import
      --status string   status: 2xx | 4xx,5xx | >=500 | 404 | 400-499
      --type string     response content type contains (json, image)
```

## `proxypilot import`

```text
Import a HAR or session file into a new, separate session

Usage:
  proxypilot import [command]

Available Commands:
  har         Import a HAR file (source=import)

Flags:
  -h, --help   help for import
```

### `proxypilot import har`

```text
Import a HAR file (source=import)

Usage:
  proxypilot import har <file> [flags]

Flags:
  -h, --help   help for har
```

## `proxypilot list`

```text
List captured flows, newest first

Usage:
  proxypilot list [flags]

Flags:
      --cursor string   continue from a previous page
      --errors          only failed requests (transport errors, GraphQL errors)
      --gql-op string   GraphQL operation name
  -h, --help            help for list
      --host string     host, wildcards allowed (*.example.com)
      --limit int       max flows (≤ 500) (default 20)
      --method string   methods, comma-separated (GET,POST)
      --since string    only newer than a duration (5m) or RFC 3339 time
      --source string   proxy | cdp | composer | import
      --status string   status: 2xx | 4xx,5xx | >=500 | 404 | 400-499
      --type string     response content type contains (json, image)
```

## `proxypilot mcp`

```text
Serves ProxyPilot tools over MCP on stdin/stdout. Configure it in your agent, e.g.
  claude mcp add proxypilot -- proxypilot mcp --agent-name claude
Rules the agent creates are owned by agent:<name> and expire after 1h by default.
Secrets are always masked unless you pass --allow-secrets (agents cannot change it).

Usage:
  proxypilot mcp [flags]

Flags:
      --agent-name string   owner name for rules the agent creates (agent:<name>) (default "agent")
      --allow-secrets       do not mask secrets in tool results
      --auto-start          start the daemon if it is not running
  -h, --help                help for mcp
      --read-only           only register tools that do not change anything
```

## `proxypilot repeat`

```text
Send a recorded request again, optionally edited (results are recorded as source=composer)

Usage:
  proxypilot repeat <id> [flags]

Examples:
  proxypilot repeat 01M5…
  proxypilot repeat 01M5… --set-header 'Authorization: Bearer other' --body @order.json
  proxypilot repeat 01M5… --count 10 --concurrency 5      # race / flakiness repro

Flags:
      --body string             replace the body (text or @file)
      --bypass-rules            skip rules (Map Local, rewrite, breakpoints)
      --concurrency int         how many at once (max 10) (default 1)
      --count int               send it this many times (max 50) (default 1)
  -h, --help                    help for repeat
      --method string           change the method
      --remove-header strings   remove a header
      --set-header strings      set a header, 'Name: value' (repeatable)
      --set-query strings       set a query parameter, name=value
      --url string              change the URL
```

## `proxypilot rule`

```text
Manage rules: Map Local, Map Remote, Rewrite, Breakpoint

Usage:
  proxypilot rule [command]

Available Commands:
  add         Add a rule
  clear       Delete every rule of an owner
  list        List rules in evaluation order
  rm          Delete rules
  show        Show one rule as JSON
  toggle      Enable or disable a rule (flips it without --on/--off)

Flags:
  -h, --help   help for rule
```

### `proxypilot rule add`

```text
Add a rule

Usage:
  proxypilot rule add [command]

Available Commands:
  breakpoint  Pause matching requests and/or responses until someone decides
  map-local   Answer matching requests with a local file, folder or inline body
  map-remote  Send matching requests to another server (prod → staging, or localhost)
  rewrite     Edit matching requests or responses: headers, query, body, JSON fields, status, URL

Flags:
  -h, --help   help for add
```

### `proxypilot rule clear`

```text
Delete every rule of an owner

Usage:
  proxypilot rule clear --owner agent|agent:<name>|user|all [flags]

Flags:
  -h, --help           help for clear
      --owner string   agent, agent:<name>, user or all
```

### `proxypilot rule list`

```text
List rules in evaluation order

Usage:
  proxypilot rule list [flags]

Aliases:
  list, ls

Flags:
  -h, --help           help for list
      --owner string   filter: user, agent, agent:<name>
```

### `proxypilot rule rm`

```text
Delete rules

Usage:
  proxypilot rule rm <id>... [flags]

Aliases:
  rm, delete

Flags:
  -h, --help   help for rm
```

### `proxypilot rule show`

```text
Show one rule as JSON

Usage:
  proxypilot rule show <id> [flags]

Flags:
  -h, --help   help for show
```

### `proxypilot rule toggle`

```text
Enable or disable a rule (flips it without --on/--off)

Usage:
  proxypilot rule toggle <id> [flags]

Flags:
  -h, --help   help for toggle
      --off    disable
      --on     enable
```

## `proxypilot search`

```text
Search flows by URL, body or headers

Usage:
  proxypilot search <text> [flags]

Flags:
  -h, --help        help for search
      --in string   where to search: url | body | headers (default "url")
      --limit int   max flows (≤ 500) (default 20)
```

## `proxypilot session`

```text
Save or open .proxypilot session files (flows + bodies in one file)

Usage:
  proxypilot session [command]

Available Commands:
  open        Open a session file as a new, separate session
  save        Save flows and their bodies to one file (secrets masked unless --no-redact)

Flags:
  -h, --help   help for session
```

### `proxypilot session open`

```text
Open a session file as a new, separate session

Usage:
  proxypilot session open <file.proxypilot> [flags]

Flags:
  -h, --help   help for open
```

### `proxypilot session save`

```text
Save flows and their bodies to one file (secrets masked unless --no-redact)

Usage:
  proxypilot session save <file.proxypilot> [flags]

Flags:
      --errors          only failed requests (transport errors, GraphQL errors)
      --gql-op string   GraphQL operation name
  -h, --help            help for save
      --host string     host, wildcards allowed (*.example.com)
      --ids string      only these flow ids, comma-separated
      --method string   methods, comma-separated (GET,POST)
      --since string    only newer than a duration (5m) or RFC 3339 time
      --source string   proxy | cdp | composer | import
      --status string   status: 2xx | 4xx,5xx | >=500 | 404 | 400-499
      --type string     response content type contains (json, image)
```

## `proxypilot setup`

```text
Configure simulators, devices and browsers

Usage:
  proxypilot setup [command]

Available Commands:
  android     Point an Android emulator or USB device at ProxyPilot and install the CA
  device      Connect a physical iPhone or Android phone (QR code + steps)
  ios-sim     Install the ProxyPilot CA into iOS simulators

Flags:
  -h, --help   help for setup
```

### `proxypilot setup android`

```text
Emulators use 10.0.2.2 (the host). USB devices use `adb reverse`, so Wi-Fi is not needed.
--ca user copies the certificate to Downloads for a manual install (apps trust it only in debug builds);
--ca system installs it as a system CA on emulators without Google Play or rooted devices (until reboot).

Usage:
  proxypilot setup android [flags]

Examples:
  proxypilot setup android --list
  proxypilot setup android                       # the only connected device, default mode
  proxypilot setup android --serial emulator-5554 --ca system
  proxypilot setup android --remove

Flags:
      --ca string       user, system or none (default "user")
  -h, --help            help for android
      --list            list connected devices
      --mode string     emulator, usb or wifi (default: emulator for emulators, usb for devices)
      --remove          undo: clear the proxy and the USB port forward
      --serial string   device serial (from --list); needed when several are connected
```

### `proxypilot setup device`

```text
Connect a physical iPhone or Android phone (QR code + steps)

Usage:
  proxypilot setup device [flags]

Flags:
  -h, --help    help for device
      --no-qr   do not print the QR code
```

### `proxypilot setup ios-sim`

```text
Install the ProxyPilot CA into iOS simulators

Usage:
  proxypilot setup ios-sim [flags]

Flags:
      --all-booted     install into every booted simulator
  -h, --help           help for ios-sim
      --udid strings   simulator UDID (repeatable)
```

## `proxypilot show`

```text
Show one flow (headers; --body for bodies)

Usage:
  proxypilot show <id> [flags]

Flags:
      --body              include bodies
  -h, --help              help for show
      --max-body string   truncate bodies to this size (default "4kb")
      --messages          include WebSocket messages
      --raw               write the full decoded body to stdout (one side, default resp)
      --side string       req | resp | both (default "both")
```

## `proxypilot start`

```text
Start the proxy daemon (foreground; --detach for background)

Usage:
  proxypilot start [flags]

Flags:
      --detach                run in the background
      --disable-h2            speak only HTTP/1.1 on intercepted connections (if an app misbehaves with HTTP/2)
  -h, --help                  help for start
      --proxy-listen string   proxy listen address (default 127.0.0.1:9090; 0.0.0.0:9090 for devices)
      --unsafe-remote-api     allow the control API on a non-loopback address (dangerous)
```

## `proxypilot status`

```text
Show daemon status

Usage:
  proxypilot status [flags]

Flags:
  -h, --help   help for status
```

## `proxypilot stop`

```text
Stop the running daemon (and restore the macOS system proxy if ProxyPilot set it)

Usage:
  proxypilot stop [flags]

Flags:
  -h, --help                help for stop
      --keep-system-proxy   leave the macOS system proxy pointing at ProxyPilot
```

## `proxypilot summary`

```text
Short traffic summary: counts, failures, slowest request

Usage:
  proxypilot summary [flags]

Flags:
  -h, --help           help for summary
      --since string   window: duration (5m, 1h), RFC 3339, or all (default "5m")
```

## `proxypilot system-proxy`

```text
Point the macOS system proxy at ProxyPilot (on|off|status)

Usage:
  proxypilot system-proxy [command]

Available Commands:
  off         Restore the network proxy settings saved by `system-proxy on`
  on          Back up the current settings and send HTTP/HTTPS of every network service to ProxyPilot
  status      Show the current macOS proxy settings

Flags:
  -h, --help   help for system-proxy
```

### `proxypilot system-proxy off`

```text
Restore the network proxy settings saved by `system-proxy on`

Usage:
  proxypilot system-proxy off [flags]

Flags:
  -h, --help   help for off
```

### `proxypilot system-proxy on`

```text
Back up the current settings and send HTTP/HTTPS of every network service to ProxyPilot

Usage:
  proxypilot system-proxy on [flags]

Flags:
  -h, --help   help for on
```

### `proxypilot system-proxy status`

```text
Show the current macOS proxy settings

Usage:
  proxypilot system-proxy status [flags]

Flags:
  -h, --help   help for status
```

## `proxypilot tail`

```text
Stream flows live (--json: NDJSON)

Usage:
  proxypilot tail [flags]

Flags:
      --errors          only failed requests (transport errors, GraphQL errors)
      --events string   event patterns (flow.*, breakpoint.*, suggestion) (default "flow.completed,flow.failed")
      --gql-op string   GraphQL operation name
  -h, --help            help for tail
      --host string     host, wildcards allowed (*.example.com)
      --method string   methods, comma-separated (GET,POST)
      --source string   proxy | cdp | composer | import
      --status string   status: 2xx | 4xx,5xx | >=500 | 404 | 400-499
      --type string     response content type contains (json, image)
```

## `proxypilot throttle`

```text
Profiles: edge, 3g, lte, bad-wifi, or custom with --down/--up/--latency.
Every request waits the profile's latency, then bodies flow at its bandwidth.
--host limits it to some hosts (repeat, or comma-separate; *.example.com works).

Usage:
  proxypilot throttle [command]

Available Commands:
  off         Turn throttling off
  on          Turn throttling on with a profile
  status      Show the current throttle

Flags:
  -h, --help   help for throttle
```

### `proxypilot throttle off`

```text
Turn throttling off

Usage:
  proxypilot throttle off [flags]

Flags:
  -h, --help   help for off
```

### `proxypilot throttle on`

```text
Turn throttling on with a profile

Usage:
  proxypilot throttle on <edge|3g|lte|bad-wifi|custom> [flags]

Flags:
      --down int       custom: download kbit/s
  -h, --help           help for on
      --host strings   only throttle these hosts
      --latency int    custom: added latency in ms
      --up int         custom: upload kbit/s
```

### `proxypilot throttle status`

```text
Show the current throttle

Usage:
  proxypilot throttle status [flags]

Flags:
  -h, --help   help for status
```

## `proxypilot trust`

```text
Trust the CA in the macOS System keychain

Usage:
  proxypilot trust [flags]

Flags:
  -h, --help   help for trust
```

## `proxypilot tui`

```text
Terminal UI: live traffic, detail, repeat, breakpoints (? for keys)

Usage:
  proxypilot tui [flags]

Flags:
  -h, --help   help for tui
```

## `proxypilot ui`

```text
Open the web UI (starts the daemon if needed)

Usage:
  proxypilot ui [flags]

Flags:
  -h, --help      help for ui
      --no-open   only print the URL
```

## `proxypilot uninstall`

```text
Without --yes it only shows the plan. With --yes it stops the daemon, restores the system proxy,
removes the CA from iOS simulators and prints the admin commands for the macOS keychain.
--purge also deletes the data directory (CA key, flows, token).

Usage:
  proxypilot uninstall [flags]

Flags:
  -h, --help    help for uninstall
      --purge   also delete the data directory (with --yes)
      --yes     apply the automatic steps
```

## `proxypilot untrust`

```text
Remove the CA from the macOS System keychain

Usage:
  proxypilot untrust [flags]

Flags:
  -h, --help   help for untrust
```

## `proxypilot version`

```text
Print version information

Usage:
  proxypilot version [flags]

Flags:
  -h, --help   help for version
```

## `proxypilot wait`

```text
Wait blocks until a matching flow completes (exit 0) or the timeout passes (exit 5).
Typical agent loop: start waiting, trigger the action in the app, then inspect the printed flow.
--since also accepts flows that completed shortly before wait started.

Usage:
  proxypilot wait [flags]

Flags:
  -h, --help               help for wait
      --method string      method(s), comma-separated
      --since string       also match flows completed within this window before waiting (e.g. 10s)
      --status string      status filter (2xx, >=400, 404)
      --timeout duration   give up after this long (exit 5) (default 1m0s)
      --url string         URL substring or * pattern (required)
```

## Global flags

```text
Flags:
      --api-addr string     control API address (default 127.0.0.1:9091)
      --config string       config file (default <data-dir>/config.yaml)
      --data-dir string     data directory (env PROXYPILOT_HOME)
  -h, --help                help for proxypilot
      --json                machine-readable JSON output
      --log-format string   text|json (default "text")
      --log-level string    debug|info|warn|error (default "info")
      --no-color            disable colored output
      --no-redact           show secrets (tokens, cookies, passwords) in output
  -q, --quiet               only print errors
      --token string        control API token (default: read from <data-dir>/token)

```

## Exit codes

| Code | Name | Meaning |
|---|---|---|
| 0 | ExitOK | Success |
| 1 | ExitError | Something failed (message on stderr) |
| 2 | ExitUsage | Wrong flags or arguments |
| 3 | ExitNoDaemon | The daemon is not running (`proxypilot start --detach`) |
| 4 | ExitNotFound | The flow, rule or breakpoint does not exist |
| 5 | ExitTimeout | `wait` / breakpoint wait timed out |
| 6 | ExitUnauthorized | The control API rejected the token |
