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
// The corrupted unit's length is unchanged; only its content changes.
//
// The unit of corruption is the Write call. A dropped unit is never
// mutated, though its corruption decision is still drawn. A zero-length
// write has no bit to flip, so the decision is drawn but nothing is mutated.
//
// The caller's original buffer is never mutated.
//
// This sets the value a Network starts with. Network.SetCorruption
// changes it mid-test, on connections that already exist as well as future
// ones -- the same live semantics SetLatency/SetPacketLoss already have.
func WithCorruption(rate float64) Option {
	return func(c *networkConfig) {
		c.corruptEnabled = true
		c.corruptRate = rate
	}
}

// validateCorruptionRate panics, naming caller and the offending value, if
// rate is outside [0.0, 1.0] -- including NaN, which always compares false
// and so would otherwise pass any range check silently. caller is the
// identifier the panic message names -- WithCorruption's own validation and
// SetCorruption (M9-4) share this function but must each be named for
// their own call, not each other's, matching the convention
// validateLatencyRange/validateLossRate already established (M8-2).
func validateCorruptionRate(caller string, rate float64) {
	if math.IsNaN(rate) || rate < 0 || rate > 1 {
		panic(fmt.Sprintf("netchaos: %s: rate must be in [0.0, 1.0], got %v", caller, rate))
	}
}
