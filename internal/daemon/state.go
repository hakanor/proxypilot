// Package daemon runs the long-lived ProxyPilot process and lets the CLI
// find, inspect and stop it through files in the data directory.
package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	StateFile = "daemon.json"
	PIDFile   = "proxypilot.pid"
	TokenFile = "token"
	LogFile   = "proxypilot.log"
)

// State is written by the running daemon and read by `status` / `stop`.
type State struct {
	PID       int       `json:"pid"`
	ProxyAddr string    `json:"proxy_addr"`
	APIAddr   string    `json:"api_addr"`
	StartedAt time.Time `json:"started_at"`
	Version   string    `json:"version"`
	DataDir   string    `json:"data_dir"`
}

func writeState(dir string, st State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, StateFile), b, 0o600); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, PIDFile), []byte(strconv.Itoa(st.PID)+"\n"), 0o600)
}

func removeState(dir string, pid int) {
	// Only remove files that still describe this process.
	if st, err := readStateFile(dir); err == nil && st.PID != pid {
		return
	}
	_ = os.Remove(filepath.Join(dir, StateFile))
	_ = os.Remove(filepath.Join(dir, PIDFile))
}

func readStateFile(dir string) (State, error) {
	var st State
	b, err := os.ReadFile(filepath.Join(dir, StateFile))
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(b, &st)
}

// ErrNotRunning means no live daemon owns the data directory.
var ErrNotRunning = errors.New("proxypilot is not running")

// Running returns the state of a live daemon, cleaning up stale files.
func Running(dir string) (State, error) {
	st, err := readStateFile(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return st, ErrNotRunning
	}
	if err != nil {
		return st, err
	}
	if !Alive(st.PID) {
		removeState(dir, st.PID)
		return st, ErrNotRunning
	}
	return st, nil
}

// EnsureToken returns the control API token, creating 32 random bytes (hex) on first use.
func EnsureToken(dir string) (string, error) {
	path := filepath.Join(dir, TokenFile)
	if b, err := os.ReadFile(path); err == nil {
		if tok := strings.TrimSpace(string(b)); tok != "" {
			return tok, nil
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(buf)
	return tok, writeAtomic(path, []byte(tok+"\n"), 0o600)
}

// ReadToken reads the token without creating it.
func ReadToken(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, TokenFile))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
