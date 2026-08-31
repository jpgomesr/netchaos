package netchaos

import (
	"fmt"
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
// SetLatency and SetPacketLoss (M7-4), a configured rate cannot be changed
// on a live connection -- #50 named only latency and packet loss for runtime
// mutation.
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
// throttled to bytesPerSecond, computed as whole seconds plus a remainder
// rather than size*time.Second/bytesPerSecond -- the naive form overflows
// int64 nanoseconds for a write past roughly 8.5 GiB, which a caller of
// conn.Write is free to attempt.
func serializationDelay(size, bytesPerSecond int) time.Duration {
	sec := size / bytesPerSecond
	rem := size % bytesPerSecond
	return time.Duration(sec)*time.Second + time.Duration(rem)*time.Second/time.Duration(bytesPerSecond)
}
