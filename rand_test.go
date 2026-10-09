package netchaos

import (
	"math/big"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestSeedReproducible(t *testing.T) {
	draw := func() []uint64 {
		s := deriveStream(42, 7, sideDialer, kindLoss)
		out := make([]uint64, 20)
		for i := range out {
			out[i] = s.next()
		}
		return out
	}

	a, b := draw(), draw()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("draw %d differs across derivations of the same stream: %d vs %d", i, a[i], b[i])
		}
	}
}

func TestOrdinalsIndependent(t *testing.T) {
	first := deriveStream(42, 1, sideDialer, kindLoss)
	second := deriveStream(42, 2, sideDialer, kindLoss)

	same := true
	for i := 0; i < 20; i++ {
		if first.next() != second.next() {
			same = false
			break
		}
	}
	if same {
		t.Fatalf("two different ordinals produced identical draw sequences from the same seed")
	}
}

func TestDirectionAndKindIndependent(t *testing.T) {
	base := deriveStream(42, 1, sideDialer, kindLoss)
	byDirection := deriveStream(42, 1, sideAcceptor, kindLoss)
	byKind := deriveStream(42, 1, sideDialer, kindLatency)

	if base.next() == byDirection.next() {
		t.Fatalf("flipping direction produced the same first draw")
	}
	base2 := deriveStream(42, 1, sideDialer, kindLoss)
	if base2.next() == byKind.next() {
		t.Fatalf("flipping fault kind produced the same first draw")
	}
}

func TestConcurrencyDoesNotPerturbStreams(t *testing.T) {
	// Through a real Network, with every drawing fault on: the same eight
	// connections write the same payloads once one connection at a time
	// and once from eight concurrent goroutines, and every draw-derived
	// field of every event must match. Deriving the streams directly (what
	// this test used to do) held by construction and could not catch a
	// shared RNG or a lock-order bug in the evaluator.
	const (
		seed        = int64(99)
		connections = 8
		writesEach  = 50
	)

	run := func(t *testing.T, concurrent bool) []FaultEvent {
		var events []FaultEvent
		synctest.Test(t, func(t *testing.T) {
			n := NewNetwork(
				WithSeed(seed),
				WithPacketLoss(0.5),
				WithLatency(time.Millisecond, 50*time.Millisecond),
				WithDuplication(0.2),
				WithCorruption(0.2),
			)
			l, err := n.Listen("tcp", "server")
			if err != nil {
				t.Fatal(err)
			}
			go func() {
				for {
					if _, err := l.Accept(); err != nil {
						return
					}
				}
			}()

			// Dialed one at a time, so ordinals are fixed across both runs.
			conns := make([]net.Conn, connections)
			for i := range conns {
				c, err := n.Dial("tcp", "server")
				if err != nil {
					t.Fatal(err)
				}
				conns[i] = c
			}

			write := func(c net.Conn) {
				for i := 0; i < writesEach; i++ {
					if _, err := c.Write(make([]byte, i%7+1)); err != nil {
						t.Error(err)
						return
					}
				}
			}
			if concurrent {
				var wg sync.WaitGroup
				for _, c := range conns {
					wg.Go(func() { write(c) })
				}
				wg.Wait()
			} else {
				for _, c := range conns {
					write(c)
				}
			}

			events = n.Trace()
			for _, c := range conns {
				_ = c.Close()
			}
			_ = l.Close()
		})
		return events
	}

	type drawn struct {
		ordinal                        uint64
		side                           Side
		seq                            uint64
		dropped, duplicated, corrupted bool
		delay                          time.Duration
		size, corruptedByte            int
		corruptBit                     uint8
	}
	project := func(evs []FaultEvent) []drawn {
		out := make([]drawn, len(evs))
		for i, ev := range evs {
			out[i] = drawn{ev.Ordinal, ev.Side, ev.Seq, ev.Dropped, ev.Duplicated, ev.Corrupted, ev.Delay, ev.Size, ev.CorruptedByte, ev.CorruptedBit}
		}
		return out
	}

	sequential := project(run(t, false))
	if len(sequential) != connections*writesEach {
		t.Fatalf("sequential run recorded %d events, want %d", len(sequential), connections*writesEach)
	}
	for round := 0; round < 20; round++ {
		concurrent := project(run(t, true))
		if !slices.Equal(concurrent, sequential) {
			for i := range sequential {
				if i >= len(concurrent) || concurrent[i] != sequential[i] {
					t.Fatalf("round %d: event %d differs under concurrent writes:\n concurrent %+v\n sequential %+v", round, i, concurrent[min(i, len(concurrent)-1)], sequential[i])
				}
			}
			t.Fatalf("round %d: concurrent run recorded %d events, sequential %d", round, len(concurrent), len(sequential))
		}
	}
}

