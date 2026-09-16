package netchaos

import (
	"fmt"
	"math"
	"math/bits"
	"time"
)

// WithBandwidth throttles delivery to the given rate, in bytes per second,
// applied globally to every connection direction the Network handles (the
// same scoping as WithLatency and WithPacketLoss -- see M0-2). bytesPerSecond
// must be positive; NewNetwork panics otherwise, naming WithBandwidth and the
// offending value. Not configuring WithBandwidth means unlimited bandwidth,
// the same distinction WithLatency and WithPacketLoss draw between "never
// given" and an explicit degenerate value.
//
// Unlike WithLatency and WithPacketLoss, the throttle is a deterministic
// function of a unit's size and the configured rate -- it draws nothing from
// any seeded stream, so it has no per-unit randomness to converge or
// reproduce, and enabling it can never perturb the loss or latency draw
// sequence on the same direction. See the package doc's fault-composition
// section for where this stage sits relative to the other three.
//
// The rate is applied per connection direction, not shared across a
// connection's two directions or across connections: a full-duplex conn gets
// the configured rate each way, and dialing many connections dials many
// independently-throttled links, mirroring how latency and loss are already
// evaluated per direction.
//
// This is a construction-time setting; there is no runtime setter. Unlike
// SetLatency, SetPacketLoss (M7-4), SetDuplication, and SetCorruption
// (M9-4), a configured rate cannot be changed on a live connection --
// bandwidth is the one fault kind whose live-setter case was split out and
// deferred (issue #85, M8-7 finding F6): unlike the other four, it draws
// nothing, so a setter's interaction with the pipe's serialization clock
// (pipe.busyUntil) needs its own design pass rather than an assumption that
// it mirrors SetLatency. See docs/06 § Explicitly out of scope for v1.
func WithBandwidth(bytesPerSecond int) Option {
	return func(c *networkConfig) {
		c.bandwidthEnabled = true
		c.bandwidthBPS = bytesPerSecond
	}
}

// validateBandwidthRate panics, naming WithBandwidth and the offending
// value, if bytesPerSecond is not positive.
func validateBandwidthRate(bytesPerSecond int) {
	if bytesPerSecond <= 0 {
		panic(fmt.Sprintf("netchaos: WithBandwidth: bytesPerSecond must be > 0, got %v", bytesPerSecond))
	}
}

// serializationDelay is how long it takes to put size bytes on a link
// throttled to bytesPerSecond, computed as size*time.Second/bytesPerSecond
// with a 128-bit intermediate product (bits.Mul64/bits.Div64) rather than
// plain int64 arithmetic or a sec/rem split -- both overflow int64
// nanoseconds under conditions a caller of conn.Write can reach:
// size*time.Second alone overflows past roughly 8.5 GiB, and a sec/rem split
// still overflows on its remainder term once bytesPerSecond itself exceeds
// roughly 9.22 GB/s and size (or size mod bytesPerSecond) is comparably
// large (issue #80). The 128-bit product is exact for any size and
// bytesPerSecond this function accepts (bytesPerSecond > 0, validated by
// validateBandwidthRate), so there is no precision lost the way a float64
// intermediate would lose.
//
// If the true result would exceed the largest representable time.Duration
// (~292 years), it clamps to that maximum instead of wrapping to a negative
// value -- a delay that large is never meaningfully "the right number of
// nanoseconds" for a test to assert on either way.
func serializationDelay(size, bytesPerSecond int) time.Duration {
	hi, lo := bits.Mul64(uint64(size), uint64(time.Second))
	divisor := uint64(bytesPerSecond)
	if hi >= divisor {
		// The quotient would not fit in 64 bits, which is already far past
		// any representable Duration -- clamp without calling bits.Div64,
		// which panics when the quotient overflows (hi >= y).
		return time.Duration(math.MaxInt64)
	}
	q, _ := bits.Div64(hi, lo, divisor)
	if q > math.MaxInt64 {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(q)
}
