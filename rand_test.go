package netchaos

import (
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
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
	const (
		seed        = int64(99)
		connections = 8
		drawsEach   = 50
	)

	baseline := make([][]uint64, connections)
	for ord := 0; ord < connections; ord++ {
		s := deriveStream(seed, uint64(ord), sideDialer, kindLoss)
		draws := make([]uint64, drawsEach)
		for i := range draws {
			draws[i] = s.next()
		}
		baseline[ord] = draws
	}

	got := make([][]uint64, connections)
	var wg sync.WaitGroup
	wg.Add(connections)
	for ord := 0; ord < connections; ord++ {
		go func(ord int) {
			defer wg.Done()
			s := deriveStream(seed, uint64(ord), sideDialer, kindLoss)
			draws := make([]uint64, drawsEach)
			for i := range draws {
				draws[i] = s.next()
			}
			got[ord] = draws
		}(ord)
	}
	wg.Wait()

	for ord := 0; ord < connections; ord++ {
		for i := 0; i < drawsEach; i++ {
			if got[ord][i] != baseline[ord][i] {
				t.Fatalf("ordinal %d draw %d perturbed by concurrent access: got %d, want %d (baseline)", ord, i, got[ord][i], baseline[ord][i])
			}
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
