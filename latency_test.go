package netchaos

import (
	"errors"
	"io"
	"net"
	"os"
	"reflect"
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
	trace := func() []faultEvent {
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
	synctest.Test(t, func(t *testing.T) { a = trace() })
	synctest.Test(t, func(t *testing.T) { b = trace() })

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
		_, client, server := newLatencyTestNetwork(t, time.Hour, time.Hour)

		if _, err := client.Write([]byte("never arrives")); err != nil {
			t.Fatal(err)
		}
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}

		buf := make([]byte, 32)
		n, err := server.Read(buf)
		if n != 0 || !errors.Is(err, io.EOF) {
			t.Fatalf("Read after peer close with an in-flight delayed write = (%d, %v), want (0, io.EOF): in-flight data is discarded on close, not delivered", n, err)
		}
	})
}

func TestNoLatencyByDefault(t *testing.T) {
	client, server := newTestConnPair()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	if reflect.ValueOf(client.writePipe.deliver).Pointer() != reflect.ValueOf(passThroughDeliver).Pointer() {
		t.Fatal("a pipe with no WithLatency configured must keep the M1 pass-through deliver function")
	}
}
