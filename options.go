package netchaos

import "time"

// defaultSeed is the seed NewNetwork uses when WithSeed is not given. See
// NewNetwork's godoc for why the default is fixed rather than random.
const defaultSeed = 1

// networkConfig accumulates the settings Options mutate before NewNetwork
// builds a Network from them. M2 adds latency/packet-loss/partition fields
// here alongside seed.
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
// derived from (wired up starting M2-1). The same seed, with the same order
// of Dial/Listen/Partition/Heal calls, always produces the same sequence of
// injected faults.
func WithSeed(seed int64) Option {
	return func(c *networkConfig) {
		c.seed = seed
	}
}
