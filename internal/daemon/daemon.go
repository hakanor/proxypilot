package daemon

import (
	"github.com/hakanor/proxypilot/internal/appicon"
	"github.com/hakanor/proxypilot/internal/cdp"

	"github.com/hakanor/proxypilot/internal/repeat"

	"github.com/hakanor/proxypilot/internal/certpage"
	"github.com/hakanor/proxypilot/internal/clients"

	"github.com/hakanor/proxypilot/internal/breakpoints"

	"github.com/hakanor/proxypilot/internal/rules"

	"github.com/hakanor/proxypilot/internal/devices"

	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/hakanor/proxypilot/internal/api"
	"github.com/hakanor/proxypilot/internal/ca"
	"github.com/hakanor/proxypilot/internal/capture"
	"github.com/hakanor/proxypilot/internal/config"
	"github.com/hakanor/proxypilot/internal/events"
	"github.com/hakanor/proxypilot/internal/proxy"
	"github.com/hakanor/proxypilot/internal/redact"
	"github.com/hakanor/proxypilot/internal/store"
	"github.com/hakanor/proxypilot/internal/version"
)

// Daemon owns every long-lived component: store, proxy, rules, control API.
type Daemon struct {
	Config      config.Config
	Log         *slog.Logger
	CA          *ca.RootCA
	Store       *store.Store
	Bus         *events.Bus
	Proxy       *proxy.Server
	Policy      *proxy.SSLPolicy
	Token       string
	API         *api.Server
	Rules       *rules.Engine
	Breakpoints *breakpoints.Manager
	Clients     *clients.Tracker
	Repeater    *repeat.Repeater
	Chrome      *cdp.Manager
	NoCache     *proxy.NoCache
	Throttle    *proxy.Throttle

	Started time.Time
	state   State
}

type RunOptions struct {
	UnsafeRemoteAPI bool
}

func (d *Daemon) status() api.StatusInfo {
	n, _ := d.Store.Count(context.Background())
	return api.StatusInfo{
		Running: true, PID: d.state.PID, ProxyAddr: d.proxyAddr(), APIAddr: d.state.APIAddr,
		StartedAt: d.Started, UptimeSeconds: int64(time.Since(d.Started).Seconds()), Flows: n,
		CAFingerprint: d.CA.Fingerprint(), Session: d.Store.CurrentSession(),
		Subscribers: d.Bus.Subscribers(), Version: version.Version, MaxEntries: d.Config.Store.MaxEntries, DataDir: d.Config.DataDir,
		ActiveRules: d.Rules.Active(), PendingBreakpoints: d.Breakpoints.Count(),
		NoCaching: d.NoCache.Enabled(),
		Throttle:  d.throttleName(),
	}
}

const shutdownGrace = 5 * time.Second

