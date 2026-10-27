---
title: Troubleshooting
section: Reference
---
# Troubleshooting

## No traffic appears

- Check the client really uses the proxy: `curl -x http://{proxy} http://example.com/` must show up.
- Simulators follow the Mac's settings: `proxypilot system-proxy status`.
- Phones need `--proxy-listen 0.0.0.0:9090` and the Mac's LAN address in their Wi-Fi proxy settings.

## `client_tls_rejected`

The client closed the connection after seeing the ProxyPilot certificate: the CA is not trusted on that device, or the app pins its certificate. Install the CA (see **Setup**) or add the host to the SSL bypass list.

## The Mac has no internet after ProxyPilot stopped

The system proxy still points at the stopped daemon. `proxypilot stop` normally restores it; if the daemon crashed, run:

```bash
proxypilot system-proxy off
```

## Something else

```bash
tail -f '{dataDir}/proxypilot.log'
proxypilot start --log-level debug
```
