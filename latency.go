package netchaos

import "time"

// pendingUnit is a write payload admitted into a pipe but held back by
// latency (M2-2), not yet visible to readers.
type pendingUnit struct {
	data      []byte
	releaseAt time.Time
}

// WithLatency delays delivery of writes by a duration drawn uniformly from
// [min, max]. Passing an equal min and max applies fixed latency. Applies
// globally, to every connection the Network handles (M0-2).
//
// Latency delays delivery; it never reorders. Units on one connection
// direction are released in write order even when a later unit draws a
// shorter delay.
func WithLatency(min, max time.Duration) Option {
	return func(c *networkConfig) {
		c.latencyEnabled = true
		c.latencyMin = min
		c.latencyMax = max
	}
}

// installLatency replaces p's deliver function with one that holds each
// admitted unit for a duration drawn from p.latency (the pipe's own,
// per-direction stream — M0-4) before making it visible to readers.
//
// The single-timer-for-the-head design is what keeps delivery in write
// order: N independent timers, one per unit, would let a later write's
// shorter draw jump ahead of an earlier write's longer one. Instead, each
// unit's releaseAt is clamped to be no earlier than the previous unit's,
// so p.pending is release-ordered by construction, and only ever one timer
// — armed for the current head — is live at a time.
func installLatency(p *pipe, min, max time.Duration) {
	p.deliver = func(p *pipe, data []byte) {
		drawn := p.latency.uniformDuration(min, max)
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

// armLatencyTimerLocked (re)arms the single live latency timer for the
// current pending head, or clears it if pending is empty. Must be called
// with p.mu held.
func (p *pipe) armLatencyTimerLocked() {
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	if len(p.pending) == 0 {
		return
	}
	delay := time.Until(p.pending[0].releaseAt)
	if delay < 0 {
		delay = 0
	}
	p.timer = time.AfterFunc(delay, p.releaseDueLatency)
}

// releaseDueLatency moves every pending unit whose delay has elapsed onto
// readable, in write order, wakes any blocked reader, and re-arms for
// whatever remains. It is the time.AfterFunc callback armed by
// armLatencyTimerLocked, so it takes p.mu itself rather than assuming it.
func (p *pipe) releaseDueLatency() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return
	}

	now := time.Now()
	i := 0
	for i < len(p.pending) && !p.pending[i].releaseAt.After(now) {
		p.readable = append(p.readable, p.pending[i].data)
		i++
	}
	if i > 0 {
		p.pending = p.pending[i:]
		p.broadcastLocked()
	}
	p.armLatencyTimerLocked()
}
