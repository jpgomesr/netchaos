package netchaos

import (
	"errors"
	"net"
	"os"
	"testing"
	"testing/synctest"
	"time"
)

// newLossTestNetwork mirrors newLatencyTestNetwork (latency_test.go) for
// WithPacketLoss.
func newLossTestNetwork(t *testing.T, rate float64) (n *Network, client, server net.Conn) {
	t.Helper()
	n = NewNetwork(WithPacketLoss(rate))
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

func TestLossDeterministic(t *testing.T) {
	trace := func() []faultEvent {
		n := NewNetwork(WithSeed(7), WithPacketLoss(0.4))
		l, err := n.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()

		accepted := make(chan net.Conn, 1)
		go func() {
			c, err := l.Accept()
			if err == nil {
				accepted <- c
			}
		}()

		client, err := n.Dial("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = client.Close() }()
		server := <-accepted
		defer func() { _ = server.Close() }()

		for i := 0; i < 200; i++ {
			if _, err := client.Write([]byte{byte(i)}); err != nil {
				t.Fatal(err)
			}
		}
		return client.(*conn).writePipe.trace.snapshot()
	}

	a, b := trace(), trace()
	if len(a) != len(b) {
		t.Fatalf("trace lengths differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("event %d differs across runs: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestLossRateConverges(t *testing.T) {
	const (
		rate = 0.3
		n    = 5000
	)
	_, client, _ := newLossTestNetwork(t, rate)

	for i := 0; i < n; i++ {
		if _, err := client.Write([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}

	trace := client.(*conn).writePipe.trace.snapshot()
	if len(trace) != n {
		t.Fatalf("trace recorded %d events, want %d", len(trace), n)
	}
	dropped := 0
	for _, e := range trace {
		if e.dropped {
			dropped++
		}
	}
	got := float64(dropped) / float64(n)
	const tolerance = 0.05
	if got < rate-tolerance || got > rate+tolerance {
		t.Fatalf("observed drop rate = %v, want within %v of %v", got, tolerance, rate)
	}
}

func TestLossZeroRate(t *testing.T) {
	_, client, server := newLossTestNetwork(t, 0.0)

	const n = 500
	go func() {
		for i := 0; i < n; i++ {
			if _, err := client.Write([]byte{byte(i)}); err != nil {
				return
			}
		}
	}()

	buf := make([]byte, 1)
	for i := 0; i < n; i++ {
		nr, err := server.Read(buf)
		if err != nil || nr != 1 || buf[0] != byte(i) {
			t.Fatalf("read %d = (%d, %v, byte=%d), want (1, nil, %d)", i, nr, err, buf[0], byte(i))
		}
	}
}

func TestLossFullRate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, client, server := newLossTestNetwork(t, 1.0)

		if err := server.SetReadDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}

		n, err := client.Write([]byte("gone"))
		if err != nil || n != 4 {
			t.Fatalf("Write of a fully-dropped unit = (%d, %v), want (4, nil): a drop must be silent to the writer", n, err)
		}

		buf := make([]byte, 4)
		nr, err := server.Read(buf)
		if nr != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("Read after a fully-dropped write = (%d, %v), want (0, os.ErrDeadlineExceeded)", nr, err)
		}
	})
}

// TestLossDropModelConcatenatesSurvivors asserts the decided drop model
// (M0-3: silent gap) directly: whichever writes the seeded RNG happens to
// drop, the surviving writes' payloads arrive concatenated in order, with
// no error, no length short-fall, and no marker indicating a gap occurred
// -- indistinguishable, from the reader's side, from those writes simply
// never having happened.
func TestLossDropModelConcatenatesSurvivors(t *testing.T) {
	_, client, server := newLossTestNetwork(t, 0.5)

	const n = 100
	payloads := make([][]byte, n)
	for i := range payloads {
		payloads[i] = []byte{byte('A' + i%26)}
	}

	for _, p := range payloads {
		nw, err := client.Write(p)
		if err != nil || nw != len(p) {
			t.Fatalf("Write(%q) = (%d, %v), want (%d, nil)", p, nw, err, len(p))
		}
	}

	trace := client.(*conn).writePipe.trace.snapshot()
	if len(trace) != n {
		t.Fatalf("trace recorded %d events, want %d", len(trace), n)
	}

	var want []byte
	dropped := 0
	for i, e := range trace {
		if e.dropped {
			dropped++
			continue
		}
		want = append(want, payloads[i]...)
	}
	if dropped == 0 || dropped == n {
		t.Skip("this seed happened to drop none or all of the writes; rerun or adjust the seed to exercise a mixed sequence")
	}

	got := make([]byte, len(want))
	if _, err := readFull(server, got); err != nil {
		t.Fatalf("reading survivors: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("reader got %q, want %q (concatenated survivors, no gap marker)", got, want)
	}
}

func readFull(c net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := c.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// TestRetrySucceedsUnderLoss was grown into TestScenarioRetryUnderPacketLoss
// (scenario_test.go, M3-4) and moved there -- see that file for the full
// M3-4 shape (fixed seed, run through the M3-3 reproducibility harness).

func TestLossIsolatedPerConnection(t *testing.T) {
	const seed = int64(55)
	n := NewNetwork(WithSeed(seed), WithPacketLoss(0.5))
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

	// conn2's stream should be exactly what direct derivation predicts,
	// unaffected by conn1 having already drawn from its own stream.
	want := deriveStream(seed, conn2.ordinal, sideDialer, kindLoss)
	for i := 0; i < 20; i++ {
		if _, err := conn1.Write([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ {
		got := conn2.writePipe.loss.bernoulli(0.5)
		w := want.bernoulli(0.5)
		if got != w {
			t.Fatalf("draw %d on conn2's loss stream diverged after unrelated writes on conn1: got %v, want %v", i, got, w)
		}
	}
}

// TestLossDoesNotWedgeWriterOnRepeatedDrops guards the bufBytes accounting
// trap: a dropped unit was admitted (bufBytes was incremented) but never
// reaches readable, so a drop must un-account it -- otherwise repeated
// drops permanently inflate bufBytes and eventually wedge the writer with
// false back-pressure that nothing will ever relieve.
func TestLossDoesNotWedgeWriterOnRepeatedDrops(t *testing.T) {
	client, server := newConnPairWithBound(&addr{"tcp", "client"}, &addr{"tcp", "server"}, 0, "tcp", 16)
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	installFaultPolicy(client.writePipe, faultPolicy{lossEnabled: true, lossRate: 1.0})

	done := make(chan error, 1)
	go func() {
		for i := 0; i < 1000; i++ {
			if _, err := client.Write([]byte("0123456789")); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("write failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("writes wedged: bufBytes was not released on drop")
	}
}
