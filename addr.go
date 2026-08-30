package netchaos

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Port synthesis constants. Both ranges are chosen to look like what a
// reader expects from a real address rather than to mean anything:
// listeners land in a low, server-ish range and dialers in the conventional
// ephemeral one, so a test failure printing "server:8000" and
// "ephemeral-3:32771" reads the way the equivalent failure against the real
// stack would.
const (
	// listenPortBase is the first port Listen synthesizes for a listener
	// whose address named no port. Successive such listeners take the next
	// value, in Listen order.
	listenPortBase = 8000

	// ephemeralPortBase is the first port a dialing side is given. A
	// dialer's port is derived from its connection ordinal rather than from
	// a counter of its own, which keeps it consistent with the determinism
	// contract for free: a dial that never establishes never takes an
	// ordinal, so it never shifts any other connection's port either.
	ephemeralPortBase = 32768

	maxPort = 65535
)

// addr identifies a simulated peer within a Network.
//
// An address has two halves and they do different jobs. The host half is
// the identity: it is what Dial and Listen resolve against, what
// Network.Partition and Network.Heal name, and the only half peerName
// returns. The port half is presentation: it exists so that a netchaos
// address is shaped like a real one, and so code under test can call
// net.SplitHostPort on RemoteAddr().String() and take the same path it
// takes against the real stack (M7-1, issue #49). Nothing in netchaos
// resolves, matches, or partitions on a port.
//
// That split is why "server" and "server:8080" name the same peer:
// Partition("server") reaches a connection dialed to "server:8080", which is
// the property that lets addresses gain structure without invalidating every
// Partition call already written.
//
// splitAddr is the single function that takes an address string apart, and
// peerName is the identity projection over it. Dial resolution
// (Network.DialContext), listener registration (Network.Listen) and
// partition lookup (Network.Partition) all go through them, so there is
// still exactly one place the address-to-peer relationship is defined — the
// property this comment guaranteed before addresses had a port, and which
// M6-10 required be re-established deliberately rather than assumed.
type addr struct {
	network string // "tcp", "tcp4", or "tcp6", stored verbatim
	peer    string // the host half: the peer's identity
	port    int    // the port half: presentation only
}

// Network returns the network name the address was created with.
func (a *addr) Network() string { return a.network }

// String returns the address in host:port form. It is stable, parses with
// net.SplitHostPort, and is intended to be useful verbatim in test failure
// output.
func (a *addr) String() string { return net.JoinHostPort(a.peer, strconv.Itoa(a.port)) }

// splitAddr takes a dial/listen address string apart into the peer it names
// and, when the caller wrote one, the port it asked for.
//
// An address with no port at all ("server") is the ordinary case and is not
// an error: netchaos synthesizes one. An explicit ":0" is the same request
// spelled the way the real stack spells it — "give me a port, I don't care
// which" — so it reports explicit=false too, and the caller assigns.
//
// A malformed address is reported as a *net.AddrError, which is what
// net.Listen and net.Dial produce for the same input. No netchaos sentinel
// is introduced for it: the standard library already has the right error
// here, and adding one would grow the exported surface for nothing.
func splitAddr(s string) (host string, port int, explicit bool, err error) {
	if !strings.Contains(s, ":") {
		return s, 0, false, nil
	}

	host, portStr, splitErr := net.SplitHostPort(s)
	if splitErr != nil {
		return "", 0, false, &net.AddrError{Err: "invalid address", Addr: s}
	}
	if portStr == "" || portStr == "0" {
		return host, 0, false, nil
	}

	port, convErr := strconv.Atoi(portStr)
	if convErr != nil || port < 0 || port > maxPort {
		return "", 0, false, &net.AddrError{Err: "invalid port", Addr: s}
	}
	return host, port, true, nil
}

// peerName derives a peer's identity from a dial/listen address string: the
// host half, with any port stripped. It is the one function used everywhere
// netchaos needs to go from an address to the peer it names.
//
// A malformed address has no meaningful host to return, so peerName hands
// back the string it was given rather than an empty name. Callers that need
// the address to be well formed (Listen, DialContext) call splitAddr
// directly and surface its error; peerName's own callers — Partition and
// Heal — are documented no-ops on a peer nothing has registered, and an
// unparseable name is simply one more name nothing has registered.
func peerName(dialAddr string) string {
	host, _, _, err := splitAddr(dialAddr)
	if err != nil {
		return dialAddr
	}
	return host
}

