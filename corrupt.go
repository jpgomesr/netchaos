package netchaos

import (
	"fmt"
	"math"
)

// WithCorruption flips a single bit, chosen uniformly at random, in a
// delivered Write unit, with the given probability, in [0.0, 1.0]. Applies
// globally, to every connection direction the Network handles. A rate
// outside [0.0, 1.0], including NaN and +/-Inf, makes NewNetwork panic,
// naming WithCorruption and the offending rate.
//
// M7-9 (#53, candidate 4): real TCP hides corruption from the application --
// the receiver's checksum drops a corrupt segment -- but netchaos already
// crossed that line deliberately for packet loss (M0-3: real TCP hides loss
// too, by retransmitting, and WithPacketLoss injects the silent gap anyway,
// precisely so a test can exercise what the application sees when the
// transport does not behave). Corruption is accepted on the same reasoning,
// the same way WithDuplication (M7-8) was.
//
// The corrupted unit's length is unchanged -- only its content. Corrupting
// a length would model truncation, a different fault than this one; the
// fault unit stays the whole Write (M0-3).
//
// The unit of corruption is the Write call, matching the fault unit
// WithPacketLoss and WithLatency already use (M0-3): a dropped unit is
// never corrupted, but the decision whether to corrupt it, and (if so)
// which bit, is still drawn -- see the draw discipline on
// installFaultPolicy (faults.go). A zero-length write has no bit to flip,
// so the decision is drawn but nothing is mutated.
//
// The caller's original buffer is never mutated: conn.Write already copies
// it before the data reaches the pipe, so corruption mutates only that
// private copy.
func WithCorruption(rate float64) Option {
	return func(c *networkConfig) {
		c.corruptEnabled = true
		c.corruptRate = rate
	}
}

// validateCorruptionRate panics, naming WithCorruption and the offending
// value, if rate is outside [0.0, 1.0] -- including NaN, which always
// compares false and so would otherwise pass any range check silently.
func validateCorruptionRate(rate float64) {
	if math.IsNaN(rate) || rate < 0 || rate > 1 {
		panic(fmt.Sprintf("netchaos: WithCorruption: rate must be in [0.0, 1.0], got %v", rate))
	}
}
