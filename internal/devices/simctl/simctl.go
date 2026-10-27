// Package simctl lists iOS simulators and installs or removes the ProxyPilot root CA.
package simctl

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/hakanor/proxypilot/internal/devices/execx"
)

var ErrNoXcode = errors.New("Xcode command line tools not found: install Xcode, open it once, then run `xcode-select -s /Applications/Xcode.app`")

// CA trust state inside a simulator.
const (
	CAInstalled = "installed"
	CAMissing   = "missing"
	CAUnknown   = "unknown" // never booted: no trust store yet
)

type Device struct {
	UDID      string `json:"udid"`
	Name      string `json:"name"`
	Runtime   string `json:"runtime"` // "iOS 26.3"
	State     string `json:"state"`   // Booted, Shutdown…
	Booted    bool   `json:"booted"`
	Available bool   `json:"available"`
	CA        string `json:"ca"`
	dataPath  string
}

type Simctl struct {
	Run execx.Runner
	// CASHA256 is the root certificate's SHA-256 (DER), used to check trust stores.
	CASHA256 []byte
}

func New(caSHA256 []byte) *Simctl { return &Simctl{Run: execx.OS{}, CASHA256: caSHA256} }

func (s *Simctl) xcrun(ctx context.Context, args ...string) ([]byte, error) {
	out, err := s.Run.Run(ctx, "xcrun", append([]string{"simctl"}, args...)...)
	if err != nil {
		var ee *execx.Error
		if errors.Is(err, os.ErrNotExist) || (errors.As(err, &ee) && (strings.Contains(ee.Output, "unable to find utility") || strings.Contains(ee.Output, "xcode-select"))) {
			return nil, ErrNoXcode
		}
		return nil, err
	}
	return out, nil
}

// runtimeName turns "com.apple.CoreSimulator.SimRuntime.iOS-26-3" into "iOS 26.3".
func runtimeName(id string) string {
	id = id[strings.LastIndex(id, ".")+1:]
	os, ver, ok := strings.Cut(id, "-")
	if !ok {
		return id
	}
	return os + " " + strings.ReplaceAll(ver, "-", ".")
}

// List returns available simulators, booted first.
func (s *Simctl) List(ctx context.Context) ([]Device, error) {
	out, err := s.xcrun(ctx, "list", "devices", "--json")
	if err != nil {
		return nil, err
	}
	var raw struct {
		Devices map[string][]struct {
			UDID        string `json:"udid"`
			Name        string `json:"name"`
			State       string `json:"state"`
			IsAvailable bool   `json:"isAvailable"`
			DataPath    string `json:"dataPath"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse simctl list: %w", err)
	}
	var list []Device
	for rt, devs := range raw.Devices {
		for _, d := range devs {
			if !d.IsAvailable {
				continue
			}
			dev := Device{UDID: d.UDID, Name: d.Name, Runtime: runtimeName(rt), State: d.State, Booted: d.State == "Booted", Available: true, dataPath: d.DataPath}
			dev.CA = s.caState(dev)
			list = append(list, dev)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Booted != list[j].Booted {
			return list[i].Booted
		}
		if list[i].Runtime != list[j].Runtime {
			return list[i].Runtime > list[j].Runtime
		}
		return list[i].Name < list[j].Name
	})
	return list, nil
}

func trustStore(dataPath string) string {
	return filepath.Join(dataPath, "private/var/protected/trustd/private/TrustStore.sqlite3")
}

// caState reads the simulator's trust store (read-only) for our certificate.
func (s *Simctl) caState(d Device) string {
	if d.dataPath == "" || len(s.CASHA256) == 0 {
		return CAUnknown
	}
	path := trustStore(d.dataPath)
	if _, err := os.Stat(path); err != nil {
		return CAUnknown
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return CAUnknown
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tsettings WHERE sha256 = ?`, s.CASHA256).Scan(&n); err != nil {
		return CAUnknown
	}
	if n > 0 {
		return CAInstalled
	}
	return CAMissing
}

// InstallCA adds certPath as a trusted root to a booted simulator.
func (s *Simctl) InstallCA(ctx context.Context, udid, certPath string) error {
	_, err := s.xcrun(ctx, "keychain", udid, "add-root-cert", certPath)
	return err
}

// InstallCommand is what InstallCA runs (shown in docs and errors).
func InstallCommand(udid, certPath string) string {
	return execx.Quote("xcrun", "simctl", "keychain", udid, "add-root-cert", certPath)
}

// RemoveCA deletes only the ProxyPilot root from a simulator's trust store
// (simctl has no remove command; `keychain reset` would wipe every certificate).
// The simulator must be restarted for running apps to notice.
func (s *Simctl) RemoveCA(ctx context.Context, d Device) (bool, error) {
	if d.dataPath == "" || len(s.CASHA256) == 0 {
		return false, nil
	}
	path := trustStore(d.dataPath)
	if _, err := os.Stat(path); err != nil {
		return false, nil
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return false, err
	}
	defer db.Close()
	res, err := db.ExecContext(ctx, `DELETE FROM tsettings WHERE sha256 = ?`, s.CASHA256)
	if err != nil {
		return false, fmt.Errorf("%s: %w", d.Name, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Find returns the device with udid.
func (s *Simctl) Find(ctx context.Context, udid string) (Device, error) {
	list, err := s.List(ctx)
	if err != nil {
		return Device{}, err
	}
	for _, d := range list {
		if strings.EqualFold(d.UDID, udid) {
			return d, nil
		}
	}
	return Device{}, fmt.Errorf("simulator %s not found", udid)
}
