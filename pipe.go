package netchaos

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// defaultPipeBound is the buffer bound used by pipes when WithPipeBound is
// not given. It is an arbitrary but reasonable analogue of a small OS socket
// receive buffer: large enough that ordinary test traffic doesn't spuriously
// hit back-pressure, small enough that an unread connection still applies
// back-pressure to its writer instead of buffering without limit.
const defaultPipeBound = 64 * 1024

// WithPipeBound sets the per-connection-direction buffer bound applied to
// every connection n handles, in place of the default (defaultPipeBound, 64
// KiB). The bound is what decides when a Write blocks on back-pressure: once
// a direction's buffered-but-unread bytes would exceed it, the next Write
// blocks until a Read frees enough room (M6-17, #52) — the same mechanism
// newConnPairWithBound (conn.go) already varied from in-package tests, now
// reachable from outside the package.
//
// One write is always admitted whole even if it exceeds the bound: a fault
// unit is a whole Write (M0-3), never split, so a payload larger than the
// bound is accepted only into a direction currently holding no buffered
// bytes, and that single write's bytes may then sit above the configured
// bound until read. Nothing else can be admitted alongside it until the
// buffer drains back to (or below) the bound.
//
// bound must be positive; NewNetwork panics otherwise, naming WithPipeBound
// and the offending value.
func WithPipeBound(bound int) Option {
	return func(c *networkConfig) {
		c.pipeBoundEnabled = true
		c.pipeBound = bound
	}
}

// validatePipeBound panics, naming WithPipeBound and the offending value, if
// bound is not positive.
func validatePipeBound(bound int) {
	if bound <= 0 {
		panic(fmt.Sprintf("netchaos: WithPipeBound: bound must be > 0, got %v", bound))
	}
}

// deliverFunc is the seam between a write being admitted (accounted against
// the pipe's bound) and its data becoming visible to readers. M1 wires only
// passThroughDeliver, which delivers synchronously; later milestones swap
// this field to delay (latency) or skip (packet loss) delivery without
// changing anything else about the pipe.
type deliverFunc func(p *pipe, data []byte)

// passThroughDeliver delivers data to readers immediately.
func passThroughDeliver(p *pipe, data []byte) {
	p.readable = append(p.readable, data)
	p.broadcastLocked()
}

// pipe is a one-directional, buffered, byte-stream queue: a producer enqueues
// whole write payloads (the fault unit, per M0-3) and a consumer reads them
// back in order, with a short buffer possibly coalescing several payloads
// into one Read. It is the delivery primitive two of which, crossed, make a
// full-duplex connection (see conn.go).
//
// A pipe is safe for concurrent use by multiple goroutines.
type pipe struct {
	mu         sync.Mutex
	readable   [][]byte      // queue of not-yet-fully-consumed write payloads
	bufBytes   int           // bytes currently accounted against bound
	closed     bool          // producer has closed; pending data may still be delivered
	readClosed bool          // consumer has closed; queued data and its timer are discarded
	eofSeen    bool          // once observed, EOF cannot be hidden by a later partition
	notify     chan struct{} // closed and replaced on every state change
	bound      int
	deliver    deliverFunc
	// A graceful end-of-stream signal waits for Heal when this pair is
	// partitioned. Raw pipes have no Network and expose EOF immediately.
	partitionNetwork *Network
	partitionPair    pairKey

	// loss, latency, duplicate, and corrupt are this pipe's per-fault-kind
	// draw streams (M0-4), derived once at creation from the owning
	// connection's ordinal and the side writing into this pipe. trace
	// records the fault decisions made for this direction, in write order.
	// All five are nil for a pipe created without fault wiring (e.g. by
	// pipe-level tests); fault code added from M2-2 onward must handle that
	// case, since M1 semantics (pass-through delivery) are still reachable
	// with none of them set.
	loss      *stream
	latency   *stream
	duplicate *stream // M7-8
	corrupt   *stream // M7-9
	trace     *traceRecorder

	// pending holds units admitted but held back for latency (M2-2) or
	// bandwidth (M7-5), release-ordered (each entry's releaseAt is >= every
	// earlier entry's), so a single timer armed for the head always fires in
	// write order regardless of the delay any individual unit drew. timer is
	// that one live *time.AfterFunc, or nil when pending is empty. Both are
	// nil for a pipe with neither latency nor bandwidth configured.
	pending []pendingUnit
	timer   *time.Timer

	// busyUntil is the instant this direction's (possibly throttled) link
	// finishes transmitting everything admitted so far (M7-5). It is the
	// serialization clock WithBandwidth models delay against: each unit's
	// transmission starts no earlier than max(now, busyUntil), so back-to-
	// back writes on a slow link queue behind each other instead of each
	// drawing its delay independently. Zero (its default) is a no-op when
	// bandwidth isn't configured, since every comparison against it is
	// max(now, busyUntil), and now is never before the zero time.
	busyUntil time.Time

	// arms counts how many times the latency timer has been armed, purely so
	// a test can assert that a write appended behind an existing pending head
	// does not re-arm for a deadline the live timer already targets (M6-8).
	// It is instrumentation, not state anything reads to make a decision:
	// nothing outside armLatencyTimerLocked writes it, and nothing in the
	// production path reads it. It is guarded by p.mu like every other field
	// here.
	arms int
}

