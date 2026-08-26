package netchaos

import "errors"

// This file consolidates every sentinel error netchaos can return, so that
// any failure mode is discoverable and matchable with errors.Is from one
// place. Two failure modes are deliberately absent from this list:
//
//   - Invalid Option values (e.g. a WithPacketLoss rate outside [0,1]):
//     NewNetwork panics rather than returning an error, so there is no
//     sentinel to match against.
//   - Use of a closed conn or listener, and a Read/Write past its deadline:
//     both reuse standard library errors (net.ErrClosed and
//     os.ErrDeadlineExceeded respectively) rather than defining new ones,
//     since those are exactly what real net.Conn/net.Listener callers
//     already check for.

// ErrUnsupportedNetwork is returned when a Dial/DialContext/Listen call
// names a network value netchaos doesn't support. v1 is TCP-shaped only:
// "tcp", "tcp4", and "tcp6" are accepted; "udp" and anything else are
// rejected, since UDP's datagram semantics are explicitly out of scope for
// v1 (see docs/06-scope-and-roadmap.md).
var ErrUnsupportedNetwork = errors.New("netchaos: unsupported network")

// ErrConnectionRefused is returned by Network.Dial/DialContext when no
// listener is registered for the target address, shaped like a real
// connection-refused failure so code under test takes the same path it
// would against a real closed port.
var ErrConnectionRefused = errors.New("netchaos: connection refused")

// ErrAddressInUse is returned by Network.Listen when the given address is
// already registered by another, still-open listener.
var ErrAddressInUse = errors.New("netchaos: address already in use")

// ErrBacklogFull is returned by Network.Dial when the target listener's
// accept queue is full. It is distinct from ErrConnectionRefused so a test
// can tell "nobody is listening" apart from "the listener is overwhelmed."
var ErrBacklogFull = errors.New("netchaos: accept backlog full")
