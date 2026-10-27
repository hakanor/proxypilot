// Package macproxy turns the macOS system HTTP/HTTPS proxy on and off with
// networksetup, keeping a backup so "off" restores the exact previous settings.
package macproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hakanor/proxypilot/internal/devices/execx"
)

const BackupFile = "system-proxy-backup.json"

// DefaultBypass is always added to the bypass list while ProxyPilot is the proxy.
var DefaultBypass = []string{"localhost", "127.0.0.1", "::1", "*.local", "169.254/16"}

var (
	ErrNotMac      = errors.New("system proxy control is only available on macOS")
	ErrNoBackup    = errors.New("system proxy is not managed by ProxyPilot (no backup found)")
	ErrNeedsAdmin  = errors.New("changing network settings requires an administrator")
	ErrNoServices  = errors.New("no enabled network services found")
	errNoBypassMsg = "There aren't any bypass domains"
)

// Proxy is one proxy kind (web or secure web) of a network service.
type Proxy struct {
	Enabled bool   `json:"enabled"`
	Server  string `json:"server"`
	Port    int    `json:"port"`
}

// Service is the proxy state of one network service ("Wi-Fi").
type Service struct {
	Name   string   `json:"name"`
	Web    Proxy    `json:"web"`
	Secure Proxy    `json:"secure"`
	Bypass []string `json:"bypass"`
}

// Status is the current state as seen by `system-proxy status`.
type Status struct {
	Services []Service `json:"services"`
	// Enabled: every service sends HTTP and HTTPS to ProxyPilot.
	Enabled bool `json:"enabled"`
	// Managed: a backup exists, i.e. ProxyPilot turned it on and can restore it.
	Managed  bool       `json:"managed"`
	BackupAt *time.Time `json:"backup_at,omitempty"`
	Target   string     `json:"target"`
}

type backup struct {
	CreatedAt time.Time `json:"created_at"`
	Target    string    `json:"target"`
	Services  []Service `json:"services"`
}

// Manager drives networksetup.
type Manager struct {
	Run     execx.Runner
	DataDir string
	GOOS    string // "darwin" in production; tests set it explicitly
}

func New(dataDir string) *Manager {
	return &Manager{Run: execx.OS{}, DataDir: dataDir, GOOS: goos}
}

func (m *Manager) ns(ctx context.Context, args ...string) (string, error) {
	if m.GOOS != "darwin" {
		return "", ErrNotMac
	}
	out, err := m.Run.Run(ctx, "networksetup", args...)
	if err != nil {
		low := strings.ToLower(string(out) + err.Error())
		if strings.Contains(low, "admin") || strings.Contains(low, "authoriz") || strings.Contains(low, "permission") {
			return "", fmt.Errorf("%w: %v", ErrNeedsAdmin, err)
		}
		return "", err
	}
	return string(out), nil
}

// Services lists enabled network services (disabled ones are marked with '*').
func (m *Manager) Services(ctx context.Context) ([]string, error) {
	out, err := m.ns(ctx, "-listallnetworkservices")
	if err != nil {
		return nil, err
	}
	var names []string
	for i, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if i == 0 && strings.Contains(line, "asterisk") || line == "" || strings.HasPrefix(line, "*") {
			continue
		}
		names = append(names, line)
	}
	if len(names) == 0 {
		return nil, ErrNoServices
	}
	return names, nil
}

func parseProxy(out string) Proxy {
	var p Proxy
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "Enabled":
			p.Enabled = v == "Yes"
		case "Server":
			p.Server = v
		case "Port":
			p.Port, _ = strconv.Atoi(v)
		}
	}
	return p
}

func parseBypass(out string) []string {
	if strings.Contains(out, errNoBypassMsg) {
		return []string{}
	}
	var list []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			list = append(list, line)
		}
	}
	return list
}

func (m *Manager) read(ctx context.Context, name string) (Service, error) {
	s := Service{Name: name}
	web, err := m.ns(ctx, "-getwebproxy", name)
	if err != nil {
		return s, err
	}
	sec, err := m.ns(ctx, "-getsecurewebproxy", name)
	if err != nil {
		return s, err
	}
	by, err := m.ns(ctx, "-getproxybypassdomains", name)
	if err != nil {
		return s, err
	}
	s.Web, s.Secure, s.Bypass = parseProxy(web), parseProxy(sec), parseBypass(by)
	return s, nil
}

func (m *Manager) backupPath() string { return filepath.Join(m.DataDir, BackupFile) }

func (m *Manager) readBackup() (*backup, error) {
	b, err := os.ReadFile(m.backupPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoBackup
	}
	if err != nil {
		return nil, err
	}
	var bk backup
	if err := json.Unmarshal(b, &bk); err != nil {
		return nil, fmt.Errorf("corrupt %s: %w", BackupFile, err)
	}
	return &bk, nil
}

