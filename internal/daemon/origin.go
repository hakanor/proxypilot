package daemon

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/hakanor/proxypilot/internal/appicon"
	"github.com/hakanor/proxypilot/internal/devices/simctl"
	"github.com/hakanor/proxypilot/internal/procinfo"
)

// originFinder names local clients by process: Mac apps by bundle, simulator
// apps under their simulator ("iPhone 17 Pro · Simulator").
type originFinder struct {
	procs *procinfo.Resolver
	sim   *simctl.Simctl
	icons *appicon.Store // learns each app's bundle for the sidebar icons

	mu     sync.Mutex
	names  map[string]string // UDID → name
	booted []string
	at     time.Time
}

func newOriginFinder(sim *simctl.Simctl, icons *appicon.Store) *originFinder {
	return &originFinder{procs: procinfo.New(), sim: sim, icons: icons, names: map[string]string{}}
}

func (o *originFinder) Origin(addr string) (string, string, bool) {
	info, ok := o.procs.Lookup(addr)
	if !ok {
		return "", "", false
	}
	c := procinfo.Classify(info.Path)
	if o.icons != nil {
		o.icons.Remember(c.App, c.Bundle)
	}
	if !c.Simulator {
		return "This Mac", c.App, true
	}
	return o.simName(c.SimulatorUDID), c.App, true
}

// simName refreshes the simulator list at most every 30s on a miss.
func (o *originFinder) simName(udid string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	lookup := func() (string, bool) {
		if udid != "" {
			n, ok := o.names[udid]
			return n, ok
		}
		if len(o.booted) == 1 {
			return o.booted[0], true
		}
		return "", false
	}
	if n, ok := lookup(); ok {
		return n + " · Simulator"
	}
	if o.sim != nil && time.Since(o.at) > 30*time.Second {
		o.at = time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		list, err := o.sim.List(ctx)
		cancel()
		if err == nil {
			o.booted = o.booted[:0]
			for _, d := range list {
				o.names[strings.ToUpper(d.UDID)] = d.Name
				if d.Booted {
					o.booted = append(o.booted, d.Name)
				}
			}
		}
	}
	if n, ok := lookup(); ok {
		return n + " · Simulator"
	}
	return "iOS Simulator"
}
