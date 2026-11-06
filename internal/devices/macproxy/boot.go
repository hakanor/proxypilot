package macproxy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// A login hook that runs `proxypilot system-proxy off` once, so a restart while the
// system proxy points at ProxyPilot comes back as if ProxyPilot never ran.
// It exists only between On and Off.
const bootName = "com.proxypilot.restore-proxy"

func (m *Manager) bootCommand() []string {
	exe, err := exec.LookPath("proxypilot") // brew's stable symlink, not the versioned Cellar path
	if err != nil {
		if exe, err = os.Executable(); err != nil {
			return nil
		}
	}
	return []string{exe, "--data-dir", m.DataDir, "system-proxy", "off"}
}

func (m *Manager) armBoot(ctx context.Context) error {
	if m.BootDir == "" {
		return nil
	}
	c := m.bootCommand()
	if c == nil {
		return nil
	}
	switch m.GOOS {
	case "darwin":
		var args strings.Builder
		for _, a := range c {
			fmt.Fprintf(&args, "<string>%s</string>", xmlEsc(a))
		}
		plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>` + bootName + `</string>
<key>ProgramArguments</key><array>` + args.String() + `</array>
<key>RunAtLoad</key><true/>
</dict></plist>
`
		return writeFile(filepath.Join(m.BootDir, bootName+".plist"), plist)
	case "linux":
		q := make([]string, len(c))
		for i, a := range c {
			q[i] = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(a) + `"`
		}
		return writeFile(filepath.Join(m.BootDir, bootName+".desktop"),
			"[Desktop Entry]\nType=Application\nName=ProxyPilot: restore proxy settings\nExec="+strings.Join(q, " ")+"\nNoDisplay=true\nX-GNOME-Autostart-enabled=true\n")
	case "windows":
		q := make([]string, len(c))
		for i, a := range c {
			q[i] = `"` + a + `"`
		}
		_, err := m.Run.Run(ctx, "reg", "add", m.BootDir, "/v", bootName, "/t", "REG_SZ", "/d", strings.Join(q, " "), "/f")
		return err
	}
	return nil
}

func (m *Manager) disarmBoot(ctx context.Context) {
	switch m.GOOS {
	case "darwin":
		_ = os.Remove(filepath.Join(m.BootDir, bootName+".plist"))
	case "linux":
		_ = os.Remove(filepath.Join(m.BootDir, bootName+".desktop"))
	case "windows":
		if m.BootDir != "" {
			_, _ = m.Run.Run(ctx, "reg", "delete", m.BootDir, "/v", bootName, "/f")
		}
	}
}

func writeFile(path, s string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(s), 0o644)
}

func xmlEsc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// defaultBootDir is where the login hook goes; "" disables it.
func defaultBootDir(goos string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents")
	case "linux":
		if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
			return filepath.Join(v, "autostart")
		}
		return filepath.Join(home, ".config", "autostart")
	case "windows": // RunOnce removes the entry itself after it runs
		return `HKCU\Software\Microsoft\Windows\CurrentVersion\RunOnce`
	}
	return ""
}
