package macproxy

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// fakeNS simulates networksetup state for a few services.
type fakeNS struct {
	services []string
	web, sec map[string]Proxy
	bypass   map[string][]string
	denyAll  bool
}

func newFake() *fakeNS {
	return &fakeNS{
		services: []string{"Wi-Fi", "Thunderbolt Bridge"},
		web:      map[string]Proxy{"Wi-Fi": {Enabled: true, Server: "corp-proxy", Port: 3128}, "Thunderbolt Bridge": {}},
		sec:      map[string]Proxy{"Wi-Fi": {Server: "corp-proxy", Port: 3129}, "Thunderbolt Bridge": {}},
		bypass:   map[string][]string{"Wi-Fi": {"*.corp", "10.0.0.0/8"}, "Thunderbolt Bridge": {}},
	}
}

func show(p Proxy) string {
	en := "No"
	if p.Enabled {
		en = "Yes"
	}
	return fmt.Sprintf("Enabled: %s\nServer: %s\nPort: %d\nAuthenticated Proxy Enabled: 0\n", en, p.Server, p.Port)
}

func (f *fakeNS) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name != "networksetup" {
		return nil, fmt.Errorf("unexpected %s", name)
	}
	switch args[0] {
	case "-listallnetworkservices":
		return []byte("An asterisk (*) denotes that a network service is disabled.\n" + strings.Join(f.services, "\n") + "\n*Disabled VPN\n"), nil
	case "-getwebproxy":
		return []byte(show(f.web[args[1]])), nil
	case "-getsecurewebproxy":
		return []byte(show(f.sec[args[1]])), nil
	case "-getproxybypassdomains":
		if len(f.bypass[args[1]]) == 0 {
			return []byte("There aren't any bypass domains set on " + args[1] + ".\n"), nil
		}
		return []byte(strings.Join(f.bypass[args[1]], "\n") + "\n"), nil
	}
	if f.denyAll {
		return []byte("** Error: Command requires admin privileges."), errors.New("exit status 14")
	}
	svc := args[1]
	switch args[0] {
	case "-setwebproxy", "-setsecurewebproxy":
		port, _ := strconv.Atoi(args[3])
		p := Proxy{Enabled: true, Server: args[2], Port: port}
		if args[0] == "-setwebproxy" {
			f.web[svc] = p
		} else {
			f.sec[svc] = p
		}
	case "-setwebproxystate", "-setsecurewebproxystate":
		m := f.web
		if args[0] == "-setsecurewebproxystate" {
			m = f.sec
		}
		p := m[svc]
		p.Enabled = args[2] == "on"
		m[svc] = p
	case "-setproxybypassdomains":
		if len(args) == 3 && args[2] == "Empty" {
			f.bypass[svc] = []string{}
		} else {
			f.bypass[svc] = append([]string{}, args[2:]...)
		}
	default:
		return nil, fmt.Errorf("unexpected %v", args)
	}
	return nil, nil
}

// snapshot is the effective configuration. networksetup cannot clear a server
// field, so a service that had no server keeps ProxyPilot's address, disabled.
func snapshot(f *fakeNS) string {
	eff := func(m map[string]Proxy) map[string]Proxy {
		out := map[string]Proxy{}
		for k, p := range m {
			if p.Server == "127.0.0.1" && !p.Enabled {
				p = Proxy{}
			}
			out[k] = p
		}
		return out
	}
	return fmt.Sprint(eff(f.web), eff(f.sec), f.bypass)
}

func TestOnOffRestoresExactly(t *testing.T) {
	f := newFake()
	m := &Manager{Run: f, DataDir: t.TempDir(), GOOS: "darwin"}
	before := snapshot(f)
	ctx := context.Background()

	st, err := m.On(ctx, "127.0.0.1:9090", "127.0.0.1:9091")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Enabled || !st.Managed || len(st.Services) != 2 {
		t.Fatalf("after On: %+v", st)
	}
	if got := f.bypass["Wi-Fi"]; !reflect.DeepEqual(got, append(append([]string{}, DefaultBypass...), "127.0.0.1:9091")) {
		t.Fatalf("bypass = %v", got)
	}
	// A second On keeps the original backup (not the ProxyPilot settings).
	if _, err := m.On(ctx, "127.0.0.1:9090"); err != nil {
		t.Fatal(err)
	}
	if left, _ := m.LeftOn(ctx); !left {
		t.Fatal("LeftOn should report the active managed proxy")
	}
	if err := m.Off(ctx); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(f); after != before {
		t.Fatalf("Off did not restore:\nbefore %s\nafter  %s", before, after)
	}
	if err := m.Off(ctx); !errors.Is(err, ErrNoBackup) {
		t.Fatalf("second Off: %v", err)
	}
}

func TestNeedsAdminAndNotMac(t *testing.T) {
	f := newFake()
	f.denyAll = true
	m := &Manager{Run: f, DataDir: t.TempDir(), GOOS: "darwin"}
	if _, err := m.On(context.Background(), "127.0.0.1:9090"); !errors.Is(err, ErrNeedsAdmin) {
		t.Fatalf("want ErrNeedsAdmin, got %v", err)
	}
	cmds := m.Commands([]string{"Wi-Fi"}, "0.0.0.0:9090")
	if strings.Join(cmds[0], " ") != "networksetup -setwebproxy Wi-Fi 127.0.0.1 9090" {
		t.Fatalf("commands: %v", cmds[0])
	}
	if _, err := (&Manager{Run: f, GOOS: "linux"}).Services(context.Background()); !errors.Is(err, ErrNotMac) {
		t.Fatalf("linux: %v", err)
	}
}

func TestServicesSkipsDisabled(t *testing.T) {
	m := &Manager{Run: newFake(), GOOS: "darwin"}
	names, err := m.Services(context.Background())
	if err != nil || !reflect.DeepEqual(names, []string{"Wi-Fi", "Thunderbolt Bridge"}) {
		t.Fatalf("%v %v", names, err)
	}
}
