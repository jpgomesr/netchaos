package netchaos

import (
	"fmt"
	"time"
)

// pendingUnit is a write payload admitted into a pipe but held back by
// latency, not yet visible to readers.
type pendingUnit struct {
	data      []byte
	releaseAt time.Time
}

// WithLatency delays delivery of writes by a duration drawn uniformly from
// [min, max]. Passing an equal min and max applies fixed latency. Applies
// globally, to every connection the Network handles. Both min and max must
// be non-negative, and min must not exceed max; NewNetwork panics
// otherwise, naming WithLatency and the offending value.
//
// Latency delays delivery; it never reorders. Units on one connection
// direction are released in write order even when a later unit draws a
// shorter delay.
//
// This sets the value a Network starts with. Network.SetLatency changes it
// mid-test, on connections that already exist as well as future ones.
//
// When packet loss or partition is also configured on the same connection
// direction, latency is evaluated last, after both — see the package doc's
// section on fault composition.
func WithLatency(min, max time.Duration) Option {
	return func(c *networkConfig) {
		c.latencyEnabled = true
		c.latencyMin = min
		c.latencyMax = max
	}
}

// validateLatencyRange panics, naming WithLatency and the offending value,
// if min or max is negative, or if min exceeds max.
func validateLatencyRange(min, max time.Duration) {
	if min < 0 {
		panic(fmt.Sprintf("netchaos: WithLatency: min must be >= 0, got %v", min))
	}
	if max < 0 {
		panic(fmt.Sprintf("netchaos: WithLatency: max must be >= 0, got %v", max))
	}
	if min > max {
		panic(fmt.Sprintf("netchaos: WithLatency: min (%v) must be <= max (%v)", min, max))
	}
}

// armLatencyForAppendLocked arms the latency timer for a unit just appended
// to pending. pending is release-ordered and appends go to the tail, so the
// appended unit is the head only when pending was empty beforehand — which
// is exactly when len(pending) is 1 afterwards. In every other case a live
// timer is already armed for the same, earlier deadline, and stopping and
// recreating it would target precisely what it already targets, at the cost
// of one *time.Timer per write (M6-8).
//
// This is deliberately a separate entry point from the one
// releaseDueLatency uses, rather than a guard inside armLatencyTimerLocked.
// There the head genuinely does change — units are removed from the front —
// so that re-arm must stay unconditional. Applying this guard to both call
// sites would leave the tail behind a drained head with no timer, and
// nothing would ever deliver it: silent data loss, not a slow path. Keep the
// two callers distinct.
//
// Must be called with p.mu held.
func (p *pipe) armLatencyForAppendLocked() {
	if len(p.pending) != 1 {
		return
	}
	p.armLatencyTimerLocked()
}

// armLatencyTimerLocked (re)arms the single live latency timer for the
// current pending head, or clears it if pending is empty. Callers that only
// appended to the tail should use armLatencyForAppendLocked instead. Must be
// called with p.mu held.
func (p *pipe) armLatencyTimerLocked() {
	p.arms++
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
