package netchaos

import "errors"

// This file consolidates every sentinel error netchaos can return, so that
// any failure mode is discoverable and matchable with errors.Is from one
// place.
//
// Match these with errors.Is, never with ==. Every error leaving Listen,
// Dial and DialContext is wrapped in a *net.OpError (M6-2), uniformly and
// including the sentinels below, so that netchaos reports failures in the
// shape real net.Listen and net.Dial do — a caller that type-asserts to
// *net.OpError, or calls Timeout()/Temporary() on the result, behaves the
// same against either. OpError unwraps, so errors.Is keeps working through
// the wrapper; direct == comparison does not.
//
// Two failure modes are deliberately absent from this list:
//
//   - Invalid Option values (e.g. a WithPacketLoss rate outside [0,1]):
//     NewNetwork panics rather than returning an error, so there is no
//     sentinel to match against.
//   - Use of a locally closed conn or listener, and a Read/Write past its
//     deadline: these reuse standard library errors (net.ErrClosed and
//     os.ErrDeadlineExceeded respectively). Write after the peer closed its
//     reading end instead matches syscall.EPIPE where available, with a
//     plain "broken pipe" fallback on plan9, rather than defining new ones,
//     since those are exactly what real net.Conn/net.Listener callers
//     already check for.

// ErrUnsupportedNetwork is returned when a Dial/DialContext/Listen call
// names a network value netchaos doesn't support. v1 is TCP-shaped only:
// "tcp", "tcp4", and "tcp6" are accepted; "udp" and anything else are
// rejected, since UDP's datagram semantics are explicitly out of scope for
// v1 (see docs/06-scope-and-roadmap.md).
var ErrUnsupportedNetwork = errors.New("netchaos: unsupported network")

// setupError keeps the exported sentinel identity and message while also
// matching a standard errno on platforms that define one.
type setupError struct {
	message string
	errno   error
}

func (e *setupError) Error() string        { return e.message }
func (e *setupError) Is(target error) bool { return e.errno != nil && errors.Is(e.errno, target) }

// ErrConnectionRefused is returned by Network.Dial/DialContext when no
// listener is registered for the target address, shaped like a real
// connection-refused failure so code under test takes the same path it
// would against a real closed port. It also matches syscall.ECONNREFUSED
// with errors.Is where that errno exists.
var ErrConnectionRefused error = &setupError{"netchaos: connection refused", errConnRefused}

// ErrAddressInUse is returned by Network.Listen when the given address is
// already registered by another, still-open listener. It also matches
// syscall.EADDRINUSE with errors.Is where that errno exists.
var ErrAddressInUse error = &setupError{"netchaos: address already in use", errAddrInUse}

// ErrBacklogFull is returned by Network.Dial when the target listener's
// accept queue is full. It is distinct from ErrConnectionRefused so a test
// can tell "nobody is listening" apart from "the listener is overwhelmed".
var ErrBacklogFull = errors.New("netchaos: accept backlog full")
