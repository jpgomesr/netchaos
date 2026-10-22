package netchaos

import (
	"fmt"
	"math"
)

// WithDuplication admits a delivered Write unit a second time, with the
// given probability, in [0.0, 1.0]. Applies globally, to every connection
// direction the Network handles. A rate outside [0.0, 1.0], including NaN
// and +/-Inf, makes NewNetwork panic, naming WithDuplication and the
// offending rate.
//
// The duplicate is delivered with the same timing decision as the
// original -- whatever WithLatency/WithBandwidth computed for the first
// copy applies to the second as well, rather than drawing an independent
// delay for it. Duplicating with a delay between copies is a delivery-
// timing model latency already owns, and is out of scope here.
//
// The unit of duplication is the Write call. A dropped unit is never
// duplicated, but the decision whether to duplicate is still drawn for it.
//
// A duplicated unit is a second, independent copy: it counts against the
// pipe's buffer bound like any other delivered bytes, and mutating one
// copy (e.g. WithCorruption) never affects the other.
//
// This sets the value a Network starts with. Network.SetDuplication
// changes it mid-test, on connections that already exist as well as future
// ones -- the same live semantics SetLatency/SetPacketLoss already have.
func WithDuplication(rate float64) Option {
	return func(c *networkConfig) {
		c.duplicateEnabled = true
		c.duplicateRate = rate
	}
}

// validateDuplicationRate panics, naming caller and the offending value, if
// rate is outside [0.0, 1.0] -- including NaN, which always compares false
// and so would otherwise pass any range check silently. caller is the
// identifier the panic message names -- WithDuplication's own validation
// and SetDuplication (M9-4) share this function but must each be named for
// their own call, not each other's, matching the convention
// validateLatencyRange/validateLossRate already established (M8-2).
func validateDuplicationRate(caller string, rate float64) {
	if math.IsNaN(rate) || rate < 0 || rate > 1 {
		panic(fmt.Sprintf("netchaos: %s: rate must be in [0.0, 1.0], got %v", caller, rate))
	}
}
