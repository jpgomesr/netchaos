package netchaos

import (
	"crypto/sha256"
	"encoding/binary"
	"math/bits"
	"math/rand/v2"
	"time"
)

// faultKind separates the draw sequence used by one fault type from another
// on the same connection and direction (M0-4), so that enabling WithLatency
// on an existing test cannot shift that test's packet-loss sequence, and
// vice versa.
type faultKind uint8

const (
	kindLoss faultKind = iota
	kindLatency
)

// stream is a connection-direction-fault-kind-scoped random source (M0-4).
// It never exposes anything from math/rand/v2's convenience API (Float64,
// IntN, N, ...) directly: Go's compatibility promise covers the ChaCha8
// byte stream, not the implementation of those helpers, so every derived
// value netchaos hands to a fault is computed here, from raw Uint64 draws,
// to stay bit-identical across Go versions, GOOS, and GOARCH.
//
// A stream is not safe for concurrent use — it belongs to exactly one
// connection direction, and netchaos's delivery path never calls it from
// more than one goroutine at a time (writes to one pipe direction are
// serialized by the pipe's own mutex).
type stream struct {
	src *rand.ChaCha8
}

// deriveStream derives a fault stream from a Network's master seed and a
// connection's identity: (masterSeed, connectionOrdinal, direction,
// faultKind), per the determinism contract (M0-4). The encoding is a fixed
// number of fixed-width big-endian bytes — no map iteration order, pointer
// values, or int-width-dependent constructs — so the same inputs produce
// the same stream on every platform.
func deriveStream(masterSeed int64, ordinal uint64, side connSide, kind faultKind) *stream {
	var in [4 + 8 + 8 + 1 + 1]byte
	copy(in[0:4], "nc1\x00") // domain-separation tag, fixed across all derivations
	binary.BigEndian.PutUint64(in[4:12], uint64(masterSeed))
	binary.BigEndian.PutUint64(in[12:20], ordinal)
	in[20] = byte(side)
	in[21] = byte(kind)

	key := sha256.Sum256(in[:])
	return &stream{src: rand.NewChaCha8(key)}
}

// next returns the next raw 64-bit draw from the stream.
func (s *stream) next() uint64 {
	return s.src.Uint64()
}

// bernoulli reports whether a trial with the given success probability
// succeeds, consuming exactly one draw. rate is clamped to [0.0, 1.0] by
// the caller (option validation, M2-6); bernoulli itself treats rate<=0 as
// "never" and rate>=1 as "always" without drawing a value that could be
// misread near the boundary.
func (s *stream) bernoulli(rate float64) bool {
	draw := s.next()
	if rate <= 0 {
		return false
	}
	if rate >= 1 {
		return true
	}
	// Top 53 bits -> a uniform float64 in [0, 1), matching the precision of
	// float64's mantissa exactly, with no possibility of rounding up to 1.0.
	v := float64(draw>>11) / (1 << 53)
	return v < rate
}

// uniformDuration draws a duration uniformly from [min, max], inclusive,
// producing exactly one duration per unit regardless of whether min == max —
// the fixed case is not special-cased, so a fault kind's value index always
// tracks the unit index one-for-one (the draw discipline recorded in the
// determinism contract, M2-5).
//
// One duration is not necessarily one raw draw. A ranged draw goes through
// boundedUint64, whose rejection loop resamples when a value lands in the
// biased zone, so the stream can advance more than once for a single
// duration. (The fixed case is the exception rather than the rule: min==max
// gives span 1, for which the rejection threshold is 0 and the loop can
// never run — which is what TestUniformDurationFixedConsumesADraw pins.)
//
// The distinction does not reach determinism: each stream is scoped to one
// (ordinal, side, kind), so however many raw draws a rejection consumes, the
// sequence of durations is a deterministic function of that stream.
func (s *stream) uniformDuration(min, max time.Duration) time.Duration {
	span := uint64(max-min) + 1 // inclusive range; min==max gives span==1
	return min + time.Duration(s.boundedUint64(span))
}

// boundedUint64 returns a uniform value in [0, n), n > 0, using Lemire's
// multiply-shift method with rejection to avoid modulo bias. This is
// implemented directly, rather than via math/rand/v2's Uint64N, because
// Uint64N's exact output for a given stream state is not part of Go's
// compatibility promise.
func (s *stream) boundedUint64(n uint64) uint64 {
	hi, lo := bits.Mul64(s.next(), n)
	if lo < n {
		thresh := -n % n
		for lo < thresh {
			hi, lo = bits.Mul64(s.next(), n)
		}
	}
	return hi
}
