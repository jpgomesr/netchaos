package netchaos

import (
	"cmp"
	"slices"
	"sync"
	"time"
)

// faultEvent is one fault-injection decision recorded for a single Write
// unit (M0-3's fault unit) on one direction of one connection.
type faultEvent struct {
	seq         uint64
	partitioned bool
	dropped     bool
	duplicated  bool          // whether a second copy of this unit was admitted (M7-8); recorded even when dropped is true, per the draw discipline
	corrupted   bool          // whether a bit was flipped in this unit's delivered bytes (M7-9); recorded even when dropped is true, per the draw discipline
	drawn       time.Duration // duration drawn from the latency stream; zero if latency isn't configured
	serialized  time.Duration // this unit's contribution to link-busy time under a throttle (M7-5); zero if bandwidth isn't configured
	effective   time.Duration // duration actually applied, relative to when serialization finished, after any clamping (M2-2)

	// size, corruptByte, and corruptBit are M9-3's addition (#78, payload
	// size and corruption site): the byte/bit index corruptionSite (rand.go)
	// already computed and installFaultPolicy (faults.go) previously
	// discarded after flipping the bit. size is len(data) at the point a
	// non-partitioned unit is first evaluated -- zero on a partitioned
	// event, matching every other field there (see FaultEvent's godoc for
	// the invariant this preserves). corruptByte/corruptBit are only
	// meaningful when corrupted is true and size > 0: a zero-length write
	// still draws the corruption decision (per the draw discipline) but has
	// no byte to flip, so corruptionSite is never called for it and both
	// stay zero.
	size        int
	corruptByte int
	corruptBit  uint8
}

// traceRecorder accumulates faultEvents for one connection direction, in
// write order. It is the artifact M3-3 compares across runs: two runs with
// the same seed and the same call order produce byte-identical traces.
//
// A traceRecorder is safe for concurrent use, though in practice writes to
// one connection direction are already serialized by that pipe's mutex.
type traceRecorder struct {
	mu     sync.Mutex
	events []faultEvent
}

// record appends e to the trace, assigning it the next sequence number.
func (r *traceRecorder) record(e faultEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e.seq = uint64(len(r.events))
	r.events = append(r.events, e)
}

// snapshot returns a copy of the events recorded so far, safe to inspect
// without racing further recordings or mutating the recorder's own state.
func (r *traceRecorder) snapshot() []faultEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]faultEvent, len(r.events))
	copy(out, r.events)
	return out
}

// Side identifies which end of a connection a FaultEvent belongs to.
type Side int

// The two Side values identify the dialer and acceptor ends.
const (
	SideDialer Side = iota
	SideAcceptor
)

// String reports "dialer" or "acceptor", used in test failure output.
func (s Side) String() string {
	switch s {
	case SideDialer:
		return "dialer"
	case SideAcceptor:
		return "acceptor"
	default:
		return "unknown"
	}
}

// Compile-time assertion that Side and connSide agree value-for-value: an
// array bound that is a nonzero constant, in either direction, fails to
// compile.
var (
	_ [SideDialer - Side(sideDialer)]byte
	_ [Side(sideDialer) - SideDialer]byte
	_ [SideAcceptor - Side(sideAcceptor)]byte
	_ [Side(sideAcceptor) - SideAcceptor]byte
)

