---
title: Install and start
section: Getting started
---
# Install and start

ProxyPilot is a single binary: the proxy, the control API, this web UI and the CLI.

| System | Install |
|---|---|
| macOS | `brew install hakanor/tap/proxypilot` |
| Linux | `curl -fsSL https://raw.githubusercontent.com/hakanor/proxypilot/main/install.sh \| sh` |
| Windows | Download `proxypilot_<version>_windows_amd64.zip` from GitHub Releases and put `proxypilot.exe` on your PATH |

Then run `proxypilot ui`. From source: `make web && make build`, the binary is `bin/proxypilot`.

The **System proxy** switch works on macOS, Windows (per-user Internet Settings, no admin) and Linux with GNOME. `proxypilot trust` trusts the CA on macOS and Windows; on Linux it prints the steps.

`proxypilot ui` starts the daemon in the background if needed and opens this page. Other ways to run it:

| Command | What it does |
|---|---|
| `proxypilot start` | Run in the foreground (Ctrl+C stops it) |
| `proxypilot start --detach` | Run in the background, log to `{dataDir}/proxypilot.log` |
| `proxypilot start --proxy-listen 0.0.0.0:9090` | Accept phones on the same Wi-Fi |
| `proxypilot status` | Ports, flow count, CA fingerprint |
| `proxypilot stop` | Stop it and restore the system proxy |

Right now the proxy listens on `{proxy}` and the control API on `http://{api}`.

## Send something through it

```bash
curl -x http://{proxy} http://example.com/
```

The request appears in **Traffic** immediately.
