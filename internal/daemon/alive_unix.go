//go:build !windows

package daemon

import (
	"errors"
	"syscall"
)

// Alive reports whether pid is a running process.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
