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

// installLoss replaces p's deliver function with one that drops each
// admitted unit with probability rate, drawn from p.loss (the pipe's own,
// per-direction stream -- M0-4).
//
// NOTE: as with installLatency, this assigns p.deliver directly, so
// configuring both WithLatency and WithPacketLoss on the same Network
// currently means whichever fault netchaos.DialContext installs last wins
// -- the other's deliverFunc is silently replaced. Composing the two is
// M2-5's job (a single evaluator, not two independent hooks); until it
// lands, only one of the two faults takes effect when both are configured
// together.
func installLoss(p *pipe, rate float64) {
	p.deliver = func(p *pipe, data []byte) {
		if p.loss.bernoulli(rate) {
			// Admission already accounted these bytes against the bound;
			// since the unit never reaches readable, that accounting must
			// be undone here, or repeated drops would permanently inflate
			// bufBytes and eventually wedge the writer with back-pressure
			// nothing will ever relieve.
			p.bufBytes -= len(data)
			if p.trace != nil {
				p.trace.record(faultEvent{dropped: true})
			}
			// A blocked writer may have been waiting on the buffer space
			// this drop just freed.
			p.broadcastLocked()
			return
		}

		if p.trace != nil {
			p.trace.record(faultEvent{})
		}
		p.readable = append(p.readable, data)
		p.broadcastLocked()
	}
}
