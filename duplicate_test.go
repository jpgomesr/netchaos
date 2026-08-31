package netchaos

// M7-8: packet duplication. Unlike WithBandwidth (M7-5), duplication is a
// per-unit Bernoulli decision and therefore does draw -- kindDuplicate has
// its own stream (rand.go), so this file has the same shape of tests loss_test.go
// does (TestLossDeterministic, TestLossRateConverges) rather than
// bandwidth_test.go's deterministic-delay shape.

import (
	"net"
	"testing"
)

// newDuplicateTestNetwork mirrors newLossTestNetwork (loss_test.go) for
// WithDuplication.
func newDuplicateTestNetwork(t *testing.T, rate float64) (n *Network, client, server net.Conn) {
	t.Helper()
	n = NewNetwork(WithDuplication(rate))
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

// TestDuplicationDeliversUnitTwice is the headline claim: at rate 1.0, a
// single Write's bytes are readable twice, in order, with the same bytes --
// not merged into one longer read, which readFull's two separate calls
// below distinguish from a coalesced buffer of double length.
func TestDuplicationDeliversUnitTwice(t *testing.T) {
	_, client, server := newDuplicateTestNetwork(t, 1.0)

	payload := []byte("hello")
	if _, err := client.Write(payload); err != nil {
		t.Fatal(err)
	}

	first := make([]byte, len(payload))
	if _, err := readFull(server, first); err != nil {
		t.Fatalf("reading first copy: %v", err)
	}
	if string(first) != string(payload) {
		t.Fatalf("first copy = %q, want %q", first, payload)
	}

	second := make([]byte, len(payload))
	if _, err := readFull(server, second); err != nil {
		t.Fatalf("reading second copy: %v", err)
	}
	if string(second) != string(payload) {
		t.Fatalf("second copy = %q, want %q", second, payload)
	}
}

// TestDuplicationNeverAtZeroRate is the rate-1.0 test's complement: rate 0.0
// must never duplicate, so a single Write's bytes are readable exactly once.
func TestDuplicationNeverAtZeroRate(t *testing.T) {
	_, client, server := newDuplicateTestNetwork(t, 0.0)

	payload := []byte("once")
	if _, err := client.Write(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("sentinel")); err != nil {
		t.Fatal(err)
	}

	got := make([]byte, len(payload))
	if _, err := readFull(server, got); err != nil {
		t.Fatalf("reading: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("got %q, want %q (rate 0.0 must never duplicate, so this must be the first write's bytes, not a repeat)", got, payload)
	}
}

// TestDuplicationDrawsUnconditionally asserts the draw discipline directly,
// mirroring TestDrawDisciplineStable (faults_test.go): a unit dropped by
// loss still draws duplication's coin flip. Both loss and duplication are
// configured at their extreme rates (1.0), so the outcome is pinned rather
// than probabilistic, and the trace records the duplication draw even
// though nothing was actually duplicated -- there was nothing left to
// duplicate once loss dropped the unit.
func TestDuplicationDrawsUnconditionally(t *testing.T) {
	n := NewNetwork(WithSeed(11), WithPacketLoss(1.0), WithDuplication(1.0))
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
	if !trace[0].duplicated {
		t.Fatalf("trace[0].duplicated = false, want true: a dropped unit must still draw duplication's decision")
	}
}

// TestDuplicationIsolatedPerConnection mirrors TestLossIsolatedPerConnection
// (loss_test.go): one connection's duplicate stream is unaffected by an
// unrelated connection's writes.
func TestDuplicationIsolatedPerConnection(t *testing.T) {
	const seed = int64(55)
	n := NewNetwork(WithSeed(seed), WithDuplication(0.5))
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

	want := deriveStream(seed, conn2.ordinal, sideDialer, kindDuplicate)
	for i := 0; i < 20; i++ {
		if _, err := conn1.Write([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ {
		got := conn2.writePipe.duplicate.bernoulli(0.5)
		w := want.bernoulli(0.5)
		if got != w {
			t.Fatalf("draw %d on conn2's duplicate stream diverged after unrelated writes on conn1: got %v, want %v", i, got, w)
		}
	}
}

// TestDuplicationChargesBothCopies confirms the decided accounting rule
// (M7-8, decision 4): the duplicate counts against the pipe's buffer bound
// like any other delivered bytes, not for free. At a bound exactly matching
// two copies of the payload, a third write must block until something is
// read -- if the duplicate weren't charged, this write would fit and never
// block.
func TestDuplicationChargesBothCopies(t *testing.T) {
	const bound = 10 // exactly two 5-byte copies
	client, server := newConnPairWithBound(&addr{network: "tcp", peer: "client"}, &addr{network: "tcp", peer: "server"}, 0, "tcp", bound)
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	installFaultPolicy(client.writePipe, faultPolicy{static: faultConfig{duplicateEnabled: true, duplicateRate: 1.0}})

	payload := []byte("hello") // 5 bytes; two copies exactly fill the bound
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("first write = %v, want nil", err)
	}

	client.writePipe.mu.Lock()
	bufBytes := client.writePipe.bufBytes
	client.writePipe.mu.Unlock()
	if bufBytes != 2*len(payload) {
		t.Fatalf("bufBytes after a duplicated write = %d, want %d (both copies charged)", bufBytes, 2*len(payload))
	}

	_, ch, _ := client.writePipe.tryWrite([]byte("x"))
	if ch == nil {
		t.Fatal("tryWrite at a bound exactly filled by both copies did not report blocking; the duplicate was not charged")
	}
}

// TestDuplicateCopyIsIndependent confirms the two delivered copies do not
// share a backing array: mutating what the reader receives for the first
// copy must never affect the second, which matters once WithCorruption
// (M7-9) can mutate a delivered payload in place.
func TestDuplicateCopyIsIndependent(t *testing.T) {
	_, client, server := newDuplicateTestNetwork(t, 1.0)

	payload := []byte("hello")
	if _, err := client.Write(payload); err != nil {
		t.Fatal(err)
	}

	first := make([]byte, len(payload))
	if _, err := readFull(server, first); err != nil {
		t.Fatal(err)
	}
	first[0] = 'X' // mutate the reader's own copy of the first delivery

	second := make([]byte, len(payload))
	if _, err := readFull(server, second); err != nil {
		t.Fatal(err)
	}
	if string(second) != string(payload) {
		t.Fatalf("second copy = %q, want %q: mutating the first copy's read buffer must not affect the second", second, payload)
	}
}

func TestWithDuplicationPanicsOnInvalidRate(t *testing.T) {
	cases := []struct {
		name string
		opt  Option
		want []string
	}{
		{"rate below zero", WithDuplication(-0.1), []string{"WithDuplication", "-0.1"}},
		{"rate above one", WithDuplication(1.1), []string{"WithDuplication", "1.1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expectPanic(t, c.want, func() {
				NewNetwork(c.opt)
			})
		})
	}
}
