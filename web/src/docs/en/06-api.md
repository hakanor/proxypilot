---
title: HTTP API
section: Reference
---
# HTTP API

The control API listens on `http://{api}` (loopback only). Authenticate with the token in `{dataDir}/token`:

```bash
TOKEN=$(cat '{dataDir}/token')
curl -H "Authorization: Bearer $TOKEN" "http://{api}/api/v1/flows?status=5xx&limit=20"
```

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/v1/status` | Daemon status |
| GET | `/api/v1/flows` | List flows (`host`, `status`, `method`, `q`, `limit`, `cursor`) |
| GET | `/api/v1/flows/{id}` | One flow with headers (`redact=true` to mask) |
| GET | `/api/v1/flows/{id}/body/{req\|resp}` | Body (`max`, `raw=true`, `redact=true`) |
| GET | `/api/v1/summary` | Totals and failures (`since=5m`) |
| GET | `/api/v1/stream` | WebSocket event stream |
| GET | `/api/v1/stream/ndjson` | Same as NDJSON (for curl) |
| GET/PUT | `/api/v1/ssl-policy` | Interception mode and lists |
| GET/POST | `/api/v1/rules` | List / create rules (`ttl` sets an expiry) |
| GET/PUT/DELETE | `/api/v1/rules/{id}` | One rule |
| POST | `/api/v1/rules/{id}/toggle` | Enable or disable |
| DELETE | `/api/v1/rules?owner=agent:*` | Delete rules by owner |
| POST | `/api/v1/flows/{id}/map-local` | Save a response as a Map Local rule |
| GET | `/api/v1/breakpoints` | Paused flows |
| GET | `/api/v1/breakpoints/wait` | Long-poll for the next pause (`timeout`, `url`, `phase`) |
| POST | `/api/v1/breakpoints/{id}/resume` | `{"action":"continue\|modify\|abort","request"\|"response":{…}}`; 409 if already resolved |
| POST | `/api/v1/breakpoints/{id}/extend` | `{"seconds":30}` |
| POST | `/api/v1/breakpoints/{id}/take-over` | Take an agent's breakpoint |
| POST | `/api/v1/flows/{id}/repeat` | `{set_headers, remove_headers, set_query, body, method, url, count, concurrency, bypass_rules}` |
| GET | `/api/v1/flows/{id}/request` | Editable request (not redacted) |
| GET | `/api/v1/flows/{id}/curl` | cURL command (`redact=true` to mask) |
| POST | `/api/v1/compose` | `{method, url, headers, body}` or `{curl}`, plus `count`, `concurrency` |
| POST | `/api/v1/curl/parse` | cURL command → request |
| GET/POST | `/api/v1/export/har` | HAR 1.2 (list filters, or POST `{ids, redact}`); masked unless `redact=false` |
| GET/POST | `/api/v1/export/session` | `.proxypilot` zip |
| POST | `/api/v1/import` | Body: a HAR or a session file |
| PUT | `/api/v1/recording` | `{"recording": false}` pauses capture |
| GET | `/api/v1/devices/setup` | LAN addresses, reachability, firewall state |
| GET | `/api/v1/devices/clients` | Devices seen by the proxy and their HTTPS state |
| PUT | `/api/v1/devices/clients/{ip}` | `{"name": "…"}` renames a device |
| GET | `/api/v1/chrome` | Connection and tabs |
| POST | `/api/v1/chrome/launch` / `attach` / `detach` | `{"url"}` / `{"addr":"127.0.0.1:9222"}` |
| PUT | `/api/v1/chrome/tabs/{id}` | `{"watch": false}` stops capturing a tab |
| GET | `/api/v1/devices/android` | adb devices with proxy and CA state |
| POST | `/api/v1/devices/android/setup` | `{"serial","mode","ca"}` |
| POST | `/api/v1/devices/android/remove` | `{"serial"}` |
| GET | `/api/v1/devices/simulators` | iOS simulators and CA state |
| GET/PUT | `/api/v1/system-proxy` | macOS system proxy |
| GET/POST | `/api/v1/uninstall` | Cleanup plan / run automatic cleanup |

Errors look like `{"error": {"code": "not_found", "message": "…"}}`. The full schema is in `docs/openapi.yaml`.
