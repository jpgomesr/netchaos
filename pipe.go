package netchaos

import (
	"io"
	"sync"
	"time"
)

// defaultPipeBound is the buffer bound used by pipes when no other bound is
// specified. It is an arbitrary but reasonable analogue of a small OS socket
// receive buffer: large enough that ordinary test traffic doesn't spuriously
// hit back-pressure, small enough that an unread connection still applies
// back-pressure to its writer instead of buffering without limit.
const defaultPipeBound = 64 * 1024

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
	mu       sync.Mutex
	readable [][]byte // queue of not-yet-fully-consumed write payloads
	bufBytes int      // bytes currently accounted against bound
	closed   bool
	notify   chan struct{} // closed and replaced on every state change
	bound    int
	deliver  deliverFunc

	// loss, latency, and corrupt are this pipe's per-fault-kind draw streams
	// (M0-4), derived once at creation from the owning connection's ordinal
	// and the side writing into this pipe. trace records the fault
	// decisions made for this direction, in write order. All four are nil
	// for a pipe created without fault wiring (e.g. by pipe-level tests);
	// fault code added from M2-2 onward must handle that case, since M1
	// semantics (pass-through delivery) are still reachable with none of
	// them set.
	loss    *stream
	latency *stream
	corrupt *stream // M7-9
	trace   *traceRecorder

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

	if p.closed {
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

	if p.closed {
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

// close closes the pipe. It is idempotent and never returns a non-nil error;
// subsequent reads drain any already-queued data and then return io.EOF,
// and subsequent writes return io.ErrClosedPipe.
//
// Any units still held back by latency (pending, unreleased) are discarded,
// not delivered: they model bytes still in flight on the wire, not bytes
// already in the peer's receive buffer, so M1's "already-buffered data
// still drains" guarantee is unaffected.
func (p *pipe) close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}
	p.closed = true
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	p.pending = nil
	p.broadcastLocked()
	return nil
}