// ephemeralPort maps a connection ordinal onto the ephemeral port range. It
// wraps rather than overflowing, so a Network that establishes more
// connections than the range has ports reuses a port instead of producing an
// address that cannot be parsed. Ports are presentation (see addr), so a
// collision there costs nothing: identity is the host half, and that stays
// unique per ordinal.
func ephemeralPort(ordinal uint64) int {
	span := uint64(maxPort + 1 - ephemeralPortBase)
	return ephemeralPortBase + int(ordinal%span)
}

// ephemeralPeerName is the identity given to a dialer that never named
// itself with WithPeerName — the analogue of an OS-assigned ephemeral client
// port.
//
// The separator is a dash, not a colon, and that is load-bearing rather than
// cosmetic. The name used to be "ephemeral:N", which parses as host
// "ephemeral" plus port N once addresses have a port — collapsing every
// unnamed dialer in a Network onto a single identity, so a Partition naming
// it would hit all of them at once. A dash keeps each one distinct.
func ephemeralPeerName(ordinal uint64) string {
	return fmt.Sprintf("ephemeral-%d", ordinal)
}

// errAddr builds the addr that goes on a *net.OpError leaving Listen or
// DialContext. The address may be the malformed one that caused the error,
// so it falls back to naming the whole string as the peer rather than
// discarding what the caller actually wrote.
func errAddr(network, s string) *addr {
	host, port, _, err := splitAddr(s)
	if err != nil {
		return &addr{network: network, peer: s}
	}
	return &addr{network: network, peer: host, port: port}
}

// validateNetwork reports whether network is one netchaos v1 supports.
func validateNetwork(network string) error {
	switch network {
	case "tcp", "tcp4", "tcp6":
		return nil
	case "udp", "udp4", "udp6":
		// All three spellings name the same exclusion, so all three get the
		// explanation rather than the generic message for an unrecognized
		// network — docs/06-scope-and-roadmap.md excludes UDP once, not
		// three times.
		return fmt.Errorf("%w: udp support is out of scope for netchaos v1", ErrUnsupportedNetwork)
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedNetwork, network)
	}
}

// peerNameCtxKey is the unexported context key a dialer's own peer identity
// travels under. Dial's frozen signature — Dial(network, addr string) — has
// no parameter for "which peer am I dialing as," so the identity has to
// ride through DialContext's context.Context instead, the one input that
// signature already has room for.
//
// WithPeerName (below) is the exported setter, added in M2-4 for
// Network.Partition, which is what actually needs a dialer to have a
// stable, partition-targetable name. A dialer that never names itself gets a
// synthesized ephemeral identity, usable for I/O like any other connection
// but not nameable by a Partition call — analogous to an OS-assigned
// ephemeral client port — and, as a consequence, never blocks on dial-time
// partition checks either, since no partition could ever be declared against
// a name that isn't known until the dial that produces it completes.
//
// The signature constraint above is real, but it is a constraint on Dial
// rather than on net.Dial-shaped dialing, which is a distinction M6-9 did not
// draw — it recorded the caveat as permanent for anything with Dial's shape.
// Network.DialerFor (M7-2) returns a function with exactly that shape which
// carries a name through this key, so a drop-in dialer can be
// partition-targetable after all. Only a bare Dial call cannot.
type peerNameCtxKey struct{}

// WithPeerName returns a copy of ctx that declares name as the calling
// peer's own identity for a subsequent DialContext call — the identity
// Network.Partition and Network.Heal target. Without it, a dialer gets a
// synthesized, unpartitionable ephemeral identity, usable for I/O like any
// other connection but never nameable by a Partition call and never
// blocked by one either.
//
// The name is a peer identity, not an address: a port written into it is
// stripped, exactly as it is on the addresses passed to Dial and Listen.
//
// Use this when the dial needs a context anyway — to bound how long it waits
// on a partition, for instance. Network.DialerFor is the same capability for
// code that wants a plain net.Dial-shaped function to hand to a client
// constructor.
func WithPeerName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, peerNameCtxKey{}, name)
}

// peerNameFromContext returns the peer name a dialer declared for itself via
// ctx, if any.
func peerNameFromContext(ctx context.Context) (string, bool) {
	name, ok := ctx.Value(peerNameCtxKey{}).(string)
	return name, ok
}
