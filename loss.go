package netchaos

// WithPacketLoss drops whole Write calls with the given probability, in
// [0.0, 1.0]. Applies globally, to every connection the Network handles
// (M0-2).
//
// The unit of loss is the Write call (M0-3): each write is delivered or
// dropped as a whole, decided by a Bernoulli trial drawn from the
// connection direction's own seeded stream (M0-4). A dropped write is a
// silent gap: it is discarded, the peer's Read never observes those bytes,
// and -- because io.Writer forbids a short count without a non-nil error --
// the call that issued the write still reports n = len(p), nil. This
// mirrors what a real socket does when a packet is lost downstream: the
// sender's kernel doesn't know either.
//
// Rate 0.0 drops nothing; rate 1.0 drops everything, which is
// behaviourally similar to (but distinct in intent, and in draw
// consumption, from) partitioning the same peer pair -- see
// Network.Partition.
func WithPacketLoss(rate float64) Option {
	return func(c *networkConfig) {
		c.lossEnabled = true
		c.lossRate = rate
	}
}
