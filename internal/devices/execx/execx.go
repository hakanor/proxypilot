// Package execx runs external tools behind an interface so device code can be tested
// without touching the machine.
package execx

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Runner runs a command and returns its combined output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// OS runs real commands.
type OS struct{}

func (OS) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	var out bytes.Buffer
	c := exec.CommandContext(ctx, name, args...)
	c.Stdout, c.Stderr = &out, &out
	err := c.Run()
	if err != nil {
		return out.Bytes(), &Error{Cmd: Quote(name, args...), Output: strings.TrimSpace(out.String()), Err: err}
	}
	return out.Bytes(), nil
}

// Error carries the failing command and its output for the user.
type Error struct {
	Cmd    string
	Output string
	Err    error
}

func (e *Error) Error() string {
	if e.Output != "" {
		return fmt.Sprintf("%s: %s", e.Cmd, e.Output)
	}
	return fmt.Sprintf("%s: %v", e.Cmd, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Quote renders a command line the user can paste into a shell.
func Quote(name string, args ...string) string {
	parts := []string{name}
	for _, a := range args {
		if a == "" || strings.ContainsAny(a, ` 	'"*$\;&|()<>`) {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}
