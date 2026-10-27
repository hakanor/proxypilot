package capture

import "testing"

func TestDeviceApp(t *testing.T) {
	cases := []struct{ addr, ua, custom, dev, app string }{
		{"127.0.0.1:5000", "Invio/4.2.1 CFNetwork/1498.700.2 Darwin/24.0.0", "", "This Mac", "Invio"},
		{"127.0.0.1:5000", "Invio/4.2 (iPhone; iOS 18.1; Scale/3.00)", "", "iOS Simulator", "Invio"},
		{"127.0.0.1:5000", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 Version/18.1 Mobile/15E148 Safari/604.1", "", "iOS Simulator", "Safari"},
		{"127.0.0.1:5000", "okhttp/4.12.0", "", "Android (emulator / USB)", "OkHttp app"},
		{"127.0.0.1:5000", "curl/8.7.1", "", "This Mac", "curl"},
		{"127.0.0.1:5000", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/141.0.0.0 Safari/537.36", "", "This Mac", "Chrome"},
		{"192.168.1.20:5000", "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Chrome/141 Mobile Safari/537.36", "", "Android · 192.168.1.20", "Chrome"},
		{"192.168.1.21:5000", "Spotify/9.0 iOS/18.1 (iPhone16,1)", "Hakan's iPhone", "Hakan's iPhone", "Spotify"},
		{"127.0.0.1:5000", "", "", "This Mac", "Unknown"},
		{"127.0.0.1:5000", "My%20App/1.0 CFNetwork", "", "This Mac", "My App"},
	}
	for _, c := range cases {
		d, a := DeviceApp(c.addr, c.ua, c.custom)
		if d != c.dev || a != c.app {
			t.Errorf("%q: got %q / %q, want %q / %q", c.ua, d, a, c.dev, c.app)
		}
	}
}
