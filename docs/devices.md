# Devices and browsers

Every device sends its traffic to the proxy port (default `9090`) and trusts the ProxyPilot root certificate so HTTPS can be decrypted. The **Setup** page in the UI walks through each one; this page is the same in text, plus troubleshooting.

## This Mac (apps, Safari, Chrome)

- Turn on the toolbar **System proxy** switch (or `proxypilot system-proxy on`). It backs up your current settings and points every network service at ProxyPilot; `off` or `proxypilot stop` restores them.
- `proxypilot trust` adds the CA to the System keychain (password prompt). Safari, Chrome and most apps then accept it.
- Apps are identified by process: the sidebar groups traffic under **This Mac → app**.

## iOS Simulator

Simulators use the Mac's network settings, so they follow the system proxy.

```bash
proxypilot setup ios-sim --all-booted   # install the CA into every booted simulator
proxypilot system-proxy on
```

Restart the app after installing so it picks up the new trust. The CA stays installed when the simulator shuts down. Simulator traffic shows up under **Simulators** in the sidebar.

Without the system proxy, only apps that set a proxy themselves are captured (e.g. a debug build with `URLSessionConfiguration.connectionProxyDictionary` pointing at `127.0.0.1:9090`).

## iPhone / iPad

1. Same Wi-Fi as the Mac. In the toolbar's address pill (or Setup → Physical device) turn on **Allow devices on this network**. This opens port 9090 to the network; do it on networks you trust and turn it off afterwards.
2. Scan the QR code on the Setup page, or open `http://<mac-ip>:9090/` in Safari. The page detects iOS and shows an animated walkthrough:
   - Settings → Wi-Fi → (i) → Configure Proxy → Manual, server = the Mac's IP, port 9090.
   - Safari: `http://proxypilot.cert` → Download profile → Settings → Profile Downloaded → Install.
   - Settings → General → About → Certificate Trust Settings → turn on ProxyPilot CA.
3. The phone appears under **Devices** with its IP; rename it on the Setup page.

Turn the phone's proxy off when you are done: with ProxyPilot stopped it cannot reach the internet.

## Android

**Emulator or USB device** (adb on PATH):

```bash
proxypilot setup android            # sets the proxy and pushes the CA
proxypilot setup android --remove   # undo
```

**Over Wi-Fi**: same as iPhone (Allow devices on this network, QR code). Android steps: Settings → Network & internet → Internet → your network → Edit → Advanced → Proxy: Manual; then download `http://proxypilot.cert` and install it under Security → Encryption & credentials → Install a certificate → CA certificate.

Android apps trust user-installed CAs **only in debug builds** that opt in with a `network_security_config`:

```xml
<network-security-config>
  <debug-overrides>
    <trust-anchors><certificates src="user" /></trust-anchors>
  </debug-overrides>
</network-security-config>
```

Browsers work without it.

## Chrome without the system proxy

`proxypilot chrome launch` opens Chrome with a separate profile and captures it over DevTools, no proxy or certificate needed. `proxypilot chrome attach` connects to a Chrome you started with `--remote-debugging-port=9222`. Your normal Chrome profile can also be captured with the system proxy (above).

## Troubleshooting

| Symptom | Cause and fix |
|---|---|
| Nothing arrives from a phone | Network access is off (toolbar pill → Allow devices on this network), the phone is on another network, or the macOS firewall blocks incoming connections: System Settings → Network → Firewall → Options → allow ProxyPilot. |
| `Certificate rejected` on a host | The device doesn't trust the CA yet (iOS: the Certificate Trust Settings switch), or the app pins its certificate. Right-click one of its requests → **Bypass SSL for <host>** so it is tunneled untouched. |
| Flutter / React Native apps ignore the proxy | Flutter's `HttpClient` ignores system proxies: set `HttpClient.findProxy` in debug builds, or route the device through the system proxy with a VPN-style tool. React Native uses the platform stack and works like a native app. |
| An app breaks with HTTP/2 | Start with `proxypilot start --detach --disable-h2` to keep both sides on HTTP/1.1. |
| Requests hang | A breakpoint rule is pausing them (Breakpoints page), or a network throttle is on (badge in the toolbar pill). |
| The phone has no internet after testing | Its Wi-Fi proxy still points at the Mac: set Configure Proxy back to Off. |