func TestNoGlobalRandUsage(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}

	importRe := regexp.MustCompile(`"math/rand"(?:\s|$)`)
	callRe := regexp.MustCompile(`\brand\.([A-Za-z0-9_]+)\(`)

	for _, f := range files {
		if filepath.Ext(f) != ".go" || len(f) >= len("_test.go") && f[len(f)-len("_test.go"):] == "_test.go" {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if importRe.Match(data) {
			t.Errorf("%s imports math/rand (v1); netchaos must derive all randomness from per-connection streams", f)
		}
		for _, m := range callRe.FindAllSubmatch(data, -1) {
			fn := string(m[1])
			if fn != "NewChaCha8" {
				t.Errorf("%s calls rand.%s, a package-level math/rand/v2 function; only rand.NewChaCha8 (to seed a stream) is allowed outside stream methods", f, fn)
			}
		}
	}
}

func TestUniformDurationBounds(t *testing.T) {
	s := deriveStream(1, 0, sideDialer, kindLatency)
	min, max := 10*time.Millisecond, 50*time.Millisecond

	for i := 0; i < 2000; i++ {
		d := s.uniformDuration(min, max)
		if d < min || d > max {
			t.Fatalf("draw %d out of bounds: %v not in [%v, %v]", i, d, min, max)
		}
	}
}

func TestUniformDurationFixed(t *testing.T) {
	s := deriveStream(1, 0, sideDialer, kindLatency)
	fixed := 25 * time.Millisecond

	for i := 0; i < 50; i++ {
		if d := s.uniformDuration(fixed, fixed); d != fixed {
			t.Fatalf("draw %d with min==max = %v, want %v", i, d, fixed)
		}
	}
}

// TestUniformDurationFixedConsumesADraw asserts min==max still advances the
// stream, per the fixed draw discipline: a fault kind's draw index must
// track the unit index regardless of whether the draw happened to be fixed.
func TestUniformDurationFixedConsumesADraw(t *testing.T) {
	withFixedDraw := deriveStream(1, 0, sideDialer, kindLatency)
	_ = withFixedDraw.uniformDuration(5*time.Millisecond, 5*time.Millisecond)
	afterFixed := withFixedDraw.next()

	raw := deriveStream(1, 0, sideDialer, kindLatency)
	_ = raw.next() // the draw uniformDuration should have consumed
	afterRaw := raw.next()

	if afterFixed != afterRaw {
		t.Fatalf("uniformDuration(fixed, fixed) did not consume exactly one draw: next value = %d, want %d", afterFixed, afterRaw)
	}
}

// TestConnPairAttachesPerDirectionStreams asserts that each pipe making up
// a conn pair is attached, at creation, to the streams the determinism
// contract (M0-4) says it must draw from: derived from the pair's shared
// ordinal, the writing side, and the fault kind.
func TestConnPairAttachesPerDirectionStreams(t *testing.T) {
	const seed, ordinal = int64(7), uint64(3)
	client, server := newConnPairWithSeed(&addr{network: "tcp", peer: "client"}, &addr{network: "tcp", peer: "server"}, ordinal, "tcp", defaultPipeBound, seed)
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	cases := []struct {
		name string
		got  *stream
		side connSide
		kind faultKind
	}{
		{"client write pipe loss", client.writePipe.loss, sideDialer, kindLoss},
		{"client write pipe latency", client.writePipe.latency, sideDialer, kindLatency},
		{"server write pipe loss", server.writePipe.loss, sideAcceptor, kindLoss},
		{"server write pipe latency", server.writePipe.latency, sideAcceptor, kindLatency},
	}
	for _, c := range cases {
		if c.got == nil {
			t.Fatalf("%s: stream not attached", c.name)
		}
		want := deriveStream(seed, ordinal, c.side, c.kind)
		if c.got.next() != want.next() {
			t.Fatalf("%s: draw sequence does not match deriveStream(%d, %d, %v, %v)", c.name, seed, ordinal, c.side, c.kind)
		}
	}

	if client.writePipe.trace == nil || server.writePipe.trace == nil {
		t.Fatal("pipe trace recorder not attached")
	}
}

