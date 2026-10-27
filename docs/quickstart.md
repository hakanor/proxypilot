# Quickstart: first traffic in 5 minutes

## 1. Install

From a release (macOS / Linux, checksum verified):

```bash
curl -fsSL https://raw.githubusercontent.com/hakanor/proxypilot/main/install.sh | sh
```

Homebrew: `brew install hakanor/tap/proxypilot`. From source (Go 1.26+, Node 20+): `make web build`, the binary is `bin/proxypilot`.

## 2. Start and open the UI

```bash
proxypilot ui
```

This starts the daemon in the background if needed and opens the web UI. The proxy listens on `127.0.0.1:9090`; the UI and control API on `127.0.0.1:9091` (this Mac only).

Check that it works:

```bash
curl -x http://127.0.0.1:9090 http://example.com/
```

The request appears in the Traffic list.

## 3. See HTTPS

ProxyPilot decrypts HTTPS with its own root certificate. Trust it once on this Mac (asks for your password):

```bash
proxypilot trust
```

## 4. Pick what to capture

Open **Setup** in the UI, or use the CLI:

| Source | How |
|---|---|
| Apps on this Mac, Safari, Chrome | Toolbar **System proxy** switch, or `proxypilot system-proxy on` |
| iOS Simulator | `proxypilot setup ios-sim --all-booted` (installs the CA), then the system proxy switch |
| iPhone / Android on the same Wi-Fi | Setup → Physical device: turn on **Allow devices on this network**, scan the QR code, follow the animated steps |
| Android emulator / USB device | `proxypilot setup android` |
| Chrome only, without touching the system | `proxypilot chrome launch` |

Device details and troubleshooting: [devices.md](devices.md).

## 5. Next

- Mock a response: right-click a request → Tools → Map Local, edit, Save.
- Pause and edit a request: right-click → Tools → Breakpoint.
- Slow network: click the address pill in the toolbar → Network throttle.
- Agents: `claude mcp add proxypilot -- proxypilot mcp --agent-name claude` ([mcp.md](mcp.md), [AGENTS.md](AGENTS.md)).

## Undo everything

```bash
proxypilot uninstall
```

Restores the system proxy, removes simulator certificates and lists anything that needs a manual step (like removing the CA from a phone).
