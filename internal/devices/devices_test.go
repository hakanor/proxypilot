package devices

import (
	"context"
	"strings"
	"testing"

	"github.com/hakanor/proxypilot/internal/ca"
	"github.com/hakanor/proxypilot/internal/devices/macproxy"
	"github.com/hakanor/proxypilot/internal/devices/simctl"
)

type fake struct{ calls []string }

func (f *fake) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	switch {
	case name == "xcrun" && args[1] == "list":
		return []byte(`{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-26-3":[
		  {"udid":"AAA","name":"iPhone A","state":"Booted","isAvailable":true},
		  {"udid":"BBB","name":"iPhone B","state":"Shutdown","isAvailable":true}]}}`), nil
	case name == "networksetup" && args[0] == "-listallnetworkservices":
		return []byte("An asterisk (*) denotes that a network service is disabled.\nWi-Fi\n"), nil
	case name == "networksetup" && strings.HasPrefix(args[0], "-get"):
		return []byte("Enabled: No\nServer: \nPort: 0\n"), nil
	}
	return nil, nil
}

func newService(t *testing.T) (*Service, *fake) {
	root, err := ca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fake{}
	dir := t.TempDir()
	return &Service{
		CA:      root,
		Sim:     &simctl.Simctl{Run: f, CASHA256: root.SHA256()},
		Proxy:   &macproxy.Manager{Run: f, DataDir: dir, GOOS: "darwin"},
		DataDir: dir,
	}, f
}

func TestInstallCATargets(t *testing.T) {
	s, f := newService(t)
	res, err := s.InstallCA(context.Background())
	if err != nil || len(res) != 1 || res[0].UDID != "AAA" || !res[0].OK {
		t.Fatalf("all booted: %+v %v", res, err)
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "xcrun simctl keychain AAA add-root-cert "+s.CA.CertPath()) {
		t.Fatalf("calls: %v", f.calls)
	}
	res, _ = s.InstallCA(context.Background(), "bbb", "nope")
	if len(res) != 2 || res[0].OK || !strings.Contains(res[0].Error, "not booted") || res[1].Error != "simulator not found" {
		t.Fatalf("explicit: %+v", res)
	}
}

func TestPlanAndClean(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	if _, err := s.Proxy.On(ctx, "127.0.0.1:9090"); err != nil {
		t.Fatal(err)
	}
	plan := s.Plan(ctx)
	if plan[0].ID != "system_proxy" || plan[0].Status != "pending" || len(plan[0].Commands) == 0 {
		t.Fatalf("plan: %+v", plan[0])
	}
	res := s.Clean(ctx)
	if !res[0].OK || len(res[0].Done) != 1 {
		t.Fatalf("clean: %+v", res)
	}
	if p := s.Plan(ctx); p[0].Status != "clean" {
		t.Fatalf("after clean: %+v", p[0])
	}
}