// TestDialAttachesNetworkSeed asserts that connections established through
// Network.Dial derive their streams from the Network's own seed, not a
// hardcoded default — the property that makes WithSeed meaningful.
func TestDialAttachesNetworkSeed(t *testing.T) {
	const seed = int64(12345)
	n := NewNetwork(WithSeed(seed))
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	go func() {
		c, err := l.Accept()
		if err == nil {
			_ = c.Close()
		}
	}()

	client, err := n.Dial("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	c := client.(*conn)
	want := deriveStream(seed, c.ordinal, sideDialer, kindLoss)
	if c.writePipe.loss.next() != want.next() {
		t.Fatalf("dialed conn's loss stream does not match deriveStream with the Network's configured seed")
	}
}

// TestDeriveStreamGoldenVector pins the exact draw sequence for a fixed
// (masterSeed, ordinal, direction, kind) tuple. deriveStream's own
// correctness doesn't depend on these particular numbers, but a change to
// the derivation encoding or the underlying generator that silently altered
// them would break the determinism contract's "across machines" guarantee
// for every test written against a real seed — this is what would catch
// that, since none of the other tests compare against a value computed
// outside the package under test.
func TestDeriveStreamGoldenVector(t *testing.T) {
	want := []uint64{
		5692967259353408272,
		11389518709791257957,
		10515866004196439246,
		16007143287290652423,
		10207277178793558162,
	}

	s := deriveStream(42, 0, sideDialer, kindLoss)
	for i, w := range want {
		if got := s.next(); got != w {
			t.Fatalf("draw %d = %d, want %d (golden vector regression — did the derivation encoding or generator change?)", i, got, w)
		}
	}
}

func TestBernoulliBoundaries(t *testing.T) {
	s := deriveStream(1, 0, sideDialer, kindLoss)
	for i := 0; i < 1000; i++ {
		if s.bernoulli(0.0) {
			t.Fatalf("bernoulli(0.0) returned true on draw %d", i)
		}
	}
	for i := 0; i < 1000; i++ {
		if !s.bernoulli(1.0) {
			t.Fatalf("bernoulli(1.0) returned false on draw %d", i)
		}
	}
}

// referenceBounded is an independent implementation of Lemire's
// multiply-shift bounded draw, written with math/big so it shares no
// arithmetic with boundedUint64: take a raw 64-bit draw r, form the
// 128-bit product r*n, accept when its low 64 bits are at least 2^64 mod n,
// and return the high 64 bits; otherwise draw again. It returns the value
// and how many raw draws it consumed.
func referenceBounded(next func() uint64, n uint64) (uint64, int) {
	two64 := new(big.Int).Lsh(big.NewInt(1), 64)
	bn := new(big.Int).SetUint64(n)
	thresh := new(big.Int).Mod(two64, bn)
	for draws := 1; ; draws++ {
		prod := new(big.Int).Mul(new(big.Int).SetUint64(next()), bn)
		lo := new(big.Int).Mod(prod, two64)
		if lo.Cmp(thresh) >= 0 {
			return new(big.Int).Rsh(prod, 64).Uint64(), draws
		}
	}
}

// TestBoundedUint64RejectionMatchesReference covers boundedUint64's
// rejection loop, which no other test reaches: it only runs when a raw
// draw's low product bits fall below 2^64 mod n, which for the small n
// netchaos normally uses (latency spans, byte and bit indices) almost
// never happens. With n = 2^63+1 the rejection zone is nearly half the
// range, so a stream that rejects on its first draw is easy to find. The
// value boundedUint64 returns is compared against referenceBounded fed the
// same raw draws from an identically derived stream, so a change to the
// rejection logic -- which would shift every later draw on that stream and
// break cross-version reproducibility -- fails here.
func TestBoundedUint64RejectionMatchesReference(t *testing.T) {
	for _, n := range []uint64{1<<63 + 1, 1<<62 + 3, 3, 1000} {
		rejected := false
		for ordinal := uint64(0); ordinal < 200; ordinal++ {
			got := deriveStream(7, ordinal, sideDialer, kindLatency)
			raw := deriveStream(7, ordinal, sideDialer, kindLatency)
			for i := 0; i < 20; i++ {
				v := got.boundedUint64(n)
				want, draws := referenceBounded(raw.next, n)
				if v != want {
					t.Fatalf("n=%d ordinal=%d draw %d: boundedUint64 = %d, reference = %d", n, ordinal, i, v, want)
				}
				if v >= n {
					t.Fatalf("n=%d: boundedUint64 = %d, want < n", n, v)
				}
				if draws > 1 {
					rejected = true
				}
			}
		}
		// The two large n reject often enough that 4,000 draws without a
		// rejection would mean the loop is not being exercised at all.
		if n > 1<<61 && !rejected {
			t.Fatalf("n=%d: no draw hit the rejection loop; the test is not covering it", n)
		}
	}
}

// TestSideStringUnknown pins Side's fallback for a value outside the two
// defined sides, used only in test failure output.
func TestSideStringUnknown(t *testing.T) {
	if got := Side(99).String(); got != "unknown" {
		t.Fatalf("Side(99).String() = %q, want \"unknown\"", got)
	}
}
