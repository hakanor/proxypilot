package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// colorOn: stdout is a terminal, --no-color not set, NO_COLOR unset.
func (a *App) colorOn() bool {
	if a.Flags.NoColor || os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := a.Out.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

const (
	ansiReset  = "\x1b[0m"
	ansiDim    = "\x1b[2m"
	ansiBold   = "\x1b[1m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiGray   = "\x1b[90m"
)

func (a *App) paint(code, s string) string {
	if !a.colorOn() || s == "" {
		return s
	}
	return code + s + ansiReset
}

// statusText renders a status code or state with the DESIGN.md color meaning.
func (a *App) statusText(status int, state, errKind string) string {
	switch {
	case errKind != "":
		return a.paint(ansiRed, "ERR")
	case state == "tunneled":
		return a.paint(ansiGray, "TUN")
	case state == "pending":
		return a.paint(ansiGray, "…")
	case state == "paused":
		return a.paint(ansiYellow, "PAU")
	case status >= 500:
		return a.paint(ansiRed, strconv.Itoa(status))
	case status >= 400:
		return a.paint(ansiYellow, strconv.Itoa(status))
	case status >= 300:
		return a.paint(ansiGray, strconv.Itoa(status))
	case status > 0:
		return a.paint(ansiGreen, strconv.Itoa(status))
	}
	return "-"
}

// formatBytes uses 1024 steps with one decimal from KB up (DESIGN.md §8).
func formatBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(n) / 1024
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

// parseBytes accepts 4096, 4kb, 4KB, 1.5mb, 2m.
func parseBytes(s string) (int64, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	mult := float64(1)
	for _, u := range []struct {
		suffix string
		m      float64
	}{{"gb", 1 << 30}, {"mb", 1 << 20}, {"kb", 1 << 10}, {"g", 1 << 30}, {"m", 1 << 20}, {"k", 1 << 10}, {"b", 1}} {
		if strings.HasSuffix(t, u.suffix) {
			t, mult = strings.TrimSuffix(t, u.suffix), u.m
			break
		}
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid size %q (examples: 4096, 4kb, 1mb)", s)
	}
	return int64(v * mult), nil
}

func formatMs(ms float64) string {
	switch {
	case ms <= 0:
		return "-"
	case ms < 1000:
		return fmt.Sprintf("%.0fms", ms)
	default:
		return fmt.Sprintf("%.1fs", ms/1000)
	}
}

func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// truncate shortens s to n runes with a middle ellipsis (keeps host and tail).
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n || n < 8 {
		return s
	}
	head := (n - 1) * 2 / 3
	tail := n - 1 - head
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}

// hostPath is the compact request label used in tables.
func hostPath(host, path, url string) string {
	if host == "" {
		return url
	}
	return host + path
}
