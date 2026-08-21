package netchaos

// M3-1: bubble-compatibility verification.
//
// Every blocking point in netchaos parks on a channel receive (or a select
// over channel receives), never a mutex -- confirmed by review of pipe.go,
// listener.go, deadline.go, and netchaos.go's waitUnpartitioned. This file
// proves that by construction, not just by reading the source: each test
// below blocks a goroutine on one specific point and demonstrates the
// enclosing bubble reaches idle while it is blocked.
//
// Two failure modes matter here, and they are not the same:
//
//   - A goroutine parked on a sync.Mutex keeps the bubble non-idle forever,
//     so synctest.Wait() never returns and the test hangs to go test's
//     timeout. This is not a panic.
//   - A bubble where every goroutine is durably blocked with no pending
//     timer panics immediately with a deadlock diagnostic.
//
// Consequently "synctest.Wait() returned without panicking" is too weak an
// assertion on its own -- Wait can return either because the goroutine
// under test reached a durable block, or because it already finished.
// assertDurablyBlocked (below) is the pattern every test in this file uses
// to rule out both: it snapshots the blocked goroutine's result channel,
// then sleeps virtual time and asserts the clock advanced by exactly that
// much -- a mutex-parked goroutine would make that sleep cost real time
// instead. The sleep also supplies the pending timer that keeps the
// SUCCESS path itself off the deadlock panic: following synctest.Wait()
// with a bare channel receive and nothing else pending is exactly the
// all-durably-blocked-no-timer condition that panics.
//
// A durably blocked goroutine's result channel must be buffered, or the
// goroutine parks on the send instead of returning, and the "hasn't
// completed yet" check below is meaningless.
//
// Where the blocked call is itself waiting on a timer (a latency delivery
// or a deadline), the proof sleep must be strictly shorter than that timer:
// two timers expiring at the same virtual instant makes the outcome depend
// on same-instant ordering, which is not a property to rest a test on.
// Those tests sleep for half the configured duration, assert the call is
// still pending, then advance the remainder separately.
//
// Every synctest.Test closure below takes the synctest-provided *testing.T
// explicitly rather than closing over the outer one, per the convention at
// faults_test.go's TestAllFaultsDeterministic: helpers like dialNamedPair
// register t.Cleanup, and cleanup registered against the outer t would fire
// only after the whole test function returns -- by which point the bubble
// is long gone ("close of synctest channel from outside bubble").
//
// No package-level sync.WaitGroup exists in this package (verified by
// review of every .go file at the repository root); the synctest docs flag
// this as unsafe because such a WaitGroup cannot associate with any one
// bubble. There is no meaningful runtime assertion for "a declaration does
// not exist", so this is recorded here rather than as a test.
//
// netchaos.go's godoc already documents that a Network must be constructed
// inside the bubble that uses it -- every test in this file follows that
// requirement, and no rewrite is needed to satisfy the corresponding M3-1
// acceptance criterion.

import (
	"context"
	"net"
	"runtime"
	"testing"
	"testing/synctest"
	"time"
)

// assertDurablyBlocked calls synctest.Wait, asserts the goroutine feeding
// done has not completed, and asserts the bubble clock advances by exactly
// d -- the positive proof the bubble reached idle. See the file comment for
// why the sleep is required and not merely convenient, and for the
// same-instant-timer caveat that means d must be strictly less than any
// timer the blocked call is itself waiting on.
func assertDurablyBlocked[T any](t *testing.T, done <-chan T, d time.Duration) {
	t.Helper()

	synctest.Wait() // a mutex-parked goroutine hangs here instead of returning

	select {
	case v := <-done:
		t.Fatalf("blocked call returned early: %+v", v)
	default:
	}

	start := time.Now()
	time.Sleep(d)
	if got := time.Since(start); got != d {
		t.Fatalf("virtual clock advanced %v while blocked, want %v", got, d)
	}
}

// TestBubbleIdleOnBlockedRead covers conn.Read blocking on an empty pipe
// (conn.go's select over pipe.notify / read-deadline / closed).
func TestBubbleIdleOnBlockedRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, server := newTestConnPair()
		defer func() { _ = client.Close() }()
		defer func() { _ = server.Close() }()

		type readResult struct {
			n   int
			err error
		}
		done := make(chan readResult, 1)
		go func() {
			buf := make([]byte, 5)
			n, err := server.Read(buf)
			done <- readResult{n, err}
		}()

		assertDurablyBlocked(t, done, time.Hour)

		if _, err := client.Write([]byte("hello")); err != nil {
			t.Fatal(err)
		}
		r := <-done
		if r.err != nil || r.n != 5 {
			t.Fatalf("read = (%d, %v), want (5, nil)", r.n, r.err)
		}
	})
}

