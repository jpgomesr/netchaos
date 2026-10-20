package netchaos

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestTCPAddressesAndNameAliases(t *testing.T) {
	n := NewNetwork()
	l, err := n.Listen("tcp", "api.internal:8080")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	serverAddr, ok := l.Addr().(*net.TCPAddr)
	if !ok || serverAddr.IP == nil || serverAddr.Port != 8080 || serverAddr.Network() != "tcp" {
		t.Fatalf("listener address = %#v, want *net.TCPAddr at port 8080", l.Addr())
	}
	for _, target := range []string{"api.internal:8080", serverAddr.String()} {
		c, err := n.Dial("tcp", target)
		if err != nil {
			t.Fatal(err)
		}
		a, ok := c.RemoteAddr().(*net.TCPAddr)
		if !ok || !a.IP.Equal(serverAddr.IP) {
			t.Fatalf("remote = %#v, want %v", c.RemoteAddr(), serverAddr)
		}
		if _, ok := c.LocalAddr().(*net.TCPAddr); !ok {
			t.Fatalf("local = %T", c.LocalAddr())
		}
		_ = c.Close()
	}
	if again, err := n.Listen("tcp", "api.internal:8080"); !errors.Is(err, ErrAddressInUse) {
		if err == nil {
			_ = again.Close()
		}
		t.Fatalf("repeat name: %v", err)
	}
}

func TestTCPAddressFamiliesAndLiteralCollision(t *testing.T) {
	n := NewNetwork()
	for _, tc := range []struct {
		network, address string
		ipv4             bool
	}{
		{"tcp4", "v4.internal", true}, {"tcp6", "v6.internal", false},
	} {
		l, err := n.Listen(tc.network, tc.address)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()
		a := l.Addr().(*net.TCPAddr)
		if (a.IP.To4() != nil) != tc.ipv4 {
			t.Fatalf("%s: wrong family: %v", tc.network, a)
		}
		c, err := n.Dial(tc.network, a.String())
		if err != nil {
			t.Fatal(err)
		}
		_ = c.Close()
		if literal, err := n.Listen(tc.network, a.String()); err == nil {
			_ = literal.Close()
			t.Fatalf("literal %v stole a synthetic address", a)
		}
	}
}

func TestBareIPv6Literal(t *testing.T) {
	n := NewNetwork()
	l, err := n.Listen("tcp6", "2001:db8::1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if got := l.Addr().(*net.TCPAddr).IP.String(); got != "2001:db8::1" {
		t.Fatalf("IP = %s", got)
	}
	c, err := n.Dial("tcp6", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
}

func TestSyntheticAddressSkipsExistingLiteralAndStaysStable(t *testing.T) {
	n := NewNetwork()
	literal, err := n.Listen("tcp", "198.18.0.0:9000")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = literal.Close() }()
	first, err := n.Listen("tcp", "api.internal")
	if err != nil {
		t.Fatal(err)
	}
	ip := first.Addr().(*net.TCPAddr).IP
	if ip.Equal(literal.Addr().(*net.TCPAddr).IP) {
		t.Fatalf("synthetic IP collided with literal: %v", ip)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := n.Listen("tcp", "api.internal")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	if !second.Addr().(*net.TCPAddr).IP.Equal(ip) {
		t.Fatalf("IP changed after reopening: %v -> %v", ip, second.Addr())
	}
	if _, err := n.Dial("tcp", "name-that-exists-only-in-public-dns.example"); !errors.Is(err, ErrConnectionRefused) {
		t.Fatalf("unknown name should use topology only: %v", err)
	}
}

func TestTCPAddressPartitionAlias(t *testing.T) {
	n := NewNetwork()
	l, err := n.Listen("tcp", "api.internal")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	client := n.DialerFor("client.internal")
	c, err := client("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	n.Partition(c.LocalAddr().String(), l.Addr().String())
	ctx, cancel := context.WithTimeout(WithPeerName(context.Background(), "client.internal"), time.Millisecond)
	defer cancel()
	if _, err := n.DialContext(ctx, "tcp", "api.internal"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("dial across IP-addressed partition = %v", err)
	}
	n.Heal("client.internal", "api.internal")
}
