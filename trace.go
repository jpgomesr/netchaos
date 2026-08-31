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