// Run starts everything, blocks until ctx is cancelled, then shuts down cleanly.
// ready (optional) is called once the proxy is listening.
func Run(ctx context.Context, cfg config.Config, log *slog.Logger, opts RunOptions, ready func(*Daemon)) error {
	if log == nil {
		log = slog.Default()
	}
	if st, err := Running(cfg.DataDir); err == nil {
		return fmt.Errorf("%w (pid %d, proxy %s)", ErrAlreadyRunning, st.PID, st.ProxyAddr)
	}
	if err := cfg.EnsureDataDir(); err != nil {
		return err
	}
	d := &Daemon{Config: cfg, Log: log, Bus: events.NewBus(), Started: time.Now()}

	var err error
	if d.Token, err = EnsureToken(cfg.DataDir); err != nil {
		return fmt.Errorf("token: %w", err)
	}
	if d.CA, err = ca.LoadOrCreate(filepath.Join(cfg.DataDir, "ca")); err != nil {
		return fmt.Errorf("ca: %w", err)
	}
	issuer, err := ca.NewIssuer(d.CA, 0)
	if err != nil {
		return err
	}
	mode, err := proxy.ParseSSLMode(cfg.SSL.Mode)
	if err != nil {
		return err
	}
	d.Policy = proxy.NewSSLPolicy(mode, cfg.SSL.Allowlist, cfg.SSL.Denylist)

	if d.Store, err = store.Open(cfg.DataDir, store.Options{MaxEntries: cfg.Store.MaxEntries, InlineBodyLimit: int64(cfg.Store.InlineBodyLimit)}); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer d.Store.Close()

	rec := capture.New(d.Store, d.Bus, log)
	d.Clients = clients.New(cfg.DataDir, d.Bus, capture.ClientLabel)
	rec.Names = d.Clients.Name
	devs := newDevices(d.CA, cfg)
	icons := appicon.New(filepath.Join(cfg.DataDir, "icons"))
	rec.Origin = newOriginFinder(devs.Sim, icons).Origin
	if d.Rules, err = rules.New(cfg.DataDir, d.Bus, log); err != nil {
		return fmt.Errorf("rules: %w", err)
	}
	d.Breakpoints = breakpoints.New(d.Bus)
	d.Rules.SetPauser(d.Breakpoints)
	d.NoCache = &proxy.NoCache{}
	d.NoCache.SetEnabled(readFlag(cfg.DataDir, noCacheFile))
	d.Throttle = &proxy.Throttle{}
	d.Throttle.Set(loadThrottle(cfg.DataDir))
	// Header tweaks and throttling, then observers, then rules decide.
	hooks := proxy.Chain{d.NoCache, d.Throttle, d.Clients, rec, d.Rules}
	d.Chrome = &cdp.Manager{Store: d.Store, Bus: d.Bus, Rules: d.Rules, Log: log, DataDir: cfg.DataDir}
	d.Repeater = &repeat.Repeater{Store: d.Store, Hooks: hooks, Bypass: proxy.Chain{d.Clients, rec}}
	d.API = api.New(api.Deps{
		Store: d.Store, Bus: d.Bus, CA: d.CA, Policy: d.Policy, Token: d.Token, Log: log,
		Status: d.status, Version: version.Version, Recorder: rec, Rules: d.Rules, Breakpoints: d.Breakpoints, Clients: d.Clients, Repeater: d.Repeater, Chrome: d.Chrome, DataDir: cfg.DataDir, Devices: devs, AppIcon: icons.PNG, NoCache: noCacheSwitch{d}, Throttle: throttleSwitch{d},
		Redact: redact.New(cfg.Redact.Headers...),
	}, api.Options{Addr: cfg.API.Listen, UnsafeRemote: opts.UnsafeRemoteAPI})
	if err := d.API.Listen(); err != nil {
		return fmt.Errorf("api listen %s: %w", cfg.API.Listen, err)
	}

	ln, err := net.Listen("tcp", cfg.Proxy.Listen)
	if err != nil {
		return fmt.Errorf("proxy listen %s: %w", cfg.Proxy.Listen, err)
	}
	d.Proxy = proxy.New(proxy.Options{
		Hooks:         hooks,
		DirectHandler: certpage.New(d.CA, d.Clients, ln.Addr().String),
		Logger:        log,
		InlineLimit:   int64(cfg.Store.InlineBodyLimit),
		TempDir:       filepath.Join(cfg.DataDir, "tmp"),
		Issuer:        issuer,
		Policy:        d.Policy,
		Unrecorded:    d.API.IsAPIAddress,
		DisableH2:     cfg.Proxy.DisableH2,
	})
	d.Repeater.Proxy = d.Proxy
	d.API.SetLAN(d.lan())

	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "tmp"), 0o700); err != nil {
		return err
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go d.Store.RunRetention(runCtx, 30*time.Second)
	go d.Rules.Run(runCtx)

	proxyErr := make(chan error, 1)
	go func() { proxyErr <- d.Proxy.Serve(runCtx, ln) }()
	go func() {
		if err := d.API.Serve(runCtx); err != nil {
			proxyErr <- fmt.Errorf("api: %w", err)
		}
	}()

	st := State{PID: os.Getpid(), ProxyAddr: ln.Addr().String(), APIAddr: d.API.Addr().String(), StartedAt: d.Started, Version: version.Version, DataDir: cfg.DataDir}
	d.state = st
	if err := writeState(cfg.DataDir, st); err != nil {
		return err
	}
	defer removeState(cfg.DataDir, st.PID)

	d.restoreLAN()
	d.Bus.Publish(events.ProxyState, map[string]any{"state": "running", "proxy_addr": d.proxyAddr()})
	log.Info("proxypilot running", "proxy", st.ProxyAddr, "pid", st.PID, "data", cfg.DataDir, "ca", d.CA.Fingerprint())
	if ready != nil {
		ready(d)
	}

	select {
	case <-ctx.Done():
	case err := <-proxyErr:
		if err != nil {
			return err
		}
	}
	log.Info("shutting down")
	d.Bus.Publish(events.ProxyState, map[string]any{"state": "stopping"})
	sctx, scancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer scancel()
	d.Chrome.Detach() // a Chrome we launched stays open
	if n := d.Breakpoints.ReleaseAll(); n > 0 {
		log.Info("released paused flows", "count", n)
	}
	if err := d.Proxy.Shutdown(sctx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		log.Warn("proxy shutdown", "err", err)
	}
	return nil
}

var ErrAlreadyRunning = errors.New("proxypilot is already running")

func newDevices(root *ca.RootCA, cfg config.Config) *devices.Service {
	s := devices.New(root, cfg.DataDir)
	s.ADBPath = cfg.Android.ADB
	_, s.ProxyPort, _ = net.SplitHostPort(cfg.Proxy.Listen)
	return s
}
