//go:build windows

package daemon

import "os"

// Alive reports whether pid is a running process (on Windows FindProcess opens a handle).
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
