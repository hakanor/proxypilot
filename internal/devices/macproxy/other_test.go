package macproxy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeKV acts as both `reg` (Windows registry values) and `gsettings` (GNOME keys).
type fakeKV map[string]string

func (f fakeKV) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	switch {
	case name == "reg" && args[0] == "query":
		v, ok := f[args[3]]
		if !ok {
			return nil, fmt.Errorf("not found")
		}
		typ := "REG_SZ"
		if args[3] == "ProxyEnable" {
			typ = "REG_DWORD"
		}
		return []byte("\r\n" + winKey + "\r\n    " + args[3] + "    " + typ + "    " + v + "\r\n"), nil
	case name == "reg" && args[0] == "add":
		v := args[7]
		if args[3] == "ProxyEnable" {
			v = "0x" + v
		}
		f[args[3]] = v
	case name == "reg" && args[0] == "delete":
		delete(f, args[3])
	case name == "gsettings" && args[0] == "get":
		return []byte(f[args[1]+" "+args[2]] + "\n"), nil
	case name == "gsettings" && args[0] == "set":
		f[args[1]+" "+args[2]] = args[3]
	default:
		return nil, fmt.Errorf("unexpected %s %v", name, args)
	}
	return nil, nil
}

func roundTrip(t *testing.T, goos string, kv fakeKV) {
	before := fmt.Sprint(kv)
	m := &Manager{Run: kv, DataDir: t.TempDir(), GOOS: goos}
	st, err := m.On(context.Background(), "127.0.0.1:9090", "*.corp")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Enabled || len(st.Services) != 1 {
		t.Fatalf("%s: on: %+v", goos, st)
	}
	if !strings.Contains(fmt.Sprint(st.Services[0].Bypass), "*.corp") {
		t.Fatalf("%s: bypass %v", goos, st.Services[0].Bypass)
	}
	if err := m.Off(context.Background()); err != nil {
		t.Fatal(err)
	}
	if after := fmt.Sprint(kv); after != before && goos == "windows" {
		t.Fatalf("windows: restore\n got %s\nwant %s", after, before)
	}
	if st, _ := m.Status(context.Background(), "127.0.0.1:9090"); st.Enabled {
		t.Fatalf("%s: still on after off", goos)
	}
}

func TestWindowsProxy(t *testing.T) {
	roundTrip(t, "windows", fakeKV{"ProxyEnable": "0x0"})
	roundTrip(t, "windows", fakeKV{"ProxyEnable": "0x1", "ProxyServer": "http=corp:3128;https=corp:3129", "ProxyOverride": "*.corp;<local>"})
	if w, s := parseWinServer("corp:8080"); w != s || w.Server != "corp" || w.Port != 8080 {
		t.Fatalf("single server: %+v %+v", w, s)
	}
}

func TestGnomeProxy(t *testing.T) {
	kv := fakeKV{
		gnomeSchema + " mode": "'auto'", gnomeSchema + " ignore-hosts": "['localhost', '10.0.0.0/8']",
		gnomeSchema + ".http host": "''", gnomeSchema + ".http port": "0",
		gnomeSchema + ".https host": "''", gnomeSchema + ".https port": "0",
	}
	roundTrip(t, "linux", kv)
	m := &Manager{Run: kv, GOOS: "linux"}
	s, _ := m.gnomeRead(context.Background())
	if s.Mode != "auto" || !reflect.DeepEqual(s.Bypass, []string{"localhost", "10.0.0.0/8"}) {
		t.Fatalf("restored %+v", s)
	}
}

func TestBootHook(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		dir := t.TempDir()
		var m *Manager
		if goos == "darwin" {
			m = &Manager{Run: newFake(), DataDir: t.TempDir(), GOOS: goos, BootDir: dir}
		} else {
			m = &Manager{Run: fakeKV{gnomeSchema + " mode": "'none'"}, DataDir: t.TempDir(), GOOS: goos, BootDir: dir}
		}
		if _, err := m.On(context.Background(), "127.0.0.1:9090"); err != nil {
			t.Fatal(goos, err)
		}
		files, _ := filepath.Glob(filepath.Join(dir, bootName+".*"))
		if len(files) != 1 {
			t.Fatalf("%s: hook not written: %v", goos, files)
		}
		if goos == "darwin" {
			if out, err := exec.Command("plutil", "-lint", files[0]).CombinedOutput(); err != nil && !errors.Is(err, exec.ErrNotFound) {
				t.Fatalf("plist: %v %s", err, out)
			}
		}
		b, _ := os.ReadFile(files[0])
		if !strings.Contains(string(b), "system-proxy") || !strings.Contains(string(b), m.DataDir) {
			t.Fatalf("%s: hook %s", goos, b)
		}
		if err := m.Off(context.Background()); err != nil {
			t.Fatal(goos, err)
		}
		if files, _ := filepath.Glob(filepath.Join(dir, "*")); len(files) != 0 {
			t.Fatalf("%s: hook left after off: %v", goos, files)
		}
	}
}
