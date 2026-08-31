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
// M7-8 (#53, candidate 3): real TCP hides duplication from the application
// -- the receiver's stack dedupes -- but netchaos already crossed that line
// deliberately for packet loss (M0-3: real TCP hides loss too, by
// retransmitting, and WithPacketLoss injects the silent gap anyway,
// precisely so a test can exercise what the application sees when the
// transport does not behave). Duplication is accepted on the same
// reasoning.
//
// The duplicate is delivered with the same timing decision as the
// original -- whatever WithLatency/WithBandwidth computed for the first
// copy applies to the second as well, rather than drawing an independent
// delay for it. Duplicating with a delay between copies is a delivery-
// timing model latency already owns, and is out of scope here.
//
// The unit of duplication is the Write call, matching the fault unit
// WithPacketLoss and WithLatency already use (M0-3): a dropped unit is
// never duplicated, but the decision whether to duplicate is still drawn
// for it -- see the draw discipline on installFaultPolicy (faults.go).
//
// A duplicated unit is a second, independent copy: it counts against the
// pipe's buffer bound like any other delivered bytes, and mutating one
// copy (e.g. WithCorruption, M7-9) never affects the other.
func WithDuplication(rate float64) Option {
	return func(c *networkConfig) {
		c.duplicateEnabled = true
		c.duplicateRate = rate
	}
}

// validateDuplicationRate panics, naming WithDuplication and the offending
// value, if rate is outside [0.0, 1.0] -- including NaN, which always
// compares false and so would otherwise pass any range check silently.
func validateDuplicationRate(rate float64) {
	if math.IsNaN(rate) || rate < 0 || rate > 1 {
		panic(fmt.Sprintf("netchaos: WithDuplication: rate must be in [0.0, 1.0], got %v", rate))
	}
}
