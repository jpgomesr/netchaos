package netchaos

// M7-5: bandwidth throttling. Unlike latency and packet loss, the throttle
// delay (size / rate) is deterministic -- there is nothing to draw, so this
// file has no analogue of TestLossDeterministic or TestLossRateConverges.
// See TestBandwidthConsumesNoDraws for the property that matters instead:
// adding a throttle must not perturb the loss/latency draw sequence, since
// it draws nothing at all.

import (
	"net"
	"testing"
	"testing/synctest"
	"time"
)

// newBandwidthTestNetwork mirrors newLatencyTestNetwork (latency_test.go)
// and newLossTestNetwork (loss_test.go) for WithBandwidth.
func newBandwidthTestNetwork(t *testing.T, bytesPerSecond int) (n *Network, client, server net.Conn) {
	t.Helper()
	n = NewNetwork(WithBandwidth(bytesPerSecond))
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

// TestBandwidthDelaysProportionalToSize is the headline claim: a unit's
// delivery is delayed by size/rate, asserted in virtual time so the test
// costs no real wall-clock time.
func TestBandwidthDelaysProportionalToSize(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const bps = 1000 // 1000 bytes/sec
		_, client, server := newBandwidthTestNetwork(t, bps)

		payload := make([]byte, 500) // half a second of link time
		start := time.Now()
		if _, err := client.Write(payload); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, len(payload))
		if _, err := server.Read(buf); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed != 500*time.Millisecond {
			t.Fatalf("elapsed = %v, want exactly 500ms (500 bytes at 1000 B/s)", elapsed)
		}
	})
}

// TestBandwidthSerializesBackToBackWrites is the property that motivates a
// serialization clock instead of a flat per-unit delay: a second write
// issued before the first has finished transmitting queues behind it,
// rather than drawing its own delay independently of what the link is
// already busy doing. This is what makes sustained back-pressure observable
// (M7-6) once the link is slower than the reader.
func TestBandwidthSerializesBackToBackWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const bps = 1000
		_, client, server := newBandwidthTestNetwork(t, bps)

		start := time.Now()
		if _, err := client.Write(make([]byte, 500)); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Write(make([]byte, 500)); err != nil {
			t.Fatal(err)
		}

		buf := make([]byte, 500)
		if _, err := server.Read(buf); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed != 500*time.Millisecond {
			t.Fatalf("first unit arrived at %v, want exactly 500ms", elapsed)
		}
		if _, err := server.Read(buf); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed != time.Second {
			t.Fatalf("second unit arrived at %v, want exactly 1s: it must queue behind the first unit's serialization time, not draw its own independently", elapsed)
		}
	})
}

// TestBandwidthComposesAdditivelyWithLatency asserts the throttle's
// serialization delay and latency's propagation delay stack rather than one
// superseding the other -- "supersedes" would make WithLatency silently
// inert whenever a throttle is also configured.
func TestBandwidthComposesAdditivelyWithLatency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const (
			bps     = 1000
			latency = 200 * time.Millisecond
		)
		n := NewNetwork(WithBandwidth(bps), WithLatency(latency, latency))
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

		start := time.Now()
		if _, err := client.Write(make([]byte, 500)); err != nil { // 500ms serialization
			t.Fatal(err)
		}
		buf := make([]byte, 500)
		if _, err := server.Read(buf); err != nil {
			t.Fatal(err)
		}
		const want = 500*time.Millisecond + latency
		if elapsed := time.Since(start); elapsed != want {
			t.Fatalf("elapsed = %v, want exactly %v (serialization + propagation)", elapsed, want)
		}
	})
}

// TestWithBandwidthPanicsOnInvalidRate mirrors the non-positive-value
// panic every other Option enforces at NewNetwork time (M0-5).
func TestWithBandwidthPanicsOnInvalidRate(t *testing.T) {
	for _, c := range []struct {
		name string
		rate int
	}{
		{"zero", 0},
		{"negative", -1},
	} {
		t.Run(c.name, func(t *testing.T) {
			expectPanic(t, []string{"WithBandwidth"}, func() {
				NewNetwork(WithBandwidth(c.rate))
			})
		})
	}
}

// TestBandwidthOnALargeWriteDoesNotOverflow guards serializationDelay's
// overflow avoidance: size*time.Second/bytesPerSecond overflows int64
// nanoseconds past roughly 8.5 GiB, which a caller of conn.Write is free to
// attempt.
func TestBandwidthOnALargeWriteDoesNotOverflow(t *testing.T) {
	const (
		size = 10 << 30 // 10 GiB
		bps  = 1 << 20  // 1 MiB/s
	)
	got := serializationDelay(size, bps)
	want := 10 * 1024 * time.Second
	if got != want {
		t.Fatalf("serializationDelay(%d, %d) = %v, want %v", size, bps, got, want)
	}
	if got < 0 {
		t.Fatalf("serializationDelay overflowed to a negative duration: %v", got)
	}
}

// TestBandwidthConsumesNoDraws is the property the deterministic-throttle
// decision rests on: enabling WithBandwidth alongside WithPacketLoss and
// WithLatency must not shift either fault's draw sequence, since the
// throttle draws nothing. Verified by comparing every drawn/dropped value
// across two otherwise-identical scenarios, one with a throttle added.
func TestBandwidthConsumesNoDraws(t *testing.T) {
	const seed = int64(11)

	trace := func(withBandwidth bool) []faultEvent {
		opts := []Option{WithSeed(seed), WithPacketLoss(0.3), WithLatency(time.Millisecond, 20*time.Millisecond)}
		if withBandwidth {
			opts = append(opts, WithBandwidth(1<<20))
		}
		n := NewNetwork(opts...)
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

		for i := 0; i < 100; i++ {
			if _, err := client.Write([]byte{byte(i)}); err != nil {
				t.Fatal(err)
			}
		}
		return client.(*conn).writePipe.trace.snapshot()
	}

	without, with := trace(false), trace(true)
	if len(without) != len(with) {
		t.Fatalf("trace lengths differ: %d (no bandwidth) vs %d (bandwidth)", len(without), len(with))
	}
	for i := range without {
		if without[i].dropped != with[i].dropped || without[i].drawn != with[i].drawn {
			t.Fatalf("event %d: dropped/drawn diverged after enabling WithBandwidth: %+v vs %+v", i, without[i], with[i])
		}
	}
}