// TestBubbleIdleOnBlockedWrite covers conn.Write blocking on a full pipe
// (conn.go's select over pipe.notify / write-deadline / closed). This
// blocking point had no prior bubble test.
func TestBubbleIdleOnBlockedWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, server := newTestConnPairWithBound(8)
		defer func() { _ = client.Close() }()
		defer func() { _ = server.Close() }()

		if n, err := client.Write([]byte("12345678")); err != nil || n != 8 {
			t.Fatalf("fill write = (%d, %v), want (8, nil)", n, err)
		}

		type writeResult struct {
			n   int
			err error
		}
		done := make(chan writeResult, 1)
		go func() {
			n, err := client.Write([]byte("x"))
			done <- writeResult{n, err}
		}()

		assertDurablyBlocked(t, done, time.Hour)

		buf := make([]byte, 8)
		if _, err := server.Read(buf); err != nil {
			t.Fatal(err)
		}
		r := <-done
		if r.err != nil || r.n != 1 {
			t.Fatalf("write = (%d, %v), want (1, nil)", r.n, r.err)
		}
	})
}

// TestBubbleIdleOnBlockedAccept covers listener.Accept blocking on an empty
// accept queue (listener.go's select over incoming / closedCh).
func TestBubbleIdleOnBlockedAccept(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork()
		l, err := n.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()

		type acceptResult struct {
			c   net.Conn
			err error
		}
		done := make(chan acceptResult, 1)
		go func() {
			c, err := l.Accept()
			done <- acceptResult{c, err}
		}()

		assertDurablyBlocked(t, done, time.Hour)

		client, err := n.Dial("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = client.Close() }()

		r := <-done
		if r.err != nil || r.c == nil {
			t.Fatalf("Accept = (%v, %v), want a non-nil conn and nil error", r.c, r.err)
		}
	})
}

// TestBubbleIdleOnLatencyDelivery covers latency's delivery wait, which has
// no waiting goroutine at all: a unit sits in pipe.pending until the single
// live time.AfterFunc armed for it fires (latency.go). This blocking point
// had no prior bubble test.
//
// Shape differs from assertDurablyBlocked's callers: the reader is blocked
// on conn.Read, but the property under test is that the pending unit is
// still held back by the still-unfired latency timer, not merely that the
// reader hasn't returned. The proof sleep (30m) is deliberately half the
// configured latency (1h) so it never collides with the delivery timer's
// own expiry -- see the file comment.
func TestBubbleIdleOnLatencyDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork(WithLatency(time.Hour, time.Hour))
		client, server := dialNamedPair(t, n)

		if _, err := client.Write([]byte("hi")); err != nil {
			t.Fatal(err)
		}

		type readResult struct {
			n   int
			err error
		}
		done := make(chan readResult, 1)
		go func() {
			buf := make([]byte, 2)
			n, err := server.Read(buf)
			done <- readResult{n, err}
		}()

		assertDurablyBlocked(t, done, 30*time.Minute)

		select {
		case r := <-done:
			t.Fatalf("read returned before the full latency elapsed: %+v", r)
		default:
		}

		// Advance the remaining 30 minutes: the delivery timer fires, the
		// unit moves onto readable, and the blocked read completes.
		time.Sleep(30 * time.Minute)
		r := <-done
		if r.err != nil || r.n != 2 {
			t.Fatalf("read = (%d, %v), want (2, nil)", r.n, r.err)
		}
	})
}

// TestBubbleIdleOnDeadlineWait covers a read blocked with a deadline armed
// (deadline.go's generation channel plus its own time.AfterFunc). Shape
// differs from assertDurablyBlocked's callers for the same reason as
// TestBubbleIdleOnLatencyDelivery: the proof sleep must be strictly shorter
// than the deadline itself.
func TestBubbleIdleOnDeadlineWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, server := newTestConnPair()
		defer func() { _ = client.Close() }()
		defer func() { _ = server.Close() }()

		if err := server.SetReadDeadline(time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}

		type readResult struct {
			n   int
			err error
		}
		done := make(chan readResult, 1)
		go func() {
			n, err := server.Read(make([]byte, 4))
			done <- readResult{n, err}
		}()

		assertDurablyBlocked(t, done, 30*time.Minute)

		select {
		case r := <-done:
			t.Fatalf("read returned before the deadline elapsed: %+v", r)
		default:
		}

		time.Sleep(30 * time.Minute)
		r := <-done
		assertDeadlineExceeded(t, r.err)
	})
}

