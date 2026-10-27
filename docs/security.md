# Security

ProxyPilot sees and can change everything that goes through it. These are the parts that matter.

## The root certificate

- `proxypilot trust` (and installing the CA on a device) lets ProxyPilot impersonate **any** HTTPS site to that machine. Anyone holding the CA's private key could do the same.
- The key is created on first start in `<data-dir>/ca/` with `0600` permissions and never leaves the machine. It is unique per installation.
- Only trust it on machines and test devices you use for development. Remove it when you are done: `proxypilot untrust` on the Mac, Settings → General → VPN & Device Management on iOS, Settings → Security → Trusted credentials → User on Android. `proxypilot uninstall` lists what is left.
- Hosts in the SSL denylist, and apps that pin certificates, are tunneled without decryption.

## Opening the proxy to the network

- By default the proxy listens on `127.0.0.1:9090`: only this Mac can use it.
- **Allow devices on this network** (toolbar pill or Setup) or `--proxy-listen 0.0.0.0:9090` lets anyone on the same network send traffic through it, and see your proxy's certificate page. They cannot read your captured traffic: that is only on the control API.
- Turn it off when you are done, and never on shared networks (cafés, conferences). The setting is remembered across restarts; the toolbar shows a **LAN** badge while it is on.

## The control API and UI (port 9091)

- Bound to `127.0.0.1` only. `--unsafe-remote-api` is the only way to change that and it is not meant for normal use.
- Every request needs the token in `<data-dir>/token` (`0600`). The UI receives it once through `proxypilot ui` and keeps it in that browser; other websites you visit cannot call the API.
- Requests to the API never pass through the proxy and are never recorded.

## Masking secrets

- `Authorization`, cookies, API keys, tokens and passwords are masked (`Bearer eyJh…[redacted, 812 chars]`) in CLI output, exports (HAR / session files) and everything an MCP agent sees.
- `--no-redact` (CLI), "Include secrets" (export) and `--allow-secrets` (MCP) turn it off. Only the user can set them; an agent cannot.
- The web UI shows real values: it runs locally for you.
- Breakpoint edits are built from the unmasked original, so a masked value is never sent upstream.
- Extra header names to mask: `redact.headers` in `<data-dir>/config.yaml`.

## Agents (MCP)

- Rules an agent creates are owned by `agent:<name>`, expire (1 hour by default, 24 hours at most) and live in memory only.
- An agent can remove only its own rules and resume only breakpoints its own rules created.
- `--read-only` registers only the read tools.
- `set_throttle` and `clear_session` affect everyone using the proxy; the agent guide tells agents to ask first.

## Data on disk

Captured traffic lives in `<data-dir>/proxypilot.db` and `<data-dir>/bodies/` (default `~/Library/Application Support/ProxyPilot`), including secrets in their original form. Clear it with the toolbar trash icon or `proxypilot clear`; retention keeps the newest `store.max_entries` flows (default 10 000).
