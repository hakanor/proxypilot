// Package devices groups device setup (iOS simulators, macOS system proxy) and
// the matching cleanup, shared by the API (web UI) and the CLI.
package devices

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/hakanor/proxypilot/internal/ca"
	"github.com/hakanor/proxypilot/internal/devices/android"
	"github.com/hakanor/proxypilot/internal/devices/execx"
	"github.com/hakanor/proxypilot/internal/devices/macproxy"
	"github.com/hakanor/proxypilot/internal/devices/simctl"
)

type Service struct {
	CA      *ca.RootCA
	Sim     *simctl.Simctl
	Proxy   *macproxy.Manager
	DataDir string
	// ProxyPort recognizes Android devices pointed at us (uninstall plan).
	ProxyPort string
	// ADBPath is the configured adb ("" = search); Android returns ErrNoADB if none is found.
	ADBPath string
	// NewADB is overridable for tests.
	NewADB func(path string) (*android.ADB, error)
}

// Android returns an adb client (looked up on every call so installing the SDK later works).
func (s *Service) Android() (*android.ADB, error) {
	if s.NewADB != nil {
		return s.NewADB(s.ADBPath)
	}
	return android.New(s.ADBPath)
}

// AndroidOptions fills in the CA for an Android setup.
func (s *Service) AndroidOptions(o android.SetupOptions) android.SetupOptions {
	o.CertPEM, o.CertDER, o.Cert = s.CA.ExportPEM(), s.CA.ExportDER(), s.CA.Certificate()
	return o
}

// AndroidHash is the system CA file name (without .0).
func (s *Service) AndroidHash() string { return android.SubjectHashOld(s.CA.Certificate()) }

func New(root *ca.RootCA, dataDir string) *Service {
	return &Service{CA: root, Sim: simctl.New(root.SHA256()), Proxy: macproxy.New(dataDir), DataDir: dataDir}
}

