package macproxy

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Windows: the per-user WinINet settings that browsers and most apps follow (no admin needed).
const (
	winService = "Internet Settings"
	winKey     = `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
)

func (m *Manager) winValue(ctx context.Context, name string) string {
	out, err := m.Run.Run(ctx, "reg", "query", winKey, "/v", name)
	if err != nil {
		return "" // value not set
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && strings.EqualFold(f[0], name) && strings.HasPrefix(f[1], "REG_") {
			return strings.Join(f[2:], " ")
		}
	}
	return ""
}

func (m *Manager) winRead(ctx context.Context) (Service, error) {
	s := Service{Name: winService, Bypass: []string{}}
	on := m.winValue(ctx, "ProxyEnable") == "0x1"
	s.Web, s.Secure = parseWinServer(m.winValue(ctx, "ProxyServer"))
	s.Web.Enabled = on && s.Web.Server != ""
	s.Secure.Enabled = on && s.Secure.Server != ""
	if o := m.winValue(ctx, "ProxyOverride"); o != "" {
		s.Bypass = strings.Split(o, ";")
	}
	return s, nil
}

// parseWinServer reads "host:port" (all protocols) or "http=h:p;https=h:p".
func parseWinServer(v string) (web, secure Proxy) {
	if !strings.Contains(v, "=") {
		web = hostPort(v)
		return web, web
	}
	for _, part := range strings.Split(v, ";") {
		k, hp, _ := strings.Cut(part, "=")
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "http":
			web = hostPort(hp)
		case "https":
			secure = hostPort(hp)
		}
	}
	return web, secure
}

func hostPort(v string) Proxy {
	h, p := splitTarget(strings.TrimSpace(v))
	if p == 0 {
		return Proxy{}
	}
	return Proxy{Server: h, Port: p}
}

func winCommands(host string, port int, bypass []string) [][]string {
	hp := host + ":" + strconv.Itoa(port)
	return [][]string{
		{"reg", "add", winKey, "/v", "ProxyServer", "/t", "REG_SZ", "/d", "http=" + hp + ";https=" + hp, "/f"},
		{"reg", "add", winKey, "/v", "ProxyOverride", "/t", "REG_SZ", "/d", strings.Join(append(bypass, "<local>"), ";"), "/f"},
		{"reg", "add", winKey, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f"},
	}
}

func winRestore(services []Service) [][]string {
	var cmds [][]string
	for _, s := range services {
		var parts []string
		if s.Web.Server != "" {
			parts = append(parts, fmt.Sprintf("http=%s:%d", s.Web.Server, s.Web.Port))
		}
		if s.Secure.Server != "" {
			parts = append(parts, fmt.Sprintf("https=%s:%d", s.Secure.Server, s.Secure.Port))
		}
		if len(parts) > 0 {
			cmds = append(cmds, []string{"reg", "add", winKey, "/v", "ProxyServer", "/t", "REG_SZ", "/d", strings.Join(parts, ";"), "/f"})
		} else {
			cmds = append(cmds, []string{"reg", "delete", winKey, "/v", "ProxyServer", "/f"})
		}
		if len(s.Bypass) > 0 {
			cmds = append(cmds, []string{"reg", "add", winKey, "/v", "ProxyOverride", "/t", "REG_SZ", "/d", strings.Join(s.Bypass, ";"), "/f"})
		} else {
			cmds = append(cmds, []string{"reg", "delete", winKey, "/v", "ProxyOverride", "/f"})
		}
		on := "0"
		if s.Web.Enabled || s.Secure.Enabled {
			on = "1"
		}
		cmds = append(cmds, []string{"reg", "add", winKey, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", on, "/f"})
	}
	return cmds
}

// Linux: GNOME's proxy settings (Firefox, Chrome and most desktop apps follow them).
const (
	gnomeService = "GNOME"
	gnomeSchema  = "org.gnome.system.proxy"
)

func (m *Manager) gs(ctx context.Context, schema, key string) (string, error) {
	out, err := m.Run.Run(ctx, "gsettings", "get", schema, key)
	return strings.Trim(strings.TrimSpace(string(out)), "'"), err
}

func (m *Manager) gnomeRead(ctx context.Context) (Service, error) {
	s := Service{Name: gnomeService, Bypass: []string{}}
	mode, err := m.gs(ctx, gnomeSchema, "mode")
	if err != nil {
		return s, err
	}
	s.Mode = mode
	for _, k := range []struct {
		sub string
		p   *Proxy
	}{{"http", &s.Web}, {"https", &s.Secure}} {
		h, _ := m.gs(ctx, gnomeSchema+"."+k.sub, "host")
		ps, _ := m.gs(ctx, gnomeSchema+"."+k.sub, "port")
		k.p.Server = h
		k.p.Port, _ = strconv.Atoi(strings.TrimPrefix(ps, "uint32 "))
		k.p.Enabled = mode == "manual" && h != ""
	}
	ign, _ := m.gs(ctx, gnomeSchema, "ignore-hosts")
	for _, h := range strings.Split(strings.Trim(ign, "[]@as "), ",") {
		if h = strings.Trim(strings.TrimSpace(h), "'"); h != "" {
			s.Bypass = append(s.Bypass, h)
		}
	}
	return s, nil
}

func gvList(l []string) string {
	q := make([]string, len(l))
	for i, v := range l {
		q[i] = "'" + strings.ReplaceAll(v, "'", "") + "'"
	}
	return "[" + strings.Join(q, ", ") + "]"
}

func gnomeSet(host string, port int, secureHost string, securePort int, bypass []string, mode string) [][]string {
	return [][]string{
		{"gsettings", "set", gnomeSchema + ".http", "host", host},
		{"gsettings", "set", gnomeSchema + ".http", "port", strconv.Itoa(port)},
		{"gsettings", "set", gnomeSchema + ".https", "host", secureHost},
		{"gsettings", "set", gnomeSchema + ".https", "port", strconv.Itoa(securePort)},
		{"gsettings", "set", gnomeSchema, "ignore-hosts", gvList(bypass)},
		{"gsettings", "set", gnomeSchema, "mode", mode},
	}
}

func gnomeCommands(host string, port int, bypass []string) [][]string {
	return gnomeSet(host, port, host, port, bypass, "manual")
}

func gnomeRestore(services []Service) [][]string {
	var cmds [][]string
	for _, s := range services {
		mode := s.Mode
		if mode == "" {
			mode = "none"
		}
		cmds = append(cmds, gnomeSet(s.Web.Server, s.Web.Port, s.Secure.Server, s.Secure.Port, s.Bypass, mode)...)
	}
	return cmds
}
