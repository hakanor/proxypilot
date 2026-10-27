package proxy

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"
)

func TestSetLAN(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Serve(ctx, ln)
	for s.Addr() == nil {
		time.Sleep(5 * time.Millisecond)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	lanIP := firstLANIP(t)
	dial := func() error {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(lanIP, itoa(port)), time.Second)
		if err == nil {
			c.Close()
		}
		return err
	}

	if on, fixed := s.LANState(); on || fixed {
		t.Fatalf("loopback start: on=%v fixed=%v", on, fixed)
	}
	if dial() == nil {
		t.Fatal("reachable on the LAN before SetLAN(true)")
	}
	if err := s.SetLAN(true); err != nil {
		t.Fatal(err)
	}
	if on, _ := s.LANState(); !on {
		t.Fatal("LANState off after SetLAN(true)")
	}
	if err := dial(); err != nil {
		t.Fatalf("not reachable on the LAN: %v", err)
	}
	if !s.isSelf(net.JoinHostPort(lanIP, itoa(port)), "http") {
		t.Fatal("LAN address not recognised as the proxy itself")
	}
	if err := s.SetLAN(false); err != nil {
		t.Fatal(err)
	}
	if dial() == nil {
		t.Fatal("still reachable on the LAN after SetLAN(false)")
	}
	// Loopback keeps working throughout.
	c, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("loopback: %v", err)
	}
	c.Close()
}

func firstLANIP(t *testing.T) string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
			return n.IP.String()
		}
	}
	t.Skip("no non-loopback IPv4 address")
	return ""
}

func itoa(n int) string { return strconv.Itoa(n) }
