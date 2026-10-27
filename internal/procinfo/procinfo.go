// Package procinfo finds which local process opened a connection to the proxy, so
// traffic from this Mac can be grouped by real app — and iOS Simulator traffic
// (which shares the Mac's network) can be told apart from Mac apps.
package procinfo

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Info describes the client process of a connection.
type Info struct {
	PID  int
	Path string // full executable path
}

// Origin is how the sidebar groups a process.
type Origin struct {
	App string
	// SimulatorUDID is set for apps running in an iOS Simulator.
	SimulatorUDID string
	// Simulator is true for any simulator process (UDID may be unknown for system apps).
	Simulator bool
	// Bundle is the outermost .app directory ("" for plain binaries like curl).
	Bundle string
}

var simDevice = regexp.MustCompile(`/CoreSimulator/Devices/([0-9A-Fa-f-]{36})/`)

// Classify turns an executable path into an app name and simulator membership.
//
//	…/CoreSimulator/Devices/<UDID>/data/Containers/Bundle/Application/…/Starlight.app/Starlight → Starlight, simulator UDID
//	/Applications/Google Chrome.app/Contents/Frameworks/…/Google Chrome Helper.app/… → Google Chrome
//	/usr/bin/curl → curl
func Classify(path string) Origin {
	o := Origin{}
	if m := simDevice.FindStringSubmatch(path); m != nil {
		o.SimulatorUDID, o.Simulator = strings.ToUpper(m[1]), true
	} else if strings.Contains(path, ".simruntime/") || strings.Contains(path, "/CoreSimulator/") {
		o.Simulator = true // simulator system app (MobileSafari…) without a device path
	}
	// The outermost .app bundle names the app (helpers live inside it).
	if i := strings.Index(path, ".app/"); i >= 0 {
		o.App = filepath.Base(path[:i])
		o.Bundle = path[:i+len(".app")]
	} else if strings.HasSuffix(path, ".app") {
		o.App = strings.TrimSuffix(filepath.Base(path), ".app")
		o.Bundle = path
	} else {
		o.App = filepath.Base(path)
	}
	switch o.App {
	case "MobileSafari":
		o.App = "Safari"
	}
	return o
}

type entry struct {
	info Info
	ok   bool
	at   time.Time
}

// Resolver looks up and caches the process behind client addresses.
type Resolver struct {
	mu    sync.Mutex
	cache map[string]entry
	self  int
	path  func(ctx context.Context, pid int) (string, error) // pid → executable path
	// lookup is replaceable in tests.
	lookup func(port int) (Info, bool)
}

func New() *Resolver {
	r := &Resolver{cache: map[string]entry{}, self: os.Getpid(), path: pathOf}
	r.lookup = r.lsof
	return r
}

const ttl = 10 * time.Minute

// Lookup returns the local process for a loopback client address ("127.0.0.1:63588").
// Remote clients (phones) and unsupported systems return ok=false.
func (r *Resolver) Lookup(addr string) (Info, bool) {
	if r == nil || runtime.GOOS != "darwin" {
		return Info{}, false
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return Info{}, false
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return Info{}, false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return Info{}, false
	}
	r.mu.Lock()
	if e, hit := r.cache[addr]; hit && time.Since(e.at) < ttl {
		r.mu.Unlock()
		return e.info, e.ok
	}
	r.mu.Unlock()
	info, ok := r.lookup(port)
	r.mu.Lock()
	if len(r.cache) > 4096 { // ports are reused; old entries are noise
		r.cache = map[string]entry{}
	}
	r.cache[addr] = entry{info, ok, time.Now()}
	r.mu.Unlock()
	return info, ok
}

// lsof finds the process whose end of a TCP connection uses local port `port`
// (the client side of a connection to the proxy). Other connections that merely
// involve the same port number elsewhere are ignored.
func (r *Resolver) lsof(port int) (Info, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:ESTABLISHED", "-Fpn").Output()
	if err != nil && len(out) == 0 {
		return Info{}, false
	}
	return r.pick(ctx, string(out), port)
}

// pick reads `lsof -Fpn` output: p<pid> lines followed by n<local>-><remote> lines.
func (r *Resolver) pick(ctx context.Context, out string, port int) (Info, bool) {
	suffix := ":" + strconv.Itoa(port) + "->"
	pid := 0
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			pid, _ = strconv.Atoi(line[1:])
		case strings.HasPrefix(line, "n") && pid != 0 && pid != r.self:
			local, _, ok := strings.Cut(line[1:], "->")
			if !ok || !strings.HasSuffix(local+"->", suffix) {
				continue
			}
			path, err := r.path(ctx, pid)
			if err != nil {
				continue
			}
			return Info{PID: pid, Path: path}, true
		}
	}
	return Info{}, false
}

func pathOf(ctx context.Context, pid int) (string, error) {
	out, err := exec.CommandContext(ctx, "ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	return strings.TrimSpace(string(out)), err
}
