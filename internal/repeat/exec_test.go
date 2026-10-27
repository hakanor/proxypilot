package repeat

import osexec "os/exec"

// runShell runs a command and returns its stdout (used to check cURL quoting in a real shell).
func runShell(name string, args ...string) (string, error) {
	out, err := osexec.Command(name, args...).Output()
	return string(out), err
}
