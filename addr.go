package netchaos

import (
	"context"
	"fmt"
)

// addr identifies a simulated peer within a Network.
//
// netchaos gives each peer exactly one address: the string passed to Dial
// or Listen IS the peer's name, with no separate host:port structure and no
// peer owning multiple addresses. peerName is the single function that
// derives a peer's identity from an address string; both dial resolution
// (Network.Dial, in netchaos.go) and partition lookup (Network.Partition,
// added in M2-4) call it, so there is exactly one place the address<->peer
// relationship is defined.
type addr struct {
	network string // "tcp", "tcp4", or "tcp6", stored verbatim
	peer    string
}

// Network returns the network name the address was created with.
func (a *addr) Network() string { return a.network }

// String returns the peer name. It is stable and intended to be useful
// verbatim in test failure output.
func (a *addr) String() string { return a.peer }

// peerName derives a peer's identity from a dial/listen address string. It
// is the one function used everywhere netchaos needs to go from an address
// to the peer it names.
func peerName(dialAddr string) string { return dialAddr }

// validateNetwork reports whether network is one netchaos v1 supports.
func validateNetwork(network string) error {
	switch network {
	case "tcp", "tcp4", "tcp6":
		return nil
	case "udp":
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
// No exported setter ships in M1: nothing consumes it until M2-4 adds
// Network.Partition, which is what actually needs a dialer to have a
// stable, partition-targetable name. DialContext already reads it (see
// netchaos.go); a dialer that never sets it gets a synthesized ephemeral
// identity, usable for I/O like any other connection but not nameable by a
// future Partition call — analogous to an OS-assigned ephemeral client
// port.
type peerNameCtxKey struct{}

// peerNameFromContext returns the peer name a dialer declared for itself via
// ctx, if any.
func peerNameFromContext(ctx context.Context) (string, bool) {
	name, ok := ctx.Value(peerNameCtxKey{}).(string)
	return name, ok
}
