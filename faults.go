package netchaos

import "time"

// faultConfig is the set of fault settings that can change during a run:
// what SetLatency and SetPacketLoss write, and what the per-unit evaluator
// reads. Kept as one value so it can be copied out from under a lock in a
// single read rather than field by field, which would let a unit see half of
// one configuration and half of the next.
type faultConfig struct {
	lossEnabled bool
	lossRate    float64

	latencyEnabled         bool
	latencyMin, latencyMax time.Duration

	// bandwidthEnabled/bandwidthBPS is the throttle SetLatency/SetPacketLoss
	// have no counterpart for -- #50 named only latency and packet loss for
	// runtime mutation (M7-5), so this pair is set once, from
	// networkConfig, in NewNetwork, and never written again. It lives here
	// rather than in a construction-only struct so the evaluator below reads
	// it from the same single copy taken under fp.current()'s lock, with the
	// same nil-Network fallback to faultPolicy.static that loss and latency
	// already use.
	bandwidthEnabled bool
	bandwidthBPS     int

	// duplicateEnabled/duplicateRate is WithDuplication's per-unit
	// probability of admitting a delivered write a second time (M7-8, #53
	// candidate 3). There is no runtime setter, matching bandwidth: #50
	// named only latency and packet loss for M7-4's runtime-mutation
	// contract.
	duplicateEnabled bool
	duplicateRate    float64
}

// faultPolicy is the connection-direction-scoped fault configuration a
// pipe's deliver function evaluates against.
//
// It deliberately does not hold the configuration itself. Before M7-4 it
// did: the values were copied out of the Network at dial time, which made
// them un-changeable for the life of the connection. Now the values are read
// from network on every unit, which is what lets SetLatency/SetPacketLoss
// reach connections that already exist — the semantics M7-3 fixed in the
// determinism contract before this code was written.
//
// network may be nil, meaning "no Network": no partition tracking, and no
// live configuration to read. That case belongs to tests that construct a
// pipe directly, and static is the configuration they get. It is ignored
// whenever network is non-nil.
type faultPolicy struct {
	network *Network
	pair    pairKey

	static faultConfig
}

// current returns the fault configuration to evaluate this unit against.
func (fp faultPolicy) current() faultConfig {
	if fp.network == nil {
		return fp.static
	}
	return fp.network.faultConfig()
}

