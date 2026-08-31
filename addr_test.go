package netchaos

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

func TestAddrSatisfiesNetAddr(t *testing.T) {
	var _ net.Addr = (*addr)(nil)
}

func TestAddrString(t *testing.T) {
	a := &addr{network: "tcp", peer: "server-a", port: 8080}
	if got, want := a.Network(), "tcp"; got != want {
		t.Fatalf("Network() = %q, want %q", got, want)
	}
	if got, want := a.String(), "server-a:8080"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// TestPeerFromAddr exercises the single address<->peer-name function used by
// both dial resolution (here) and, later, M2-4's partition lookup. netchaos
// v1 gives each peer exactly one address (the addr string IS the peer name),
// so there is no multi-address-per-peer case to cover.
func TestPeerFromAddr(t *testing.T) {
	tests := []struct{ addr, want string }{
		{"client", "client"},
		{"server-a", "server-a"},
		{"server-b", "server-b"},
	}
	for _, tt := range tests {
		if got := peerName(tt.addr); got != tt.want {
			t.Errorf("peerName(%q) = %q, want %q", tt.addr, got, tt.want)
		}
	}
}

func TestRejectsUDP(t *testing.T) {
	err := validateNetwork("udp")
	if !errors.Is(err, ErrUnsupportedNetwork) {
		t.Fatalf("validateNetwork(\"udp\") = %v, want errors.Is ErrUnsupportedNetwork", err)
	}
}

func TestValidateNetworkAcceptsTCPVariants(t *testing.T) {
	for _, n := range []string{"tcp", "tcp4", "tcp6"} {
		if err := validateNetwork(n); err != nil {
			t.Errorf("validateNetwork(%q) = %v, want nil", n, err)
		}
	}
}

func TestValidateNetworkRejectsUnknown(t *testing.T) {
	if err := validateNetwork("carrier-pigeon"); err == nil {
		t.Fatal("validateNetwork(\"carrier-pigeon\") = nil, want an error")
	}
}

// TestValidateNetworkRejectsUDPVariants covers all three spellings of the
// same exclusion. docs/06-scope-and-roadmap.md treats UDP as one excluded
// thing, not three, so "udp4" and "udp6" get the explanation rather than
// falling through to the generic message for a network nobody has heard of.
func TestValidateNetworkRejectsUDPVariants(t *testing.T) {
	const wantMsg = "udp support is out of scope for netchaos v1"
	for _, network := range []string{"udp", "udp4", "udp6"} {
		err := validateNetwork(network)
		if !errors.Is(err, ErrUnsupportedNetwork) {
			t.Errorf("validateNetwork(%q) = %v, want errors.Is(ErrUnsupportedNetwork)", network, err)
			continue
		}
		if !strings.Contains(err.Error(), wantMsg) {
			t.Errorf("validateNetwork(%q) = %q, want a message containing %q", network, err, wantMsg)
		}
	}
}

// TestValidateNetworkUnknownStaysGeneric guards the other half of M6-3: the
// UDP explanation must not leak onto networks it does not explain.
func TestValidateNetworkUnknownStaysGeneric(t *testing.T) {
	err := validateNetwork("unix")
	if !errors.Is(err, ErrUnsupportedNetwork) {
		t.Fatalf("validateNetwork(\"unix\") = %v, want errors.Is(ErrUnsupportedNetwork)", err)
	}
	if got, want := err.Error(), `netchaos: unsupported network: "unix"`; got != want {
		t.Fatalf("validateNetwork(\"unix\") = %q, want %q", got, want)
	}
}

func TestPeerNameFromContextAbsent(t *testing.T) {
	if name, ok := peerNameFromContext(context.Background()); ok || name != "" {
		t.Fatalf("peerNameFromContext(no value) = (%q, %v), want (\"\", false)", name, ok)
	}
}

func TestPeerNameFromContextPresent(t *testing.T) {
	ctx := context.WithValue(context.Background(), peerNameCtxKey{}, "client")
	name, ok := peerNameFromContext(ctx)
	if !ok || name != "client" {
		t.Fatalf("peerNameFromContext(with value) = (%q, %v), want (\"client\", true)", name, ok)
	}
}

// --- M7-1: addresses carry a host:port shape ------------------------------

// TestSplitHostPortOnRemoteAddr is the finding #49 was filed for: code under
// test that wants the host half of a remote address — logging, metrics
// labelling, allow-listing — calls net.SplitHostPort on it and gets an error
// against netchaos where it succeeds against the real stack.
func TestSplitHostPortOnRemoteAddr(t *testing.T) {
	n := NewNetwork()
	client, server := dialPair(t, n)

	for _, tt := range []struct {
		name     string
		addr     net.Addr
		wantHost string
	}{
		{"client.RemoteAddr", client.RemoteAddr(), "server"},
		{"server.LocalAddr", server.LocalAddr(), "server"},
	} {
		host, port, err := net.SplitHostPort(tt.addr.String())
		if err != nil {
			t.Errorf("net.SplitHostPort(%s = %q): %v", tt.name, tt.addr, err)
			continue
		}
		if host != tt.wantHost {
			t.Errorf("host of %s = %q, want %q", tt.name, host, tt.wantHost)
		}
		if port == "" {
			t.Errorf("port of %s is empty", tt.name)
		}
	}

	// The dialing side has to split too — it is what a server logs about its
	// client.
	if _, _, err := net.SplitHostPort(client.LocalAddr().String()); err != nil {
		t.Errorf("net.SplitHostPort(client.LocalAddr() = %q): %v", client.LocalAddr(), err)
	}
}

// TestListenHonoursExplicitPort keeps the address the caller actually wrote.
func TestListenHonoursExplicitPort(t *testing.T) {
	n := NewNetwork()
	l, err := n.Listen("tcp", "server:8080")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	if got, want := l.Addr().String(), "server:8080"; got != want {
		t.Fatalf("Addr() = %q, want %q", got, want)
	}
}

// TestListenEphemeralPortIsAssigned covers the ":0" case #49 notes is
// missing: a caller that does not care which port it gets asks for one, the
// way it would against the real stack.
func TestListenEphemeralPortIsAssigned(t *testing.T) {
	n := NewNetwork()

	first, err := n.Listen("tcp", "server-a:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()

	second, err := n.Listen("tcp", "server-b:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()

	for _, l := range []net.Listener{first, second} {
		_, port, err := net.SplitHostPort(l.Addr().String())
		if err != nil {
			t.Fatalf("net.SplitHostPort(%q): %v", l.Addr(), err)
		}
		if port == "0" {
			t.Errorf("Addr() = %q, want a synthesized port rather than a literal 0", l.Addr())
		}
	}
	if first.Addr().String() == second.Addr().String() {
		t.Errorf("two ephemeral listeners share an address: %q", first.Addr())
	}
}

// TestUnnamedDialersHaveDistinctIdentities pins the property that survives
// the change of address shape. An unnamed dialer's identity used to be the
// whole string "ephemeral:N", which parses as host "ephemeral" plus port N
// once addresses have structure — collapsing every unnamed dialer onto one
// host, and onto one another as far as newPairKey is concerned.
func TestUnnamedDialersHaveDistinctIdentities(t *testing.T) {
	n := NewNetwork()
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	first, err := n.Dial("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()

	second, err := n.Dial("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()

	firstHost, _, err := net.SplitHostPort(first.LocalAddr().String())
	if err != nil {
		t.Fatalf("net.SplitHostPort(%q): %v", first.LocalAddr(), err)
	}
	secondHost, _, err := net.SplitHostPort(second.LocalAddr().String())
	if err != nil {
		t.Fatalf("net.SplitHostPort(%q): %v", second.LocalAddr(), err)
	}
	if firstHost == secondHost {
		t.Errorf("two unnamed dialers share the peer identity %q; a Partition naming it would hit both", firstHost)
	}
}

// TestPeerNameStripsAnExplicitPort covers the other half of the identity
// rule: the port is presentation, the host is identity, so an address
// written with a port names the same peer as one written without.
func TestPeerNameStripsAnExplicitPort(t *testing.T) {
	tests := []struct{ addr, want string }{
		{"server", "server"},
		{"server:8080", "server"},
		{"server:0", "server"},
	}
	for _, tt := range tests {
		if got := peerName(tt.addr); got != tt.want {
			t.Errorf("peerName(%q) = %q, want %q", tt.addr, got, tt.want)
		}
	}
}
