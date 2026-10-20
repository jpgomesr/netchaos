package netchaos

import (
	"errors"
	"net"
	"testing"
)

func TestListenersResolveByPeerAndPort(t *testing.T) {
	n := NewNetwork()
	a, err := n.Listen("tcp", "server:8080")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	b, err := n.Listen("tcp", "server:9090")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	if duplicate, err := n.Listen("tcp", "server:8080"); !errors.Is(err, ErrAddressInUse) {
		if err == nil {
			_ = duplicate.Close()
		}
		t.Fatalf("duplicate port: %v", err)
	}
	for _, tc := range []struct {
		target string
		l      net.Listener
	}{{"server:8080", a}, {"server:9090", b}, {a.Addr().String(), a}, {b.Addr().String(), b}} {
		c, err := n.Dial("tcp", tc.target)
		if err != nil {
			t.Fatal(err)
		}
		accepted, err := tc.l.Accept()
		if err != nil {
			t.Fatal(err)
		}
		if got, want := accepted.LocalAddr().String(), tc.l.Addr().String(); got != want {
			t.Fatalf("dial %s delivered to %s, want %s", tc.target, got, want)
		}
		_ = accepted.Close()
		_ = c.Close()
	}
	for _, target := range []string{"server", "server:9999", "missing:8080"} {
		if c, err := n.Dial("tcp", target); !errors.Is(err, ErrConnectionRefused) {
			if err == nil {
				_ = c.Close()
			}
			t.Fatalf("Dial(%s) = %v, want refusal", target, err)
		}
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	c, err := n.Dial("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
}

func TestSynthesizedListenerPortSkipsOccupiedAfterWrap(t *testing.T) {
	n := NewNetwork()
	a, err := n.Listen("tcp", "server:8000")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	b, err := n.Listen("tcp", "server:65535")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	n.nextListenPort = maxPort
	c, err := n.Listen("tcp", "server:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if got := c.Addr().(*net.TCPAddr).Port; got != 8001 {
		t.Fatalf("synthesized port = %d, want 8001", got)
	}
}

func TestPortlessDialFamilySelection(t *testing.T) {
	n := NewNetwork()
	v4, err := n.Listen("tcp4", "server:8080")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = v4.Close() }()
	v6, err := n.Listen("tcp6", "server:9090")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = v6.Close() }()
	for _, tc := range []struct {
		network, target string
		l               net.Listener
	}{{"tcp4", "server", v4}, {"tcp6", "server", v6}, {"tcp", v4.Addr().String(), v4}, {"tcp", v6.Addr().String(), v6}} {
		c, err := n.Dial(tc.network, tc.target)
		if err != nil {
			t.Fatalf("Dial(%s, %s): %v", tc.network, tc.target, err)
		}
		a, err := tc.l.Accept()
		if err != nil {
			t.Fatal(err)
		}
		_ = a.Close()
		_ = c.Close()
	}
	if _, err := n.Dial("tcp4", "server:9090"); !errors.Is(err, ErrConnectionRefused) {
		t.Fatalf("tcp4 to tcp6 listener = %v", err)
	}
}

func TestPartitionHealAndResetCoverEveryPort(t *testing.T) {
	n := NewNetwork()
	var listeners []net.Listener
	var clients []net.Conn
	for _, address := range []string{"server:8080", "server:9090"} {
		l, err := n.Listen("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, l)
		c, err := n.DialerFor("client")("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, c)
	}
	defer func() {
		for _, c := range clients {
			_ = c.Close()
		}
		for _, l := range listeners {
			_ = l.Close()
		}
	}()
	n.Partition(clients[0].LocalAddr().String(), listeners[0].Addr().String())
	for _, c := range clients {
		if _, err := c.Write([]byte("lost")); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range n.Trace() {
		if e.Side == SideDialer && !e.Partitioned {
			t.Fatalf("port not partitioned: %+v", e)
		}
	}
	n.Heal("client", "server")
	for _, c := range clients {
		if _, err := c.Write([]byte("delivered")); err != nil {
			t.Fatal(err)
		}
	}
	var healed int
	for _, e := range n.Trace() {
		if e.Side == SideDialer && !e.Partitioned {
			healed++
		}
	}
	if healed != 2 {
		t.Fatalf("healed writes = %d, want 2", healed)
	}
	n.Reset(clients[0].LocalAddr().String(), listeners[0].Addr().String())
	for _, c := range clients {
		if _, err := c.Write([]byte("reset")); !errors.Is(err, errConnReset) {
			t.Fatalf("reset across ports = %v", err)
		}
	}
}
