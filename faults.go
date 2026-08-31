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
// unit (M2-5): latency, loss, and partition no longer install independent
// hooks that happen to run in whatever order netchaos.DialContext called
// them — there is exactly one hook, and its order is fixed by this
// function's body:
//
//  1. Partition. A partitioned link drops everything, so nothing else is
//     evaluated and no draw happens at all — partition must stay
//     deterministic by nature, and drawing here would perturb the
//     loss/latency streams for every future unit on this direction.
//  2. Loss. A dropped unit is never delivered, so any latency it might
//     also have drawn is irrelevant to what the reader sees.
//  3. Latency. Applied to whatever survives loss.
//
// Draw discipline (part of the determinism contract, docs/04): a unit that
// clears the partition gate draws from every *configured* fault's stream
// unconditionally, even one that partition or an earlier fault in this
// list already decided to drop. A latency draw still happens for a unit
// loss just dropped. This keeps each configured fault's draw index equal
// to the unit index on that connection direction, independent of what any
// other configured fault decided — the property that makes a fault trace
// diffable. Changing this discipline later is a breaking change to the
// determinism contract, not a bug fix.
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

		if dropped {
			p.bufBytes -= len(data)
			if p.trace != nil {
				p.trace.record(faultEvent{dropped: true, drawn: drawn})
			}
			p.broadcastLocked()
			return
		}

		if !cfg.latencyEnabled {
			if p.trace != nil {
				p.trace.record(faultEvent{})
			}
			p.readable = append(p.readable, data)
			p.broadcastLocked()
			return
		}

		now := time.Now()
		releaseAt := now.Add(drawn)
		if n := len(p.pending); n > 0 {
			if prev := p.pending[n-1].releaseAt; releaseAt.Before(prev) {
				releaseAt = prev
			}
		}
		if p.trace != nil {
			p.trace.record(faultEvent{drawn: drawn, effective: releaseAt.Sub(now)})
		}
		p.pending = append(p.pending, pendingUnit{data: data, releaseAt: releaseAt})
		p.armLatencyForAppendLocked()
	}
}
