package netchaos

import "time"

// defaultSeed is the seed NewNetwork uses when WithSeed is not given. See
// NewNetwork's godoc for why the default is fixed rather than random.
const defaultSeed = 1

// networkConfig accumulates the settings Options mutate before NewNetwork
// builds a Network from them.
type networkConfig struct {
	seed int64

	// latencyEnabled distinguishes "WithLatency was never given" (no timer,
	// no draw, byte-identical to M1's pass-through delivery) from
	// WithLatency(0, 0) (an explicit, if degenerate, fixed-zero delay).
	latencyEnabled         bool
	latencyMin, latencyMax time.Duration

	// lossEnabled distinguishes "WithPacketLoss was never given" from
	// WithPacketLoss(0.0) (an explicit, if degenerate, always-deliver
	// policy that still draws -- see WithPacketLoss's godoc).
	lossEnabled bool
	lossRate    float64

	// staticPartitions accumulates the raw peer-name pairs named by
	// WithPartition, kept unresolved (not yet a pairKey) until validate()
	// has checked them and NewNetwork applies them to Network.partitions.
	staticPartitions []partitionPair
}

// Option configures a Network at construction time. No Option or NewNetwork
// call returns an error: an Option given an invalid value (e.g. a
// WithPacketLoss rate outside [0,1]) makes NewNetwork panic, analogous to
// regexp.MustCompile.
//
// Validation runs once, after every Option passed to NewNetwork has been
// applied — not inside each Option's own closure. This means a later,
// valid Option of a given kind rescues an earlier, invalid one of the same
// kind: passing WithPacketLoss(-1) followed by WithPacketLoss(0.5) does not
// panic, because only the final packet-loss rate is checked.
type Option func(*networkConfig)

// validate panics, naming the offending option and value, if any option
// applied to c is invalid. It runs once, in NewNetwork, after every Option
// has been applied — so a later, valid option of a given kind rescues an
// earlier, invalid one of the same kind (WithPacketLoss(-1),
// WithPacketLoss(0.5) does not panic; only the final value is checked).
func (c *networkConfig) validate() {
	if c.lossEnabled {
		validateLossRate(c.lossRate)
	}
	if c.latencyEnabled {
		validateLatencyRange(c.latencyMin, c.latencyMax)
	}
	for _, p := range c.staticPartitions {
		validatePartitionPair(p)
	}
}

// WithSeed sets the seed a Network's per-connection random streams are
// derived from. If WithSeed is not given, NewNetwork uses the fixed default
// seed 1.
//
// The determinism guarantee: for a fixed seed and a fixed order in which
// Dial, Listen, Partition, Heal, SetLatency, and SetPacketLoss are called,
// every resulting connection produces an identical sequence of injected
// faults across runs and across machines. Each connection derives its own
// RNG stream from the seed, its establishment order, and its direction, so
// the guarantee holds regardless of how concurrently established
// connections do I/O afterward.
//
// The limit: the guarantee covers the order Network methods are called in,
// not wall-clock concurrency around them. Two cases, one fix. If two
// goroutines race to Dial, which one is assigned which connection ordinal —
// and therefore which RNG stream — is decided by the Go scheduler, not the
// seed. And if a test calls SetLatency or SetPacketLoss from one goroutine
// while another is writing, which unit is the first to see the new value is
// likewise the scheduler's choice, not the seed's: the contract fixes a
// setter's order against other Network calls, not against in-flight I/O.
// Sequence the calls a test depends on — dial before starting concurrent
// I/O, and write, then set, then write — rather than expecting netchaos to
// pick a boundary.
func WithSeed(seed int64) Option {
	return func(c *networkConfig) {
		c.seed = seed
	}
}
