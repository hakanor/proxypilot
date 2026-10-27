package cli

import (
	"errors"
	"fmt"
)

// Exit codes shared by every CLI command. Scripts and agents rely on these.
const (
	ExitOK           = 0
	ExitError        = 1
	ExitUsage        = 2
	ExitNoDaemon     = 3
	ExitNotFound     = 4
	ExitTimeout      = 5
	ExitUnauthorized = 6
)

// ExitErr carries an exit code alongside the error message.
type ExitErr struct {
	Code int
	Err  error
}

func (e *ExitErr) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit %d", e.Code)
	}
	return e.Err.Error()
}

func (e *ExitErr) Unwrap() error { return e.Err }

// Exit wraps err with an exit code.
func Exit(code int, err error) error {
	return &ExitErr{Code: code, Err: err}
}

// Exitf builds an ExitErr from a format string.
func Exitf(code int, format string, args ...any) error {
	return &ExitErr{Code: code, Err: fmt.Errorf(format, args...)}
}

// codeFor maps an error returned by a command to a process exit code.
func codeFor(err error) int {
	if err == nil {
		return ExitOK
	}
	var ee *ExitErr
	if errors.As(err, &ee) {
		return ee.Code
	}
	return ExitError
}
