package ca

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"time"
)

const SystemKeychain = "/Library/Keychains/System.keychain"

// TrustStatus reports whether the OS trusts the CA: "trusted", "not_trusted"
// or "unknown" (unsupported OS or tooling missing).
func (r *RootCA) TrustStatus() string {
	if runtime.GOOS == "windows" {
		if exec.Command("certutil", "-user", "-verifystore", "Root", r.FingerprintSHA1()).Run() != nil {
			return "not_trusted"
		}
		return "trusted"
	}
	if runtime.GOOS == "linux" {
		for _, p := range []string{"/usr/local/share/ca-certificates/proxypilot.crt", "/etc/pki/ca-trust/source/anchors/proxypilot.crt"} {
			if _, err := os.Stat(p); err == nil {
				return "trusted"
			}
		}
		return "not_trusted"
	}
	if runtime.GOOS != "darwin" {
		return "unknown"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := exec.LookPath("security"); err != nil {
		return "unknown"
	}
	// verify-cert evaluates the certificate against the user+admin trust settings.
	if err := exec.CommandContext(ctx, "security", "verify-cert", "-c", r.CertPath(), "-p", "basic").Run(); err != nil {
		return "not_trusted"
	}
	return "trusted"
}

// TrustCommand is the macOS command that trusts the CA system-wide (needs root).
func (r *RootCA) TrustCommand() []string {
	if runtime.GOOS == "windows" { // current user's Root store: no admin, Windows asks once
		return []string{"certutil", "-user", "-addstore", "Root", r.CertPath()}
	}
	return []string{"security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", SystemKeychain, r.CertPath()}
}

// UntrustCommands remove the trust setting and the certificate itself (needs root).
func (r *RootCA) UntrustCommands() [][]string {
	if runtime.GOOS == "windows" {
		return [][]string{{"certutil", "-user", "-delstore", "Root", r.FingerprintSHA1()}}
	}
	return [][]string{
		{"security", "remove-trusted-cert", "-d", r.CertPath()},
		{"security", "delete-certificate", "-Z", r.FingerprintSHA1(), SystemKeychain},
	}
}
