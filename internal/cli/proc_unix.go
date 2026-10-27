//go:build !windows

package cli

import "syscall"

// terminate asks the daemon to shut down cleanly.
func terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// detachAttr starts the daemon in its own session so it outlives the terminal.
func detachAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
