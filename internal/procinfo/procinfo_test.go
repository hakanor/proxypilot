package procinfo

import (
	"context"
	"runtime"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		path, app, udid string
		sim             bool
	}{
		{"/Users/x/Library/Developer/CoreSimulator/Devices/5EF9573F-3A8E-467B-A381-AEBC245FEDEE/data/Containers/Bundle/Application/3E28/Starlight.app/Starlight", "Starlight", "5EF9573F-3A8E-467B-A381-AEBC245FEDEE", true},
		{"/Library/Developer/CoreSimulator/Volumes/iOS_23A/Library/Developer/CoreSimulator/Profiles/Runtimes/iOS 26.3.simruntime/Contents/Resources/RuntimeRoot/Applications/MobileSafari.app/MobileSafari", "Safari", "", true},
		{"/Applications/Google Chrome.app/Contents/Frameworks/Google Chrome Framework.framework/Versions/141/Helpers/Google Chrome Helper.app/Contents/MacOS/Google Chrome Helper", "Google Chrome", "", false},
		{"/Applications/Slack.app/Contents/MacOS/Slack", "Slack", "", false},
		{"/usr/bin/curl", "curl", "", false},
	}
	for _, c := range cases {
		o := Classify(c.path)
		if o.App != c.app || o.SimulatorUDID != c.udid || o.Simulator != c.sim {
			t.Errorf("%s: %+v", c.path, o)
		}
	}
}

func TestLookupCachesAndSkipsRemote(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	r := New()
	calls := 0
	r.lookup = func(port int) (Info, bool) { calls++; return Info{PID: 42, Path: "/usr/bin/curl"}, true }
	for i := 0; i < 3; i++ {
		if info, ok := r.Lookup("127.0.0.1:5000"); !ok || info.PID != 42 {
			t.Fatal(info, ok)
		}
	}
	if calls != 1 {
		t.Fatalf("lookup ran %d times, want 1 (cached)", calls)
	}
	if _, ok := r.Lookup("192.168.1.20:5000"); ok {
		t.Fatal("remote clients have no local process")
	}
}

// Only the connection whose local end is the client port counts.
func TestPickClientSide(t *testing.T) {
	r := New()
	out := "p100\nn10.0.0.5:51000->1.2.3.4:63588\np200\nn127.0.0.1:63588->127.0.0.1:9090\np300\nn127.0.0.1:9090->127.0.0.1:63588\n"
	r.self = 300
	r.path = func(context.Context, int) (string, error) { return "/usr/bin/curl", nil }
	got, ok := r.pick(context.Background(), out, 63588)
	if !ok || got.PID != 200 {
		t.Fatalf("picked %+v %v, want pid 200 (the client end)", got, ok)
	}
}
