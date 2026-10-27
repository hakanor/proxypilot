package daemon

import (
	"net"
	"time"

	"github.com/hakanor/proxypilot/internal/events"
)

// lanFile remembers the Setup switch "Allow devices on this network" across restarts.
const lanFile = "lan"

// lanSwitch is the proxy's network access, saved to <datadir>/lan when changed.
type lanSwitch struct{ d *Daemon }

func (d *Daemon) lan() lanSwitch { return lanSwitch{d} }

func (l lanSwitch) LANState() (on, fixed bool) { return l.d.Proxy.LANState() }

func (l lanSwitch) SetLAN(on bool) error {
	if err := l.d.Proxy.SetLAN(on); err != nil {
		return err
	}
	if err := writeFlag(l.d.Config.DataDir, lanFile, on); err != nil {
		l.d.Log.Warn("save network access", "err", err)
	}
	l.d.Bus.Publish(events.ProxyState, map[string]any{"state": "running", "proxy_addr": l.d.proxyAddr()})
	if on {
		l.d.Log.Warn("proxy is reachable from your network; only do this on a trusted network", "port", l.port())
	}
	return nil
}

func (l lanSwitch) port() string {
	_, p, _ := net.SplitHostPort(l.d.state.ProxyAddr)
	return p
}

// restoreLAN re-opens network access if it was on when the daemon last ran.
func (d *Daemon) restoreLAN() {
	if !readFlag(d.Config.DataDir, lanFile) {
		return
	}
	for i := 0; i < 50 && d.Proxy.Addr() == nil; i++ { // Serve sets the listener in its goroutine
		time.Sleep(10 * time.Millisecond)
	}
	if err := d.Proxy.SetLAN(true); err != nil {
		d.Log.Warn("reopen network access", "err", err)
	}
}

// proxyAddr is the listen address as phones see it: 0.0.0.0:port while network access is on.
func (d *Daemon) proxyAddr() string {
	if on, fixed := d.Proxy.LANState(); on && !fixed {
		return net.JoinHostPort("0.0.0.0", d.lan().port())
	}
	return d.state.ProxyAddr
}
