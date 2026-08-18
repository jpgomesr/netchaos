package netchaos

import "time"

// faultPolicy is the resolved, connection-direction-scoped fault
// configuration a pipe's deliver function evaluates against. network may
// be nil, meaning "no partition tracking" — used by tests that construct a
// pipe directly, without a Network.
type faultPolicy struct {
	network *Network
	pair    pairKey

	lossEnabled bool
	lossRate    float64

	latencyEnabled         bool
	latencyMin, latencyMax time.Duration
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

		var dropped bool
		if fp.lossEnabled {
			dropped = p.loss.bernoulli(fp.lossRate)
		}

		var drawn time.Duration
		if fp.latencyEnabled {
			drawn = p.latency.uniformDuration(fp.latencyMin, fp.latencyMax)
		}

		if dropped {
			p.bufBytes -= len(data)
			if p.trace != nil {
				p.trace.record(faultEvent{dropped: true, drawn: drawn})
			}
			p.broadcastLocked()
			return
		}

		if !fp.latencyEnabled {
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
		p.armLatencyTimerLocked()
	}
}
