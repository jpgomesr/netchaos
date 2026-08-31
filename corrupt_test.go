package netchaos

// M7-9: data corruption. Like WithDuplication (M7-8) and unlike
// WithBandwidth (M7-5), corruption is a per-unit Bernoulli decision and
// therefore does draw -- kindCorrupt has its own stream (rand.go), so this
// file has the same shape of tests loss_test.go does (TestLossDeterministic,
// TestLossRateConverges) rather than bandwidth_test.go's deterministic-delay
// shape.

import (
	"net"
	"testing"
)

// newCorruptTestNetwork mirrors newLossTestNetwork (loss_test.go) for
// WithCorruption.
func newCorruptTestNetwork(t *testing.T, rate float64) (n *Network, client, server net.Conn) {
	t.Helper()
	n = NewNetwork(WithCorruption(rate))
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := l.Accept()
		if err == nil {
			accepted <- c
		}
	}()

	client, err = n.Dial("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	server = <-accepted
	t.Cleanup(func() { _ = server.Close() })
	return n, client, server
}

// TestCorruptionAltersDeliveredBytes is the headline claim: at rate 1.0, a
// unit's delivered bytes differ from what was written, with the same
// length.
func TestCorruptionAltersDeliveredBytes(t *testing.T) {
	_, client, server := newCorruptTestNetwork(t, 1.0)

	payload := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if _, err := client.Write(payload); err != nil {
		t.Fatal(err)
	}

	got := make([]byte, len(payload))
	if _, err := readFull(server, got); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(payload) {
		t.Fatalf("delivered length = %d, want %d: corruption must never change length", len(got), len(payload))
	}
	if string(got) == string(payload) {
		t.Fatalf("delivered bytes = %v, unchanged from the written %v: rate 1.0 must corrupt", got, payload)
	}

	// Exactly one bit differs -- the corruption model is a single bit flip,
	// not a byte replacement.
	diffBits := 0
	for i := range got {
		diffBits += popcount(got[i] ^ payload[i])
	}
	if diffBits != 1 {
		t.Fatalf("bits differing between written and delivered = %d, want exactly 1", diffBits)
	}
}

func popcount(b byte) int {
	n := 0
	for b != 0 {
		n += int(b & 1)
		b >>= 1
	}
	return n
}

// TestCorruptionNeverAtZeroRate is rate 1.0's complement: rate 0.0 must
// never corrupt, so a written payload arrives unchanged.
func TestCorruptionNeverAtZeroRate(t *testing.T) {
	_, client, server := newCorruptTestNetwork(t, 0.0)

	payload := []byte("unchanged")
	if _, err := client.Write(payload); err != nil {
		t.Fatal(err)
	}

	got := make([]byte, len(payload))
	if _, err := readFull(server, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("got %q, want %q (rate 0.0 must never corrupt)", got, payload)
	}
}

// TestCorruptionOnZeroLengthWriteDoesNotPanic covers the edge the draw
// discipline creates: a zero-length write still draws the corruption
// decision unconditionally, but there is no byte to flip. corruptionSite
// must not be reached, or must not be asked to index into an empty slice.
func TestCorruptionOnZeroLengthWriteDoesNotPanic(t *testing.T) {
	_, client, server := newCorruptTestNetwork(t, 1.0)

	if _, err := client.Write(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("sentinel")); err != nil {
		t.Fatal(err)
	}

	got := make([]byte, len("sentinel"))
	if _, err := readFull(server, got); err != nil {
		t.Fatal(err)
	}
	// Only asserting no panic occurred and the connection is still usable;
	// the sentinel write's own corruption (if any) is not the point here.
}

// TestCorruptionDoesNotMutateCallerBuffer confirms the decided safety
// property directly (M7-9's scope note): conn.Write already copies the
// caller's buffer before the data reaches the pipe (conn.go), so mutating
// the delivered payload must never be visible in the slice the caller
// passed to Write.
func TestCorruptionDoesNotMutateCallerBuffer(t *testing.T) {
	_, client, server := newCorruptTestNetwork(t, 1.0)

	payload := []byte{0xFF, 0xFF, 0xFF, 0xFF}
	original := append([]byte(nil), payload...)

	if _, err := client.Write(payload); err != nil {
		t.Fatal(err)
	}

	got := make([]byte, len(payload))
	if _, err := readFull(server, got); err != nil {
		t.Fatal(err)
	}

	if string(payload) != string(original) {
		t.Fatalf("caller's buffer = %v after Write, want unchanged %v: corruption must mutate only the pipe's private copy", payload, original)
	}
}

// TestCorruptionDrawsUnconditionally asserts the draw discipline directly,
// mirroring TestDrawDisciplineStable (faults_test.go) and M7-8's
// TestDuplicationDrawsUnconditionally: a unit dropped by loss still draws
// corruption's coin flip. Both loss and corruption are configured at their
// extreme rates (1.0), so the outcome is pinned rather than probabilistic,
// and the trace records the corruption draw even though nothing was
// actually corrupted -- there was nothing left to corrupt once loss dropped
// the unit.
func TestCorruptionDrawsUnconditionally(t *testing.T) {
	n := NewNetwork(WithSeed(11), WithPacketLoss(1.0), WithCorruption(1.0))
	client, _ := dialNamedPair(t, n)

	if _, err := client.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}

	trace := client.(*conn).writePipe.trace.snapshot()
	if len(trace) != 1 {
		t.Fatalf("trace = %+v, want exactly one event", trace)
	}
	if !trace[0].dropped {
		t.Fatalf("trace[0].dropped = false, want true (rate 1.0 loss)")
	}
	if !trace[0].corrupted {
		t.Fatalf("trace[0].corrupted = false, want true: a dropped unit must still draw corruption's decision")
	}
}

// TestCorruptionIsolatedPerConnection mirrors TestLossIsolatedPerConnection
// (loss_test.go) and M7-8's TestDuplicationIsolatedPerConnection: one
// connection's corruption stream is unaffected by an unrelated connection's
// writes.
func TestCorruptionIsolatedPerConnection(t *testing.T) {
	const seed = int64(55)
	n := NewNetwork(WithSeed(seed), WithCorruption(0.5))
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	accepted := make(chan net.Conn, 2)
	go func() {
		for i := 0; i < 2; i++ {
			c, err := l.Accept()
			if err == nil {
				accepted <- c
			}
		}
	}()

	c1, err := n.Dial("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c1.Close() }()
	c2, err := n.Dial("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c2.Close() }()

	for i := 0; i < 2; i++ {
		s := <-accepted
		defer func() { _ = s.Close() }()
	}

	conn1, conn2 := c1.(*conn), c2.(*conn)

	want := deriveStream(seed, conn2.ordinal, sideDialer, kindCorrupt)
	for i := 0; i < 20; i++ {
		if _, err := conn1.Write([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ {
		got := conn2.writePipe.corrupt.bernoulli(0.5)
		w := want.bernoulli(0.5)
		if got != w {
			t.Fatalf("draw %d on conn2's corrupt stream diverged after unrelated writes on conn1: got %v, want %v", i, got, w)
		}
	}
}

func TestWithCorruptionPanicsOnInvalidRate(t *testing.T) {
	cases := []struct {
		name string
		opt  Option
		want []string
	}{
		{"rate below zero", WithCorruption(-0.1), []string{"WithCorruption", "-0.1"}},
		{"rate above one", WithCorruption(1.1), []string{"WithCorruption", "1.1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expectPanic(t, c.want, func() {
				NewNetwork(c.opt)
			})
		})
	}
}
