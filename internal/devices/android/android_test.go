package android

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hakanor/proxypilot/internal/ca"
)

type fakeADB struct {
	devices string
	api     string
	root    bool
	calls   []string
}

func (f *fakeADB) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	if len(args) > 1 && args[0] == "-s" {
		args = args[2:]
	}
	switch {
	case args[0] == "devices":
		return []byte(f.devices), nil
	case args[0] == "root":
		if !f.root {
			return []byte("adbd cannot run as root in production builds"), nil
		}
		return []byte("restarting adbd as root"), nil
	case args[0] == "shell":
		switch cmd := args[1]; {
		case cmd == "getprop ro.build.version.sdk":
			return []byte(f.api), nil
		case cmd == "getprop ro.kernel.qemu":
			return []byte("1"), nil
		case cmd == "settings get global http_proxy":
			return []byte("null"), nil
		case cmd == "id -u":
			if f.root {
				return []byte("0"), nil
			}
			return []byte("2000"), nil
		}
	}
	return nil, nil
}

func (f *fakeADB) did(s string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, s) {
			return true
		}
	}
	return false
}

func testCA(t *testing.T) *ca.RootCA {
	r, err := ca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func opts(r *ca.RootCA) SetupOptions {
	return SetupOptions{Port: "9090", CertPEM: r.ExportPEM(), CertDER: r.ExportDER(), Cert: r.Certificate()}
}

const twoDevices = "List of devices attached\nemulator-5554          device product:sdk_gphone64_arm64 model:sdk_gphone64_arm64 device:emu64a transport_id:1\nR58M123ABC             unauthorized usb:1-1 transport_id:2\n"

func TestDevicesAndPick(t *testing.T) {
	f := &fakeADB{devices: twoDevices, api: "34"}
	a := &ADB{Run: f, Path: "adb"}
	list, err := a.Devices(context.Background(), "9090", "")
	if err != nil || len(list) != 2 || !list[0].Emulator || list[0].API != 34 || list[0].Model != "sdk gphone64 arm64" || list[1].State != "unauthorized" {
		t.Fatalf("%+v %v", list, err)
	}
	if d, err := Pick(list, ""); err != nil || d.Serial != "emulator-5554" {
		t.Fatalf("pick only ready: %+v %v", d, err)
	}
	if _, err := Pick(list, "R58M123ABC"); err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("unauthorized: %v", err)
	}
	if _, err := Pick(nil, ""); !errors.Is(err, ErrNoDevice) {
		t.Fatalf("none: %v", err)
	}
}

func TestEmulatorSetupWithUserCA(t *testing.T) {
	f := &fakeADB{devices: twoDevices, api: "33"}
	a := &ADB{Run: f, Path: "adb"}
	r, err := a.Setup(context.Background(), opts(testCA(t)))
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode != ModeEmu || r.Proxy != "10.0.2.2:9090" || !f.did("settings put global http_proxy 10.0.2.2:9090") || !f.did("push") || !f.did(UserCAPath) {
		t.Fatalf("%+v\n%v", r, f.calls)
	}
	if f.did("reverse") {
		t.Fatal("emulator mode should not use adb reverse")
	}
}

func TestUSBAndWiFiModes(t *testing.T) {
	f := &fakeADB{devices: "List of devices attached\nR58M123ABC device usb:1-1 model:Pixel_8\n", api: "34"}
	a := &ADB{Run: f, Path: "adb"}
	o := opts(testCA(t))
	o.Mode, o.CA = ModeUSB, "none"
	if r, err := a.Setup(context.Background(), o); err != nil || r.Proxy != "127.0.0.1:9090" || !f.did("reverse tcp:9090 tcp:9090") {
		t.Fatalf("usb: %+v %v", r, err)
	}
	o.Mode = ModeWiFi
	if _, err := a.Setup(context.Background(), o); err == nil {
		t.Fatal("wifi without LAN IP should fail")
	}
	o.LANIP = "192.168.1.66"
	if r, err := a.Setup(context.Background(), o); err != nil || r.Proxy != "192.168.1.66:9090" {
		t.Fatalf("wifi: %+v %v", r, err)
	}
}

func TestSystemCA(t *testing.T) {
	r := testCA(t)
	o := opts(r)
	o.CA = "system"
	f := &fakeADB{devices: twoDevices, api: "34"}
	if _, err := (&ADB{Run: f, Path: "adb"}).Setup(context.Background(), o); !errors.Is(err, ErrNoRoot) {
		t.Fatalf("no root: %v", err)
	}
	if f.did("settings put global http_proxy") {
		t.Fatal("a failed CA install must not change the proxy")
	}
	f = &fakeADB{devices: twoDevices, api: "34", root: true}
	if _, err := (&ADB{Run: f, Path: "adb"}).Setup(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	hash := SubjectHashOld(r.Certificate())
	if !f.did("/data/local/tmp/"+hash+".0") || !f.did("mount -t tmpfs") || !f.did("nsenter") || !f.did("/apex/com.android.conscrypt/cacerts") {
		t.Fatalf("API 34 install script:\n%s", strings.Join(f.calls, "\n"))
	}
	f = &fakeADB{devices: twoDevices, api: "30", root: true}
	(&ADB{Run: f, Path: "adb"}).Setup(context.Background(), o)
	if f.did("nsenter") {
		t.Fatal("API 30 does not need the APEX bind mount")
	}
}

func TestRemove(t *testing.T) {
	f := &fakeADB{devices: twoDevices, api: "34"}
	r, err := (&ADB{Run: f, Path: "adb"}).Remove(context.Background(), "", "9090")
	if err != nil || !f.did("settings put global http_proxy :0") || !f.did("reverse --remove tcp:9090") || len(r.Manual) == 0 {
		t.Fatalf("%+v %v %v", r, err, f.calls)
	}
}

// SubjectHashOld must equal `openssl x509 -subject_hash_old` (Android's file names).
func TestSubjectHashMatchesOpenSSL(t *testing.T) {
	r := testCA(t)
	openssl, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("openssl not installed")
	}
	p := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(p, r.ExportPEM(), 0o600)
	out, err := exec.Command(openssl, "x509", "-in", p, "-noout", "-subject_hash_old").Output()
	if err != nil {
		t.Skip(err)
	}
	if got, want := SubjectHashOld(r.Certificate()), strings.TrimSpace(string(out)); got != want {
		t.Fatalf("hash %s, openssl %s", got, want)
	}
}

func TestFind(t *testing.T) {
	dir := t.TempDir()
	adb := filepath.Join(dir, "platform-tools", "adb")
	os.MkdirAll(filepath.Dir(adb), 0o755)
	os.WriteFile(adb, []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("ANDROID_HOME", dir)
	if p, err := Find(""); err != nil || p != adb {
		t.Fatalf("ANDROID_HOME: %s %v", p, err)
	}
	if p, _ := Find(adb); p != adb {
		t.Fatalf("configured: %s", p)
	}
}
