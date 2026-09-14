package netchaos

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestAddrSatisfiesNetAddr(_ *testing.T) {
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

// TestListenSynthesizedPortWrapsAtMaxPort pins issue #81: nextListenPort
// advanced unboundedly, unlike ephemeralPort (addr.go), which already wraps
// via modulo. After 65535-8000+1 listeners with no explicit port, the next
// synthesized port would exceed the 16-bit range a real TCP port fits in.
// Tested by seeding the counter at the boundary directly rather than by
// actually creating that many listeners.
func TestListenSynthesizedPortWrapsAtMaxPort(t *testing.T) {
	n := NewNetwork()
	n.nextListenPort = maxPort // next unnamed Listen takes maxPort, then wraps

	atMax, err := n.Listen("tcp", "peer-a")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = atMax.Close() }()
	if _, port, err := net.SplitHostPort(atMax.Addr().String()); err != nil || port != strconv.Itoa(maxPort) {
		t.Fatalf("Addr() = %q, want port %d", atMax.Addr(), maxPort)
	}

	wrapped, err := n.Listen("tcp", "peer-b")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wrapped.Close() }()
	_, port, err := net.SplitHostPort(wrapped.Addr().String())
	if err != nil {
		t.Fatalf("net.SplitHostPort(%q): %v", wrapped.Addr(), err)
	}
	portNum, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("port %q is not numeric", port)
	}
	if portNum < 0 || portNum > maxPort {
		t.Fatalf("Addr() = %q, port %d is outside the valid 16-bit TCP range [0, %d]", wrapped.Addr(), portNum, maxPort)
	}
	if portNum != listenPortBase {
		t.Fatalf("Addr() = %q, want the counter to wrap to listenPortBase (%d), got %d", wrapped.Addr(), listenPortBase, portNum)
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

// --- #73: an address with no host is a wildcard bind, never a peer named "" ---

// TestHostlessListensDoNotCollide is issue #73's headline case: against a
// real net.Listen, two separate Listen("tcp", ":0") calls are independent
// wildcard binds and never collide. Before this fix both resolved to the
// peer named "", so the second Listen failed with ErrAddressInUse.
func TestHostlessListensDoNotCollide(t *testing.T) {
	n := NewNetwork()

	first, err := n.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("first Listen(\":0\") = %v, want nil", err)
	}
	defer func() { _ = first.Close() }()

	second, err := n.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("second Listen(\":0\") = %v, want nil (two wildcard binds must not collide)", err)
	}
	defer func() { _ = second.Close() }()

	firstHost, _, err := net.SplitHostPort(first.Addr().String())
	if err != nil {
		t.Fatalf("net.SplitHostPort(%q): %v", first.Addr(), err)
	}
	secondHost, _, err := net.SplitHostPort(second.Addr().String())
	if err != nil {
		t.Fatalf("net.SplitHostPort(%q): %v", second.Addr(), err)
	}
	if firstHost == "" || secondHost == "" {
		t.Fatalf("wildcard listener host = (%q, %q), want both non-empty", firstHost, secondHost)
	}
	if firstHost == secondHost {
		t.Fatalf("two wildcard listeners share the peer identity %q; a Partition naming it would hit both", firstHost)
	}
}

// TestListenEmptyAddressBinds covers the bare "" form alongside ":0" — issue
// #73 names both as hostless. splitAddr reports the same empty host for
// either, so this needs no production code beyond what
// TestHostlessListensDoNotCollide's fix already provides.
func TestListenEmptyAddressBinds(t *testing.T) {
	n := NewNetwork()

	l, err := n.Listen("tcp", "")
	if err != nil {
		t.Fatalf(`Listen("tcp", "") = %v, want nil`, err)
	}
	defer func() { _ = l.Close() }()

	host, _, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatalf("net.SplitHostPort(%q): %v", l.Addr(), err)
	}
	if host == "" {
		t.Fatalf("Addr() = %q, want a synthesized non-empty host", l.Addr())
	}
}

// TestHostlessListenHonoursExplicitPort covers ":8080": the port half is
// still honoured even though the host half is synthesized. A second
// Listen("tcp", ":8080") also succeeds — the one deliberate divergence from
// a real single-process net.Listen, which would fail the second call with
// EADDRINUSE. netchaos models each hostless bind as a separate machine's
// wildcard bind (the framing issue #73 itself uses), and the two
// synthesized hosts differ, so this does not contradict the rule that two
// listeners naming the same host collide regardless of port.
func TestHostlessListenHonoursExplicitPort(t *testing.T) {
	n := NewNetwork()

	first, err := n.Listen("tcp", ":8080")
	if err != nil {
		t.Fatalf(`Listen("tcp", ":8080") = %v, want nil`, err)
	}
	defer func() { _ = first.Close() }()

	_, port, err := net.SplitHostPort(first.Addr().String())
	if err != nil {
		t.Fatalf("net.SplitHostPort(%q): %v", first.Addr(), err)
	}
	if port != "8080" {
		t.Errorf("port of %q = %q, want \"8080\" (explicit port honoured)", first.Addr(), port)
	}

	second, err := n.Listen("tcp", ":8080")
	if err != nil {
		t.Fatalf(`second Listen("tcp", ":8080") = %v, want nil (independent wildcard binds)`, err)
	}
	defer func() { _ = second.Close() }()

	if first.Addr().String() == second.Addr().String() {
		t.Errorf("two wildcard listeners on the same explicit port share an address: %q", first.Addr())
	}
}

