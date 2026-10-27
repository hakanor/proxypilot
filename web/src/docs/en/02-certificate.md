---
title: Trust the certificate
section: Getting started
---
# Trust the certificate

To read HTTPS, clients must trust the ProxyPilot root CA. It is created on first start in `{dataDir}/ca/` and never leaves this machine.

```bash
proxypilot ca info
proxypilot ca export --format pem > proxypilot-ca.pem
```

## macOS (apps and browsers on this Mac)

`trust` prints the command when it is not run as root; ProxyPilot never runs `sudo` for you.

```bash
proxypilot trust
```

## iOS Simulator

Install the CA into every booted simulator, then send the Mac's traffic through the proxy:

```bash
proxypilot setup ios-sim --all-booted
proxypilot system-proxy on
```

The **Setup** page does the same with buttons. Restart the app in the simulator afterwards.

## Chrome without a proxy (DevTools)

```bash
proxypilot chrome launch --url https://example.com
proxypilot chrome status
proxypilot chrome detach
```

`launch` opens Chrome with a separate ProxyPilot profile and captures its tabs over the DevTools protocol, so no proxy or certificate is needed. (Chrome does not allow debugging your main profile.) To use a Chrome you started yourself with `--remote-debugging-port=9222`, run `proxypilot chrome attach --port 9222` instead. Requests show up with source **Chrome** and are grouped under **Chrome Tabs** in the sidebar. The same rules (Map Local, rewrite, map remote, breakpoints) apply. The first launch may make macOS ask whether Chrome may use its keychain or accept connections; denying is fine.

## Android emulator and USB devices (adb)

```bash
proxypilot setup android --list
proxypilot setup android                    # the only device; emulator → 10.0.2.2, USB device → adb reverse
proxypilot setup android --ca system        # system CA: emulator without Google Play, or rooted (until reboot)
proxypilot setup android --remove
```

USB devices use `adb reverse`, so traffic is captured even with Wi-Fi off. With `--ca user` (default) the certificate is copied to Downloads and you install it under Settings → Security → Encryption & credentials; apps then trust it only in debug builds with a `network_security_config` (the Setup → Android page has the snippet, plus a Flutter `HttpOverrides` example). adb is found via `ANDROID_HOME`, `~/Library/Android/sdk`, `PATH`, or `android.adb` in config.yaml.

## iPhone and Android phones

Phones on the same Wi-Fi reach the proxy only when it listens on all interfaces:

```bash
proxypilot stop && proxypilot start --detach --proxy-listen 0.0.0.0:9090
proxypilot setup device
```

`setup device` prints a QR code for the certificate page (`http://<this Mac's IP>:9090/`) and the Wi-Fi proxy steps. Once the phone's proxy is set, `http://proxypilot.cert` opens the same page. It offers a `.mobileconfig` profile on iOS and a `.crt` on Android, and shows live whether the phone reaches the proxy and whether HTTPS decryption works. The **Setup → Physical device** page shows the same QR code and lists connected devices, which you can rename.

The control API stays on this Mac; only the proxy is reachable from the network. If the macOS firewall is on, allow incoming connections for ProxyPilot.

## SSL pinning and hosts that must not be decrypted

Apple services are tunneled untouched by default. When a client keeps rejecting the certificate (`client_tls_rejected`), add its host to the bypass list from the error panel, or:

```bash
curl -X PUT -H "Authorization: Bearer $(cat '{dataDir}/token')" \
  -d '{"mode":"all","denylist":["pinning.bank.com"]}' http://{api}/api/v1/ssl-policy
```
