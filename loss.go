package netchaos

import (
	"fmt"
	"math"
)

// WithPacketLoss drops whole Write calls with the given probability, in
// [0.0, 1.0]. Applies globally, to every connection the Network handles. A
// rate outside [0.0, 1.0], including NaN and +/-Inf, makes NewNetwork
// panic, naming WithPacketLoss and the offending rate.
//
// The unit of loss is the Write call: each write is delivered or dropped as
// a whole, decided by a Bernoulli trial drawn from the connection
// direction's own seeded stream. A dropped write is a silent gap: it is
// discarded, the peer's Read never observes those bytes, and -- because
// io.Writer forbids a short count without a non-nil error -- the call that
// issued the write still reports n = len(p), nil. This mirrors what a real
// socket does when a packet is lost downstream: the sender's kernel doesn't
// know either.
//
// Rate 0.0 drops nothing; rate 1.0 drops everything, which is
// behaviourally similar to (but distinct in intent, and in draw
// consumption, from) partitioning the same peer pair -- see
// Network.Partition.
//
// When partition is also configured on the same connection direction,
// packet loss is evaluated after partition and before latency -- see the
// package doc's section on fault composition.
func WithPacketLoss(rate float64) Option {
	return func(c *networkConfig) {
		c.lossEnabled = true
		c.lossRate = rate
	}
}

// validateLossRate panics, naming WithPacketLoss and the offending value,
// if rate is outside [0.0, 1.0] -- including NaN, which always compares
// false and so would otherwise pass any range check silently.
func validateLossRate(rate float64) {
	if math.IsNaN(rate) || rate < 0 || rate > 1 {
		panic(fmt.Sprintf("netchaos: WithPacketLoss: rate must be in [0.0, 1.0], got %v", rate))
	}
}
