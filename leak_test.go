package netchaos

import (
	"net"
	"testing"
	"testing/synctest"
	"time"
)

// TestNoGoroutineLeaks drives a full dial/accept/write/read/close cycle
// including a deadline, so deadline.go's time.AfterFunc-backed callback is
// actually exercised, and asserts that nothing netchaos started outlives the
// conns that own it. netchaos spawns no goroutine of its own except two
// time.AfterFunc callbacks (deadline.go and latency.go), each of which
// self-terminates once it fires or is stopped (see Network's godoc) — this
// test is the audit that backs that claim for the deadline timer.
// TestNoLatencyTimerLeaks (synctest_test.go) is the equivalent audit for
// the latency timer; TestCloseWithInFlightWorkInBubble (synctest_test.go)
// is the stronger, bubble-based proof that a still-pending latency timer
// never outlives its conn's Close.
//
// This replaces a runtime.NumGoroutine() baseline polled on a wall-clock
// loop for up to two seconds (M6-6). That version was the one genuinely
// flake-prone test in an otherwise deterministic suite: goroutine counts are
// noisy under parallel execution, and a leak test that flakes is worse than
// none, because it teaches people to re-run it.
//
// Two signals replace it, because neither is sufficient alone:
//
//  1. The bubble. synctest.Test fails if any goroutine started inside it is
//     still running when the function returns, which covers the Accept
//     goroutine and any blocked Read/Write — a stronger and fully
//     deterministic version of what the count was reaching for.
//  2. A direct check that Close disarmed the deadline timer. This is here
//     because the bubble provably does *not* cover it: an armed-but-unfired
//     time.AfterFunc owns no goroutine, so removing conn.Close's rd.stop()
//     leaves this test green if the bubble is the only assertion. The same
//     is true of the latency timer and TestCloseWithInFlightWorkInBubble —
//     see M6-18, which records that finding rather than fixing it here.
func TestNoGoroutineLeaks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork()
		l, err := n.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}

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
		server := <-accepted

		// An armed, never-fired deadline: Close must stop this timer rather
		// than leave it to outlive the conn.
		if err := client.SetDeadline(time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Write([]byte("hi")); err != nil {
			t.Fatal(err)
		}
		if _, err := server.Read(make([]byte, 2)); err != nil {
			t.Fatal(err)
		}

		_ = client.Close()
		_ = server.Close()
		_ = l.Close()

		assertDeadlineTimerDisarmed(t, client.(*conn))
	})
}

// TestBubbleCleanExit runs a full dial/accept/write/read/close scenario
// inside synctest.Test. synctest.Test itself fails the test if any bubble
// goroutine is still running (and not durably blocked) when the function
// passed to it returns, so simply completing this test is the assertion.
func TestBubbleCleanExit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork()
		l, err := n.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}

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
		server := <-accepted

		if _, err := client.Write([]byte("hi")); err != nil {
			t.Fatal(err)
		}
		if _, err := server.Read(make([]byte, 2)); err != nil {
			t.Fatal(err)
		}

		_ = client.Close()
		_ = server.Close()
		_ = l.Close()
	})
}

// assertDeadlineTimerDisarmed checks directly that both of c's deadline
// timers were stopped, which is the part of "nothing outlives the conn" that
// no goroutine-based signal can see: time.AfterFunc allocates no goroutine
// until it fires, so an armed timer is invisible both to
// runtime.NumGoroutine and to synctest's bubble-exit check.
func assertDeadlineTimerDisarmed(t *testing.T, c *conn) {
	t.Helper()
	for _, d := range []struct {
		name string
		d    *deadline
	}{{"read", c.rd}, {"write", c.wd}} {
		d.d.mu.Lock()
		live := d.d.timer != nil
		d.d.mu.Unlock()
		if live {
			t.Errorf("%s deadline timer still armed after Close, so it outlives the conn that owns it", d.name)
		}
	}
}
