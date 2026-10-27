package proxy

import (
	"errors"
	"net"
	"sync"
)

// multiListener feeds connections from several listeners into one Accept, so the
// proxy can start or stop listening on the network while it keeps running.
type multiListener struct {
	primary net.Listener
	conns   chan net.Conn
	done    chan struct{}

	mu    sync.Mutex
	extra map[string]net.Listener
	once  sync.Once
}

func newMultiListener(primary net.Listener) *multiListener {
	m := &multiListener{primary: primary, conns: make(chan net.Conn), done: make(chan struct{}), extra: map[string]net.Listener{}}
	go m.pump(primary)
	return m
}

func (m *multiListener) pump(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return // closed (removed, or the whole listener closed)
		}
		select {
		case m.conns <- c:
		case <-m.done:
			c.Close()
			return
		}
	}
}

func (m *multiListener) Accept() (net.Conn, error) {
	select {
	case c := <-m.conns:
		return c, nil
	case <-m.done:
		return nil, net.ErrClosed
	}
}

func (m *multiListener) Close() error {
	m.once.Do(func() {
		close(m.done)
		m.mu.Lock()
		for k, ln := range m.extra {
			ln.Close()
			delete(m.extra, k)
		}
		m.mu.Unlock()
	})
	return m.primary.Close()
}

func (m *multiListener) Addr() net.Addr { return m.primary.Addr() }

func (m *multiListener) add(key string, ln net.Listener) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old := m.extra[key]; old != nil {
		old.Close()
	}
	m.extra[key] = ln
	go m.pump(ln)
}

func (m *multiListener) remove(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ln := m.extra[key]; ln != nil {
		ln.Close()
		delete(m.extra, key)
	}
}

// LANState reports whether phones on the network can reach the proxy, and whether
// that is fixed by the listen address (started with --proxy-listen 0.0.0.0:…).
func (s *Server) LANState() (on, fixed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return false, false
	}
	if !isLoopback(s.ln.Addr()) {
		return true, true
	}
	return s.lanAddr != nil, false
}

// SetLAN starts or stops listening on all interfaces (same port) next to the loopback
// listener. Connections already open stay open; only new ones are refused.
func (s *Server) SetLAN(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.ln.(*multiListener)
	if !ok || !isLoopback(s.ln.Addr()) {
		return nil // listening on the network already (or not serving)
	}
	if on == (s.lanAddr != nil) {
		return nil
	}
	if !on {
		m.remove("lan")
		s.lanAddr = nil
		s.selfAddr = selfAddresses(s.ln.Addr())
		return nil
	}
	port := s.ln.Addr().(*net.TCPAddr).Port
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{Port: port})
	if err != nil {
		return err
	}
	m.add("lan", ln)
	s.lanAddr = ln.Addr()
	s.selfAddr = selfAddresses(ln.Addr()) // the wildcard set also covers loopback
	return nil
}

func isLoopback(a net.Addr) bool {
	t, ok := a.(*net.TCPAddr)
	return ok && t.IP.IsLoopback()
}
