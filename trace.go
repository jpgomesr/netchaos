package netchaos

import (
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

// Side identifies which end of a connection a FaultEvent belongs to. It
// mirrors connSide (conn.go) as an exported value.
//
// The two enums must agree value-for-value: the assertion below fails the
// build if either const block is ever reordered, rather than silently
// mislabeling every exported event's side — the same "exactly one place"
// caution addr.go's type comment carries for peer identity, applied here.
type Side int

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
// unit (M0-3's fault unit) on one direction of one connection, exported by
// Network.Trace (M7-10, issue #51). It is faultEvent plus the (Ordinal,
// Side) identity needed to attribute an event to a connection direction
// once it has left traceRecorder's per-direction scope.
//
// Ordinal and Side identify the connection direction this event belongs
// to: Ordinal is the connection's dial-order ordinal (the determinism
// contract, docs/04, already fixes this order), and Side is which end
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
//     any clamping against an already-pending unit ahead of it (M2-2). A
//     unit's total delay from Write to delivery is Serialization +
//     Effective.
//
// On a Dropped event, Delay/Serialization/Effective describe what was
// drawn or computed before loss discarded the unit, not a delivery that
// happened: Delay may be non-zero for a unit that was never sent, and
// Serialization and Effective are always zero, since loss short-circuits
// installFaultPolicy's evaluator before either is computed (faults.go).
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
// once per direction from DialContext, in dial order — which is also
// ordinal order, since the determinism contract already fixes that — so
// Network.Trace's (ordinal, side, seq) canonical order falls out of append
// order and needs no sort.
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
// connection n has ever dialed, in (Ordinal, Side, Seq) order — the same
// canonical order the reproducibility harness (M3-3, reproducibility_test.go)
// compares golden traces in. This closes the deferral M2-1 recorded when
// the trace was first built: always recorded, never exported, until M6-14
// decided the full trace (not counters) belonged in v0.2.0's scope.
//
// The returned slice is a copy: mutating it, or a later call to Trace,
// never affects the other. Reused from traceRecorder.snapshot, which
// already establishes this per direction (TestTraceSnapshotIsACopy);
// Trace does no second copy of its own beyond concatenating those.
//
// See FaultEvent's own godoc for what is and is not recorded — notably,
// Network.Reset produces no event, and a dial that never establishes
// allocates no pipes to trace at all.
func (n *Network) Trace() []FaultEvent {
	n.traceMu.Lock()
	defer n.traceMu.Unlock()

	var out []FaultEvent
	for _, h := range n.traces {
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
			})
		}
	}
	return out
}
