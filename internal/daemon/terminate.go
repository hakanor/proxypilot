package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/hakanor/proxypilot/internal/devices"
	"github.com/hakanor/proxypilot/internal/devices/macproxy"
)

// TerminateResult says what Terminate cleaned up before the daemon stopped.
type TerminateResult struct {
	Released            int  `json:"released_breakpoints"`
	AgentRules          int  `json:"agent_rules_removed"`
	BreakpointRules     int  `json:"breakpoint_rules_removed"`
	SystemProxyRestored bool `json:"system_proxy_restored"`
}

// terminator is the "Terminate" button: release paused flows, drop agent rules and
// (optionally) breakpoint rules, restore the system proxy, then stop the daemon.
type terminator struct {
	d    *Daemon
	devs *devices.Service
	quit chan struct{}
}

func (t *terminator) Terminate(ctx context.Context, deleteBreakpoints bool) (TerminateResult, error) {
	var res TerminateResult
	var errs []error
	res.Released = t.d.Breakpoints.ReleaseAll()
	n, err := t.d.Rules.DeleteOwner("agent:*")
	res.AgentRules = n
	errs = append(errs, err)
	if deleteBreakpoints {
		for _, r := range t.d.Rules.List() {
			if r.Type == "breakpoint" {
				if err := t.d.Rules.Delete(r.ID); err == nil {
					res.BreakpointRules++
				} else {
					errs = append(errs, err)
				}
			}
		}
	}
	if t.devs != nil && t.devs.Proxy != nil {
		switch err := t.devs.Proxy.Off(ctx); {
		case err == nil:
			res.SystemProxyRestored = true
		case !errors.Is(err, macproxy.ErrNoBackup) && !errors.Is(err, macproxy.ErrNotMac):
			errs = append(errs, err)
		}
	}
	// Stop shortly after, so the HTTP response still reaches the caller.
	go func() {
		time.Sleep(300 * time.Millisecond)
		select {
		case <-t.quit:
		default:
			close(t.quit)
		}
	}()
	return res, errors.Join(errs...)
}