// Status reports every service and whether it points at target ("127.0.0.1:9090").
func (m *Manager) Status(ctx context.Context, target string) (Status, error) {
	st := Status{Target: target}
	names, err := m.Services(ctx)
	if err != nil {
		return st, err
	}
	host, port := splitTarget(target)
	st.Enabled = true
	for _, n := range names {
		s, err := m.read(ctx, n)
		if err != nil {
			return st, err
		}
		st.Services = append(st.Services, s)
		if !pointsAt(s.Web, host, port) || !pointsAt(s.Secure, host, port) {
			st.Enabled = false
		}
	}
	if bk, err := m.readBackup(); err == nil {
		st.Managed, st.BackupAt = true, &bk.CreatedAt
	}
	return st, nil
}

func pointsAt(p Proxy, host string, port int) bool {
	return p.Enabled && p.Server == host && p.Port == port
}

func splitTarget(target string) (string, int) {
	h, p, err := net.SplitHostPort(target)
	if err != nil {
		return target, 0
	}
	if h == "" || h == "0.0.0.0" || h == "::" {
		h = "127.0.0.1"
	}
	n, _ := strconv.Atoi(p)
	return h, n
}

// Commands returns the networksetup calls that On would run (shown when admin rights are needed).
func (m *Manager) Commands(names []string, target string, extraBypass ...string) [][]string {
	host, port := splitTarget(target)
	bypass := append(append([]string{}, DefaultBypass...), extraBypass...)
	var cmds [][]string
	for _, n := range names {
		cmds = append(cmds,
			[]string{"networksetup", "-setwebproxy", n, host, strconv.Itoa(port)},
			[]string{"networksetup", "-setsecurewebproxy", n, host, strconv.Itoa(port)},
			append([]string{"networksetup", "-setproxybypassdomains", n}, bypass...),
		)
	}
	return cmds
}

// On saves the current settings (once) and points every enabled service at target.
func (m *Manager) On(ctx context.Context, target string, extraBypass ...string) (Status, error) {
	names, err := m.Services(ctx)
	if err != nil {
		return Status{}, err
	}
	if _, err := m.readBackup(); errors.Is(err, ErrNoBackup) {
		bk := backup{CreatedAt: time.Now().UTC(), Target: target}
		for _, n := range names {
			s, err := m.read(ctx, n)
			if err != nil {
				return Status{}, err
			}
			bk.Services = append(bk.Services, s)
		}
		b, _ := json.MarshalIndent(bk, "", "  ")
		if err := os.WriteFile(m.backupPath(), b, 0o600); err != nil {
			return Status{}, fmt.Errorf("write backup: %w", err)
		}
	} else if err != nil {
		return Status{}, err
	}
	for _, c := range m.Commands(names, target, extraBypass...) {
		if _, err := m.ns(ctx, c[1:]...); err != nil {
			return Status{}, err
		}
	}
	return m.Status(ctx, target)
}

// Off restores the backup taken by On, then deletes it.
func (m *Manager) Off(ctx context.Context) error {
	bk, err := m.readBackup()
	if err != nil {
		return err
	}
	for _, c := range restoreCommands(bk.Services) {
		if _, err := m.ns(ctx, c[1:]...); err != nil {
			// Services removed since the backup are skipped; anything else stops the restore.
			if strings.Contains(err.Error(), "not a recognized network service") {
				continue
			}
			return err
		}
	}
	return os.Remove(m.backupPath())
}

// RestoreCommands returns the networksetup calls Off would run.
func (m *Manager) RestoreCommands() ([][]string, error) {
	bk, err := m.readBackup()
	if err != nil {
		return nil, err
	}
	return restoreCommands(bk.Services), nil
}

// restoreCommands re-applies saved servers and on/off states. networksetup has no way
// to clear a server field, so a service that had none keeps ours, but disabled.
func restoreCommands(services []Service) [][]string {
	var cmds [][]string
	for _, s := range services {
		for _, kind := range []struct {
			set, state string
			p          Proxy
		}{{"-setwebproxy", "-setwebproxystate", s.Web}, {"-setsecurewebproxy", "-setsecurewebproxystate", s.Secure}} {
			if kind.p.Server != "" {
				cmds = append(cmds, []string{"networksetup", kind.set, s.Name, kind.p.Server, strconv.Itoa(kind.p.Port)})
			}
			state := "off"
			if kind.p.Enabled {
				state = "on"
			}
			cmds = append(cmds, []string{"networksetup", kind.state, s.Name, state})
		}
		bypass := s.Bypass
		if len(bypass) == 0 {
			bypass = []string{"Empty"} // networksetup's way to clear the list
		}
		cmds = append(cmds, append([]string{"networksetup", "-setproxybypassdomains", s.Name}, bypass...))
	}
	return cmds
}

// LeftOn reports a backup whose proxy is still active: ProxyPilot turned the
// system proxy on and it was never restored (e.g. the daemon crashed).
func (m *Manager) LeftOn(ctx context.Context) (bool, string) {
	bk, err := m.readBackup()
	if err != nil {
		return false, ""
	}
	st, err := m.Status(ctx, bk.Target)
	if err != nil {
		return false, ""
	}
	return st.Enabled, bk.Target
}