// newPipe returns an empty, open pipe with the given buffer bound.
func newPipe(bound int) *pipe {
	return &pipe{
		notify:  make(chan struct{}),
		bound:   bound,
		deliver: passThroughDeliver,
	}
}

// broadcastLocked wakes every goroutine currently waiting on p.notify. Must
// be called with p.mu held.
func (p *pipe) broadcastLocked() {
	close(p.notify)
	p.notify = make(chan struct{})
}

// tryRead attempts to satisfy a Read without blocking. If it can, ch is nil
// and (n, err) is the final answer. Otherwise ch is the channel to wait on
// before retrying; it is snapshotted under the same lock as the "nothing to
// read yet" determination, so no wakeup between the check and the wait can
// be missed.
func (p *pipe) tryRead(b []byte) (n int, ch <-chan struct{}, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.readClosed {
		return 0, nil, io.EOF
	}

	if len(p.readable) > 0 {
		for len(p.readable) > 0 && n < len(b) {
			chunk := p.readable[0]
			copied := copy(b[n:], chunk)
			n += copied
			if copied == len(chunk) {
				p.readable = p.readable[1:]
			} else {
				p.readable[0] = chunk[copied:]
			}
		}
		p.bufBytes -= n
		p.broadcastLocked() // may have freed space for a blocked writer

		// A zero-length write is admitted and draws from every configured
		// fault stream like any other unit (the draw discipline in docs/04),
		// but it carries nothing for a reader. Handing one back as (0, nil)
		// would be a wakeup no real net.Conn produces, and io.Reader
		// discourages that return for a non-empty buffer — so when every
		// payload consumed here was empty, fall through and keep waiting for
		// something real. len(b) == 0 is the one case where (0, nil) is the
		// correct answer, and it keeps its existing behaviour.
		if n > 0 || len(b) == 0 {
			return n, nil, nil
		}
	}

	if p.closed && len(p.pending) == 0 {
		if p.eofSeen {
			return 0, nil, io.EOF
		}
		if p.partitionNetwork != nil {
			if partitioned, partCh := p.partitionNetwork.checkPartition(p.partitionPair); partitioned {
				return 0, partCh, nil
			}
		}
		p.eofSeen = true
		return 0, nil, io.EOF
	}
	return 0, p.notify, nil
}

// tryWrite attempts to admit data without blocking. If it can, ch is nil and
// (n, err) is the final answer. Otherwise ch is the channel to wait on before
// retrying, snapshotted under the same lock as the admission check.
//
// data must already be a private copy the caller will not mutate or retain
// (conn.go copies the caller-supplied slice before calling this, per
// io.Writer's non-retention convention).
func (p *pipe) tryWrite(data []byte) (n int, ch <-chan struct{}, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed || p.readClosed {
		return 0, nil, io.ErrClosedPipe
	}

	// A write larger than the bound can never fit "alongside" other data, so
	// the only rule consistent with never splitting a write (M0-3) is: admit
	// it once the pipe is completely empty, even if that exceeds bound.
	if p.bufBytes == 0 || p.bufBytes+len(data) <= p.bound {
		p.bufBytes += len(data)
		p.deliver(p, data)
		return len(data), nil, nil
	}

	return 0, p.notify, nil
}

// read blocks until it can return data, io.EOF, or a closed-pipe write error
// is not applicable to read; read only ever returns io.EOF for a closed,
// drained pipe.
func (p *pipe) read(b []byte) (int, error) {
	for {
		n, ch, err := p.tryRead(b)
		if ch == nil {
			return n, err
		}
		<-ch
	}
}

// write blocks until data is admitted or the pipe is closed.
func (p *pipe) write(data []byte) (int, error) {
	for {
		n, ch, err := p.tryWrite(data)
		if ch == nil {
			return n, err
		}
		<-ch
	}
}

// close finishes the producer's side of the pipe. Already admitted units
// remain pending until their scheduled delivery, then the reader sees EOF.
// The consumer's close and Reset use abort instead.
func (p *pipe) close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}
	p.closed = true
	p.broadcastLocked()
	return nil
}

// abort closes the consumer's side and discards everything it can no longer
// read. It also stops the timer and unblocks writers. Reset calls it on both
// directions of the connection.
func (p *pipe) abort() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.readClosed {
		return
	}
	p.readClosed = true
	p.closed = true
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	p.pending = nil
	p.readable = nil
	p.bufBytes = 0
	p.broadcastLocked()
}