// TestWildcardListenerAddrIsDialableAndPartitionable proves the synthesized
// identity is actually useful, not merely non-colliding: the canonical Go
// test pattern of listening on ":0" and dialing back l.Addr() must work, and
// the resulting peer must be a real, Partition-targetable name. It also
// pins the naming trap this change introduces: the peer's name is the host
// half of l.Addr(), never the string originally passed to Listen (which,
// for a hostless bind, split to the empty peer and can no longer resolve to
// anything).
func TestWildcardListenerAddrIsDialableAndPartitionable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork()
		l, err := n.Listen("tcp", ":0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()

		host, _, err := net.SplitHostPort(l.Addr().String())
		if err != nil {
			t.Fatalf("net.SplitHostPort(%q): %v", l.Addr(), err)
		}
		if host == "" {
			t.Fatalf("wildcard listener host is empty; not a real, targetable peer")
		}

		accepted := make(chan net.Conn, 1)
		go func() {
			c, err := l.Accept()
			if err == nil {
				accepted <- c
			}
		}()

		ctx := WithPeerName(context.Background(), "client")
		client, err := n.DialContext(ctx, "tcp", l.Addr().String())
		if err != nil {
			t.Fatalf("DialContext to a wildcard listener's own Addr() = %v, want nil", err)
		}
		defer func() { _ = client.Close() }()
		server := <-accepted
		defer func() { _ = server.Close() }()

		if _, err := client.Write([]byte("before")); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 6)
		nr, err := server.Read(buf)
		if err != nil || string(buf[:nr]) != "before" {
			t.Fatalf("read before partition = (%d, %q, %v), want (6, \"before\", nil)", nr, buf[:nr], err)
		}

		// Partition against the host half of l.Addr(), not the ":0" string
		// originally passed to Listen -- that string names no peer at all
		// once addresses have a host/port split.
		n.Partition("client", host)

		if err := server.SetReadDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Write([]byte("dropped")); err != nil {
			t.Fatal(err)
		}
		if _, err := server.Read(buf); err == nil {
			t.Fatal("read while partitioned succeeded, want it to block until the deadline")
		}
	})
}

// TestDialRejectsHostlessAddress covers the other half of issue #73: unlike
// Listen, Dial has no wildcard-bind meaning to fall back on, so a hostless
// address is simply rejected -- netchaos has no address a hostless dial
// could mean to reach. Before this, ":0" and "" resolved to the peer named
// "" and ":8080" returned ErrConnectionRefused, since nothing had ever
// registered there.
func TestDialRejectsHostlessAddress(t *testing.T) {
	n := NewNetwork()

	for _, addr := range []string{"", ":0", ":8080"} {
		_, err := n.Dial("tcp", addr)
		if err == nil {
			t.Errorf("Dial(\"tcp\", %q) = nil, want an error", addr)
			continue
		}
		var opErr *net.OpError
		if !errors.As(err, &opErr) {
			t.Errorf("Dial(\"tcp\", %q) = %v (%T), want a *net.OpError", addr, err, err)
			continue
		}
		var addrErr *net.AddrError
		if !errors.As(err, &addrErr) {
			t.Errorf("Dial(\"tcp\", %q) = %v, want errors.As(*net.AddrError)", addr, err)
		}
	}
}

// TestDialEmptyAddressOmitsAddrFromOpError pins what the *net.OpError says
// when Dial("tcp", "") is rejected: it must not claim an address the caller
// never wrote. errAddr used to re-join the empty host and zero port back
// into ":0", so the error read "dial tcp :0: ..." -- a specific address
// Dial("tcp", "") never named. Real net.Dial("tcp", "") reports no address
// at all ("dial tcp: missing address"), which is the shape this matches.
func TestDialEmptyAddressOmitsAddrFromOpError(t *testing.T) {
	n := NewNetwork()

	_, err := n.Dial("tcp", "")
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		t.Fatalf(`Dial("tcp", "") = %v (%T), want a *net.OpError`, err, err)
	}
	if opErr.Addr != nil {
		t.Errorf("OpError.Addr = %v, want nil (no address was written)", opErr.Addr)
	}
	if got, want := err.Error(), "dial tcp: missing host"; got != want {
		t.Errorf("Dial(\"tcp\", \"\") error = %q, want %q", got, want)
	}
}