// TestBubbleIdleOnPartitionDialWait covers Network.waitUnpartitioned, the
// select a partition-targetable Dial blocks on (netchaos.go). A plain Dial
// against a partitioned peer never blocks here at all -- only a dialer that
// named itself via WithPeerName is partition-targetable -- so this test
// must use DialContext + WithPeerName, not Dial.
func TestBubbleIdleOnPartitionDialWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork(WithPartition("client", "server"))
		l, err := n.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()

		type dialResult struct {
			c   net.Conn
			err error
		}
		done := make(chan dialResult, 1)
		go func() {
			ctx := WithPeerName(context.Background(), "client")
			c, err := n.DialContext(ctx, "tcp", "server")
			done <- dialResult{c, err}
		}()

		assertDurablyBlocked(t, done, time.Hour)

		n.Heal("client", "server")
		r := <-done
		if r.err != nil {
			t.Fatalf("Dial after Heal = %v, want nil", r.err)
		}
		_ = r.c.Close()
	})
}

// TestCloseWithInFlightWorkInBubble is the primary evidence that a latency
// delivery timer does not outlive the conn that created it: a unit is
// admitted and held back by a 1h latency, never read, and both ends are
// closed immediately. synctest.Test fails this test if any bubble goroutine
// is still running when it returns, so simply completing is the assertion
// that pipe.close's timer.Stop (pipe.go) actually disarmed the pending
// AfterFunc rather than leaving it to fire later against a closed pipe.
func TestCloseWithInFlightWorkInBubble(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork(WithLatency(time.Hour, time.Hour))
		client, server := dialNamedPair(t, n)

		if _, err := client.Write([]byte("in flight")); err != nil {
			t.Fatal(err)
		}

		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
		if err := server.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

// runFullCycleInBubble drives dial -> write -> read -> close to completion
// on n, which must already be constructed inside the calling bubble.
func runFullCycleInBubble(t *testing.T, n *Network) {
	t.Helper()
	client, server := dialNamedPair(t, n)
	if _, err := client.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2)
	if _, err := server.Read(buf); err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	_ = server.Close()
}

// TestFullCycleInBubble drives a complete dial/write/read/close cycle to
// completion with no deadlock panic. The plain case is already covered by
// TestBubbleCleanExit (leak_test.go); the new value here is the two
// variants where a timer is still live partway through the cycle -- latency
// delivery must resolve via the bubble's auto-advancing virtual clock
// before the read returns, and a still-armed (but unfired) deadline timer
// must not prevent a clean Close.
func TestFullCycleInBubble(t *testing.T) {
	t.Run("plain", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			runFullCycleInBubble(t, NewNetwork())
		})
	})

	t.Run("latency pending mid-cycle", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			runFullCycleInBubble(t, NewNetwork(WithLatency(time.Minute, time.Minute)))
		})
	})

	t.Run("deadline armed at close", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			n := NewNetwork()
			client, server := dialNamedPair(t, n)

			if err := client.SetDeadline(time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if _, err := client.Write([]byte("hi")); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 2)
			if _, err := server.Read(buf); err != nil {
				t.Fatal(err)
			}
			// The read succeeded well before the 1h deadline, so Close must
			// stop a still-live deadline timer cleanly.
			_ = client.Close()
			_ = server.Close()
		})
	})
}

// TestNoGoroutinesOutliveBubble combines both timer types netchaos ever
// arms -- a pending latency delivery and an armed-but-unfired deadline --
// and closes both ends before either fires. Completing without
// synctest.Test's own "bubble goroutine still running" failure is the
// assertion.
func TestNoGoroutinesOutliveBubble(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork(WithLatency(time.Hour, time.Hour))
		client, server := dialNamedPair(t, n)

		if err := client.SetDeadline(time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Write([]byte("hi")); err != nil {
			t.Fatal(err)
		}

		_ = client.Close()
		_ = server.Close()
	})
}

// TestNoLatencyTimerLeaks is a backstop, not the real proof: time.AfterFunc
// allocates no goroutine until it fires, so runtime.NumGoroutine cannot
// observe a latency timer that was stopped before it ever ran.
// TestCloseWithInFlightWorkInBubble is what actually proves pipe.close
// disarms a pending latency timer; this test only catches the (different,
// grosser) failure of a timer being left running long enough to fire and
// leave its callback goroutine behind.
func TestNoLatencyTimerLeaks(t *testing.T) {
	before := runtime.NumGoroutine()

	n := NewNetwork(WithLatency(500*time.Millisecond, 500*time.Millisecond))
	client, server := dialNamedPair(t, n)

	if _, err := client.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	_ = server.Close()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if runtime.NumGoroutine() <= before {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine count = %d, want <= %d (baseline) after closing with a pending latency timer", runtime.NumGoroutine(), before)
		}
		runtime.Gosched()
		time.Sleep(10 * time.Millisecond)
	}
}