// installFaultPolicy replaces p's deliver function with the single composed
// evaluator for fp. This is the only place fault policy is evaluated per
// unit (M2-5): latency, loss, bandwidth, duplication, and partition no
// longer install independent hooks that happen to run in whatever order
// netchaos.DialContext called them — there is exactly one hook, and its
// order is fixed by this function's body:
//
//  1. Partition. A partitioned link drops everything, so nothing else is
//     evaluated and no draw happens at all — partition must stay
//     deterministic by nature, and drawing here would perturb the
//     loss/latency/duplicate streams for every future unit on this
//     direction.
//  2. Loss. A dropped unit is never delivered, so any latency it might
//     also have drawn is irrelevant to what the reader sees, and it never
//     reaches the link: a dropped unit costs no serialization time either
//     (step 3 below runs only for units that survive this step), and is
//     never duplicated (step 5 below).
//  3. Bandwidth (throttle). Delays delivery by however long the unit takes
//     to serialize onto a link of the configured rate, queued behind
//     whatever this direction is already transmitting (pipe.busyUntil).
//     Unlike loss and latency this is a deterministic function of the
//     unit's size and the configured rate — it draws nothing (see
//     WithBandwidth's godoc for why that is safe), so it has no ordering
//     constraint relative to loss/latency's draws and is placed here, after
//     loss decides whether there is anything to serialize.
//  4. Latency. Propagation delay, added on top of whatever step 3
//     produced.
//  5. Duplication. A unit that survives loss may be admitted a second
//     time, with the same releaseAt steps 3 and 4 already computed for the
//     first copy — the duplicate is the same physical unit delivered
//     twice, not an independently delayed one (WithDuplication's godoc).
//     Placed last because it is the only step that can turn one admitted
//     unit into two.
//
// Draw discipline (part of the determinism contract, docs/04): a unit that
// clears the partition gate draws from every *configured, drawing* fault's
// stream unconditionally, even one that partition or an earlier fault in
// this list already decided to drop. A latency draw still happens for a
// unit loss just dropped, and so does duplication's coin flip — recorded in
// the trace even though a dropped unit is never actually duplicated.
// Bandwidth is not part of this — it has no stream to draw from, so
// enabling it can never perturb the loss/latency/duplicate sequence. This
// keeps each drawing fault's draw index equal to the unit index on that
// connection direction, independent of what any other configured fault
// decided — the property that makes a fault trace diffable. Changing this
// discipline later is a breaking change to the determinism contract, not a
// bug fix.
func installFaultPolicy(p *pipe, fp faultPolicy) {
	p.deliver = func(p *pipe, data []byte) {
		if fp.network != nil && fp.network.isPartitioned(fp.pair) {
			p.bufBytes -= len(data)
			if p.trace != nil {
				p.trace.record(faultEvent{partitioned: true})
			}
			p.broadcastLocked()
			return
		}

		// Read once per unit, not once per field: a unit is evaluated against
		// one configuration, never a mix of the one before a setter call and
		// the one after it.
		cfg := fp.current()

		var dropped bool
		if cfg.lossEnabled {
			dropped = p.loss.bernoulli(cfg.lossRate)
		}

		var drawn time.Duration
		if cfg.latencyEnabled {
			drawn = p.latency.uniformDuration(cfg.latencyMin, cfg.latencyMax)
		}

		var duplicated bool
		if cfg.duplicateEnabled {
			duplicated = p.duplicate.bernoulli(cfg.duplicateRate)
		}

		if dropped {
			p.bufBytes -= len(data)
			if p.trace != nil {
				p.trace.record(faultEvent{dropped: true, drawn: drawn, duplicated: duplicated})
			}
			p.broadcastLocked()
			return
		}

		// base is when this unit finishes transmitting onto the (possibly
		// throttled) link -- "now" when bandwidth isn't configured, which is
		// what keeps effective's meaning below identical to what it was
		// before this fault kind existed.
		now := time.Now()
		base := now
		var serialized time.Duration
		if cfg.bandwidthEnabled {
			if p.busyUntil.After(base) {
				base = p.busyUntil
			}
			base = base.Add(serializationDelay(len(data), cfg.bandwidthBPS))
			p.busyUntil = base
			serialized = base.Sub(now)
		}

		if !cfg.latencyEnabled && !cfg.bandwidthEnabled {
			if p.trace != nil {
				p.trace.record(faultEvent{duplicated: duplicated})
			}
			p.readable = append(p.readable, data)
			if duplicated {
				dup := append([]byte(nil), data...)
				p.bufBytes += len(dup)
				p.readable = append(p.readable, dup)
			}
			p.broadcastLocked()
			return
		}

		releaseAt := base.Add(drawn)
		if n := len(p.pending); n > 0 {
			if prev := p.pending[n-1].releaseAt; releaseAt.Before(prev) {
				releaseAt = prev
			}
		}
		if p.trace != nil {
			p.trace.record(faultEvent{drawn: drawn, serialized: serialized, effective: releaseAt.Sub(base), duplicated: duplicated})
		}
		p.pending = append(p.pending, pendingUnit{data: data, releaseAt: releaseAt})
		p.armLatencyForAppendLocked()
		if duplicated {
			dup := append([]byte(nil), data...)
			p.bufBytes += len(dup)
			// Appended at the tail with the same releaseAt as the entry just
			// armed above: pending's release-ordering invariant (each entry's
			// releaseAt >= every earlier entry's) holds, and the timer already
			// armed for the head covers this entry too, so no re-arm is
			// needed here (armLatencyForAppendLocked only arms when the
			// append made pending's length exactly 1, which duplication's
			// second append never does).
			p.pending = append(p.pending, pendingUnit{data: dup, releaseAt: releaseAt})
		}
	}
}
