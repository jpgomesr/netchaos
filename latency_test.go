package netchaos

import (
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"testing/synctest"
	"time"
)

// newLatencyTestNetwork returns a Network with WithLatency(min, max) plus a
// listener at "server" whose Accept results are delivered on the returned
// channel, so tests don't need to repeat the dial/accept boilerplate.
func newLatencyTestNetwork(t *testing.T, min, max time.Duration) (n *Network, client, server net.Conn) {
	t.Helper()
	n = NewNetwork(WithLatency(min, max))
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

func TestLatencyFixed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const delay = 50 * time.Millisecond
		_, client, server := newLatencyTestNetwork(t, delay, delay)

		start := time.Now()
		if _, err := client.Write([]byte("hello")); err != nil {
			t.Fatal(err)
		}

		buf := make([]byte, 5)
		n, err := server.Read(buf)
		if err != nil || string(buf[:n]) != "hello" {
			t.Fatalf("server.Read = (%d, %q, %v), want (5, \"hello\", nil)", n, buf[:n], err)
		}

		if elapsed := time.Since(start); elapsed != delay {
			t.Fatalf("elapsed = %v, want exactly %v", elapsed, delay)
		}
	})
}

func TestLatencyRanged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		min, max := 10*time.Millisecond, 100*time.Millisecond
		_, client, server := newLatencyTestNetwork(t, min, max)

		start := time.Now()
		if _, err := client.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 1)
		if _, err := server.Read(buf); err != nil {
			t.Fatal(err)
		}

		elapsed := time.Since(start)
		if elapsed < min || elapsed > max {
			t.Fatalf("elapsed = %v, want within [%v, %v]", elapsed, min, max)
		}
	})
}

func TestLatencyDeterministic(t *testing.T) {
	// trace takes the bubble's own t: calling the outer test's t.Fatal from
	// inside a synctest bubble reports against the wrong test and skips
	// the bubble's own cleanup.
	trace := func(t *testing.T) []faultEvent {
		n := NewNetwork(WithSeed(42), WithLatency(10*time.Millisecond, 200*time.Millisecond))
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

		for i := 0; i < 10; i++ {
			if _, err := client.Write([]byte{byte(i)}); err != nil {
				t.Fatal(err)
			}
		}
		return client.(*conn).writePipe.trace.snapshot()
	}

	var a, b []faultEvent
	synctest.Test(t, func(t *testing.T) { a = trace(t) })
	synctest.Test(t, func(t *testing.T) { b = trace(t) })

	if len(a) != len(b) {
		t.Fatalf("trace lengths differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("event %d differs across runs: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestLatencyPreservesOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// A wide range makes an out-of-order release likely if latency were
		// implemented as N independent timers instead of a monotonic queue.
		_, client, server := newLatencyTestNetwork(t, time.Millisecond, 500*time.Millisecond)

		const n = 20
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
			if err != nil || nr != 1 {
				t.Fatalf("read %d: (%d, %v)", i, nr, err)
			}
			if buf[0] != byte(i) {
				t.Fatalf("read %d out of order: got %d, want %d", i, buf[0], i)
			}
		}
	})
}

func TestLatencyVsReadDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, client, server := newLatencyTestNetwork(t, 100*time.Millisecond, 100*time.Millisecond)

		if err := server.SetReadDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Write([]byte("late")); err != nil {
			t.Fatal(err)
		}

		buf := make([]byte, 4)
		_, err := server.Read(buf)
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("Read before latency elapses = %v, want errors.Is(os.ErrDeadlineExceeded)", err)
		}

		if err := server.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		n, err := server.Read(buf)
		if err != nil || string(buf[:n]) != "late" {
			t.Fatalf("Read after latency elapses = (%d, %q, %v), want (4, \"late\", nil)", n, buf[:n], err)
		}
	})
}

func TestLatencyCloseInFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const delay = 10 * time.Millisecond
		_, client, server := newLatencyTestNetwork(t, delay, delay)

		if _, err := client.Write([]byte("arrives")); err != nil {
			t.Fatal(err)
		}
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}

		start := time.Now()
		buf := make([]byte, 32)
		n, err := server.Read(buf)
		if err != nil || string(buf[:n]) != "arrives" || time.Since(start) != delay {
			t.Fatalf("Read after peer close = (%q, %v) at %v, want arrives at %v", buf[:n], err, time.Since(start), delay)
		}
		n, err = server.Read(buf)
		if n != 0 || !errors.Is(err, io.EOF) || time.Since(start) != delay {
			t.Fatalf("Read after delivery = (%d, %v) at %v, want EOF at %v", n, err, time.Since(start), delay)
		}
	})
}

func TestNoLatencyByDefault(t *testing.T) {
	// Through a real Network, the path users take: every Network conn has
	// the fault evaluator installed, so checking a raw pipe's deliver
	// function (what this test used to do) proved nothing about it.
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork(WithSeed(42))
		client, server := dialNamedPair(t, n)

		start := time.Now()
		for i := 0; i < 10; i++ {
			if _, err := client.Write([]byte{byte(i)}); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 1)
			if _, err := io.ReadFull(server, buf); err != nil {
				t.Fatal(err)
			}
		}
		if elapsed := time.Since(start); elapsed != 0 {
			t.Fatalf("10 writes and reads with no WithLatency took %v of virtual time, want 0", elapsed)
		}

		for _, ev := range n.Trace() {
			if ev.Delay != 0 || ev.Effective != 0 {
				t.Fatalf("event %+v has a delay with no WithLatency configured", ev)
			}
		}
	})
}

// TestReleaseDueLatencyAfterCloseDeliversPending covers the delivery timer
// firing after a graceful close: the pending write precedes EOF.
func TestReleaseDueLatencyAfterCloseDeliversPending(t *testing.T) {
	p := newPipe(defaultPipeBound)
	p.mu.Lock()
	p.pending = append(p.pending, pendingUnit{data: []byte("late"), releaseAt: time.Now().Add(-time.Second)})
	p.mu.Unlock()
	if err := p.close(); err != nil {
		t.Fatal(err)
	}

	p.releaseDueLatency() // as if the timer fired just after close

	buf := make([]byte, 4)
	if n, err := p.read(buf); err != nil || n != 4 || string(buf) != "late" {
		t.Fatalf("read after timer = (%q, %v), want late", buf[:n], err)
	}
	if n, err := p.read(buf); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("read after draining = (%d, %v), want EOF", n, err)
	}
}

func TestReleaseDueLatencyAfterAbortDeliversNothing(t *testing.T) {
	p := newPipe(defaultPipeBound)
	p.pending = append(p.pending, pendingUnit{data: []byte("late"), releaseAt: time.Now().Add(-time.Second)})
	p.abort()
	p.releaseDueLatency()
	if len(p.readable) != 0 || len(p.pending) != 0 || p.timer != nil {
		t.Fatal("timer callback retained data after abort")
	}
}