// FaultEvent is one fault-injection decision recorded for a single Write
// on one direction of one connection, returned by Network.Trace.
// FaultEvent remains comparable: callers may compare two values with ==
// or use one as a map key. Future fields must preserve this property.
//
// Ordinal and Side identify the connection direction this event belongs
// to: Ordinal is the connection's dial-order ordinal, and Side is which end
// produced the event. Seq is this event's position within that direction's
// own trace, starting at 0.
//
// Partitioned, Dropped, Duplicated and Corrupted are independent, not
// mutually exclusive — the draw discipline records Duplicated and
// Corrupted even for a unit Dropped already discarded, so more than one
// can be true on the same event. Partitioned is never true alongside any
// other field: a partitioned unit draws nothing (the determinism
// contract's zero-draw exception), so every other field on that event is
// its zero value.
//
// Delay, Serialization and Effective are durations, and reading them
// correctly depends on which fault produced the event:
//
//   - Delay is the duration drawn from WithLatency's/SetLatency's stream.
//     Zero is ambiguous by itself: an explicit fixed-zero delay
//     (WithLatency(0, 0) or SetLatency(0, 0)) still draws, so Delay == 0
//     does not distinguish "latency not configured" from "configured,
//     drew zero" — the property this field exists to make observable in
//     the first place.
//   - Serialization is this unit's contribution to link-busy time under
//     WithBandwidth; zero whenever bandwidth isn't configured.
//   - Effective is the delay actually applied, measured from when
//     serialization finished (not from when the unit was written), after
//     any clamping against an already-pending unit ahead of it. A
//     delivered unit's total Write-to-delivery delay is Serialization +
//     Effective. Delay remains the latency draw alone.
//
// On a Dropped event, Delay/Serialization/Effective describe what was
// drawn or computed before loss discarded the unit, not a delivery that
// happened: Delay may be non-zero for a unit that was never sent, and
// Serialization and Effective are always zero, since a dropped unit is not
// transmitted or delivered.
//
// Size, CorruptedByte and CorruptedBit let a corruption or duplication
// failure be diagnosed, or an exact byte reproduced:
//
//   - Size is the unit's payload length, in bytes, at the point it was
//     evaluated. Always zero on a Partitioned event, since a partitioned
//     unit is discarded before its size is ever recorded (the same
//     zero-draw exception every other field there follows) -- but the
//     converse does not hold: a zero-length Write also reports Size == 0
//     on a non-Partitioned event, so Size == 0 alone never implies
//     Partitioned. Recorded on every event past the partition gate,
//     including a Dropped one, since a unit's size is known and meaningful
//     even when it was discarded.
//   - CorruptedByte and CorruptedBit are the byte index and bit index
//     (within that byte) selected for corruption, meaningful only when
//     Corrupted && Size > 0 && !Dropped. A dropped or zero-length unit has
//     no corruption site, even if Corrupted is true; both fields stay zero.
//
// Reproducibility: not every field reproduces under the same conditions.
// For a fixed seed and a fixed order of calls (see the package doc's
// determinism contract):
//
//   - Fields derived from a seeded draw reproduce anywhere, inside or
//     outside a testing/synctest bubble: Dropped, Duplicated, Corrupted,
//     Delay, Size, CorruptedByte and CorruptedBit, along with Ordinal,
//     Side, Seq and Partitioned.
//   - Fields derived from time reproduce only inside a synctest bubble,
//     where the clock is virtual: Serialization (the bandwidth clock counts
//     the real time elapsed between writes) and Effective (the clamp
//     against a unit already pending ahead of it depends on when that unit
//     was released). Outside a bubble, compare only the draw-derived fields.
type FaultEvent struct {
	Ordinal uint64
	Side    Side
	Seq     uint64

	Partitioned bool
	Dropped     bool
	Duplicated  bool
	Corrupted   bool

	Delay         time.Duration
	Serialization time.Duration
	Effective     time.Duration

	Size          int
	CorruptedByte int
	CorruptedBit  uint8
}

// traceHandle is what Network.Trace (M7-10) keeps for one connection
// direction: enough to reconstruct FaultEvent.Ordinal and FaultEvent.Side
// without retaining the *conn that produced the direction, or that conn's
// pipes' buffered data and deadline timers.
type traceHandle struct {
	ordinal uint64
	side    connSide
	rec     *traceRecorder
}

// registerTrace records rec as one connection direction's trace. Called
// once per direction from DialContext. Append order is NOT ordinal order
// in general: DialContext assigns the ordinal under n.mu and registers here
// later under traceMu, so concurrent dials can register out of ordinal
// order (#112). Network.Trace sorts by (ordinal, side) instead of relying
// on append order.
//
// Unlike registerReset (reset.go), an entry here is never removed: it is
// deliberately not pruned when its conn closes, since Network.Trace exists
// to be read after the connection that produced it typically already has
// been (the common defer c.Close() case) — pruning on Close would defeat
// the accessor's own purpose. See the Network type comment for what this
// means for a Network's own lifetime.
func (n *Network) registerTrace(ordinal uint64, side connSide, rec *traceRecorder) {
	n.traceMu.Lock()
	defer n.traceMu.Unlock()
	n.traces = append(n.traces, traceHandle{ordinal: ordinal, side: side, rec: rec})
}

// Trace returns every fault-injection decision recorded across every
// connection n has ever dialed, in (Ordinal, Side, Seq) order. The order
// holds even when connections are dialed concurrently.
//
// The returned slice is a copy: mutating it never changes retained events
// or the result of a later call to Trace.
//
// See FaultEvent's own godoc for what is and is not recorded — notably,
// Network.Reset produces no event, and a dial that never establishes
// allocates no pipes to trace at all.
// WithTrace(false) disables recorder allocation and event retention from
// construction; Trace then returns no events. WithTrace(true) and the
// default preserve the complete trace, including fault-free writes.
func (n *Network) Trace() []FaultEvent {
	if !n.traceEnabled {
		return nil
	}
	n.traceMu.Lock()
	defer n.traceMu.Unlock()

	// Concurrent dials can register out of ordinal order (#112), so sort a
	// copy by (ordinal, side); each direction's own events are already in
	// seq order. Stable keeps a dialer/acceptor pair's two handles in their
	// registration order, though side alone already distinguishes them.
	handles := slices.Clone(n.traces)
	slices.SortStableFunc(handles, func(a, b traceHandle) int {
		return cmp.Or(cmp.Compare(a.ordinal, b.ordinal), cmp.Compare(a.side, b.side))
	})

	var out []FaultEvent
	for _, h := range handles {
		for _, e := range h.rec.snapshot() {
			out = append(out, FaultEvent{
				Ordinal:       h.ordinal,
				Side:          Side(h.side),
				Seq:           e.seq,
				Partitioned:   e.partitioned,
				Dropped:       e.dropped,
				Duplicated:    e.duplicated,
				Corrupted:     e.corrupted,
				Delay:         e.drawn,
				Serialization: e.serialized,
				Effective:     e.effective,
				Size:          e.size,
				CorruptedByte: e.corruptByte,
				CorruptedBit:  e.corruptBit,
			})
		}
	}
	return out
}