// InstallResult is one simulator's outcome.
type InstallResult struct {
	UDID  string `json:"udid"`
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// InstallCA installs the root CA into the given simulators, or every booted one when udids is empty.
func (s *Service) InstallCA(ctx context.Context, udids ...string) ([]InstallResult, error) {
	list, err := s.Sim.List(ctx)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, u := range udids {
		want[strings.ToUpper(u)] = true
	}
	var out []InstallResult
	for _, d := range list {
		if len(want) == 0 && !d.Booted || len(want) > 0 && !want[strings.ToUpper(d.UDID)] {
			continue
		}
		r := InstallResult{UDID: d.UDID, Name: d.Name}
		if !d.Booted {
			r.Error = "simulator is not booted (boot it first: xcrun simctl boot " + d.UDID + ")"
		} else if err := s.Sim.InstallCA(ctx, d.UDID, s.CA.CertPath()); err != nil {
			r.Error = err.Error()
		} else {
			r.OK = true
		}
		delete(want, strings.ToUpper(d.UDID))
		out = append(out, r)
	}
	for u := range want {
		out = append(out, InstallResult{UDID: u, Error: "simulator not found"})
	}
	return out, nil
}

// Step is one item of the uninstall plan.
type Step struct {
	ID     string `json:"id"`
	Status string `json:"status"` // "pending" (something to remove), "clean", "manual", "unavailable"
	// Auto: ProxyPilot can do it without admin rights (`uninstall --yes` / the UI button).
	Auto     bool     `json:"auto"`
	Details  []string `json:"details,omitempty"`
	Commands []string `json:"commands,omitempty"` // for the user to run (sudo etc.)
}

// Plan inspects what ProxyPilot changed on this machine.
func (s *Service) Plan(ctx context.Context) []Step {
	var steps []Step

	proxy := Step{ID: "system_proxy", Status: "clean", Auto: true}
	if st, err := s.Proxy.Status(ctx, ""); errors.Is(err, macproxy.ErrNotMac) {
		proxy.Status, proxy.Auto = "unavailable", false
	} else if err == nil && st.Managed {
		proxy.Status = "pending"
		if cmds, err := s.Proxy.RestoreCommands(); err == nil {
			for _, c := range cmds {
				proxy.Commands = append(proxy.Commands, execx.Quote(c[0], c[1:]...))
			}
		}
	}
	steps = append(steps, proxy)

	sims := Step{ID: "simulators", Status: "clean", Auto: true}
	if list, err := s.Sim.List(ctx); err != nil {
		sims.Status, sims.Auto, sims.Details = "unavailable", false, []string{err.Error()}
	} else {
		for _, d := range list {
			if d.CA == simctl.CAInstalled {
				sims.Status = "pending"
				sims.Details = append(sims.Details, d.Name+" ("+d.Runtime+") "+d.UDID)
			}
		}
	}
	steps = append(steps, sims)

	if a, err := s.Android(); err == nil {
		droid := Step{ID: "android", Status: "clean", Auto: true}
		if list, err := a.Devices(ctx, s.ProxyPort, ""); err == nil {
			for _, d := range list {
				if d.Ours {
					droid.Status = "pending"
					droid.Details = append(droid.Details, d.Serial+" ("+d.Model+") → "+d.Proxy)
				}
			}
		}
		steps = append(steps, droid)
	}

	trust := Step{ID: "mac_trust", Status: "clean"}
	if st := s.CA.TrustStatus(); st == "trusted" {
		trust.Status = "pending"
		for _, c := range s.CA.UntrustCommands() {
			trust.Commands = append(trust.Commands, "sudo "+execx.Quote(c[0], c[1:]...))
		}
	}
	steps = append(steps, trust)

	steps = append(steps,
		Step{ID: "devices", Status: "manual"},
		Step{ID: "data", Status: "manual", Details: []string{s.DataDir}, Commands: []string{"proxypilot uninstall --yes --purge"}},
	)
	return steps
}

// Result of an automatic cleanup.
type Result struct {
	ID    string   `json:"id"`
	OK    bool     `json:"ok"`
	Done  []string `json:"done,omitempty"`
	Error string   `json:"error,omitempty"`
}

// Clean restores the system proxy and removes the CA from every simulator.
// Steps that need administrator rights are left to the user (see Plan).
func (s *Service) Clean(ctx context.Context) []Result {
	var out []Result
	r := Result{ID: "system_proxy", OK: true}
	if err := s.Proxy.Off(ctx); err == nil {
		r.Done = []string{"restored previous network proxy settings"}
	} else if !errors.Is(err, macproxy.ErrNoBackup) && !errors.Is(err, macproxy.ErrNotMac) {
		r.OK, r.Error = false, err.Error()
	}
	out = append(out, r)

	r = Result{ID: "simulators", OK: true}
	if list, err := s.Sim.List(ctx); err == nil {
		for _, d := range list {
			if d.CA != simctl.CAInstalled {
				continue
			}
			if ok, err := s.Sim.RemoveCA(ctx, d); err != nil {
				r.OK, r.Error = false, err.Error()
			} else if ok {
				r.Done = append(r.Done, d.Name+" ("+d.Runtime+")")
			}
		}
	} else if !errors.Is(err, simctl.ErrNoXcode) {
		r.OK, r.Error = false, err.Error()
	}
	out = append(out, r)

	if a, err := s.Android(); err == nil {
		r = Result{ID: "android", OK: true}
		if list, err := a.Devices(ctx, s.ProxyPort, ""); err == nil {
			for _, d := range list {
				if !d.Ours {
					continue
				}
				if _, err := a.Remove(ctx, d.Serial, s.ProxyPort); err != nil {
					r.OK, r.Error = false, err.Error()
				} else {
					r.Done = append(r.Done, d.Serial+": proxy cleared")
				}
			}
		}
		out = append(out, r)
	}
	return out
}

// Purge deletes the data directory (CA key, flows, token). The daemon must be stopped.
func (s *Service) Purge() error {
	return os.RemoveAll(s.DataDir)
}
