//go:build windows

package cli

import (
	"os"
	"syscall"
)

// terminate stops the daemon (Windows has no SIGTERM; the daemon exits without cleanup).
func terminate(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// detachAttr starts the daemon without a console window tied to this one.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000008} // DETACHED_PROCESS
}
