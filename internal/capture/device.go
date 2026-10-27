package capture

import (
	"net"
	"strings"
)

// DeviceApp groups a flow for the sidebar: which device sent it and which app on
// that device. custom is the user's name for the client IP ("" if none).
//
// Simulators and emulators reach the proxy from this Mac's loopback, so the
// User-Agent decides between "This Mac", "iOS Simulator" and "Android".
func DeviceApp(clientAddr, ua, custom string) (device, app string) {
	app = AppName(ua)
	host, _, err := net.SplitHostPort(clientAddr)
	if err != nil {
		host = clientAddr
	}
	l := strings.ToLower(ua)
	mobile := ""
	switch {
	case strings.Contains(l, "iphone"), strings.Contains(l, "ipad"), strings.Contains(l, "ios "), strings.Contains(l, "cfnetwork") && strings.Contains(l, "iphone"):
		mobile = "iOS"
	case strings.Contains(l, "android"), strings.Contains(l, "dalvik"), strings.HasPrefix(l, "okhttp"):
		mobile = "Android"
	}
	if custom != "" {
		return custom, app
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		switch mobile {
		case "iOS":
			return "iOS Simulator", app
		case "Android":
			return "Android (emulator / USB)", app
		}
		return "This Mac", app
	}
	if ip == nil {
		return host, app
	}
	switch mobile {
	case "iOS":
		return "iPhone · " + host, app
	case "Android":
		return "Android · " + host, app
	}
	return host, app
}

// AppName guesses the app from a User-Agent: "Invio/4.2 CFNetwork/1498 Darwin/24"
// → Invio, browsers by name, HTTP libraries by name.
func AppName(ua string) string {
	l := strings.ToLower(ua)
	switch {
	case ua == "":
		return "Unknown"
	case strings.Contains(l, "edg/"):
		return "Edge"
	case strings.Contains(l, "chrome/"), strings.Contains(l, "crios/"):
		return "Chrome"
	case strings.Contains(l, "firefox/"), strings.Contains(l, "fxios/"):
		return "Firefox"
	case strings.HasPrefix(l, "mozilla/") && strings.Contains(l, "safari/"):
		return "Safari"
	case strings.HasPrefix(l, "mozilla/") && (strings.Contains(l, "android") || strings.Contains(l, "wv)")):
		return "WebView"
	case strings.HasPrefix(l, "okhttp/"):
		return "OkHttp app"
	case strings.HasPrefix(l, "dalvik/"):
		return "Android app"
	case strings.HasPrefix(l, "go-http-client"):
		return "Go client"
	case strings.HasPrefix(l, "python-requests"), strings.HasPrefix(l, "python-urllib"), strings.HasPrefix(l, "python-httpx"):
		return "Python"
	case strings.HasPrefix(l, "node"), strings.HasPrefix(l, "axios"), strings.HasPrefix(l, "undici"):
		return "Node.js"
	}
	// "AppName/1.2.3 …": the first product token, with %20 and _ undone.
	name := ua
	if i := strings.IndexAny(name, "/ ("); i > 0 {
		name = name[:i]
	}
	name = strings.NewReplacer("%20", " ", "_", " ").Replace(name)
	if strings.EqualFold(name, "mozilla") || name == "" {
		return "Browser"
	}
	return name
}
