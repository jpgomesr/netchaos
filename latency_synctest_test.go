package netchaos

// M3-2: proves the value proposition docs/03-architecture.md names for
// composing with testing/synctest -- a test configured with real-feeling
// injected latency exercises a client's waiting behaviour without spending
// that latency in real wall-clock time.
//
// Two of the task's acceptance criteria are already fully covered by
// existing tests and are not duplicated here:
//
//   - "A round trip with WithLatency(d, d) advances the bubble clock by
//     exactly d" -- for a one-way delivery this is exactly what
//     TestLatencyFixed (latency_test.go) already asserts. A genuine round
//     trip crosses two directions and advances 2d instead, which is new:
//     see TestRoundTripAdvancesTwiceLatency below.
//   - "A conn read deadline shorter than the injected latency yields
//     os.ErrDeadlineExceeded" -- already covered end to end, including the
//     longer-deadline success case, by TestLatencyVsReadDeadline
//     (latency_test.go).

import (
	"context"
	"errors"
	"net"
	"testing"
	"testing/synctest"
	"time"
)

// TestLatencyCostsNoRealTime is the milestone's headline claim: 30s of
// configured virtual latency completes in negligible real time. The
// measurement must straddle the bubble boundary -- time.Since taken
// *inside* a bubble reports virtual duration and can never demonstrate
// real-time cheapness, since a 30s-virtual test would read exactly 30s
// either way. The bound is loose (want < 1s) because the claim under test
// is "virtual time is not real time," not a performance target; a
// microsecond bound would flake on a loaded CI runner without proving
// anything more.
func TestLatencyCostsNoRealTime(t *testing.T) {
	wallStart := time.Now()

	synctest.Test(t, func(t *testing.T) {
		const delay = 30 * time.Second
		_, client, server := newLatencyTestNetwork(t, delay, delay)

		if _, err := client.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 1)
		if _, err := server.Read(buf); err != nil {
			t.Fatal(err)
		}
	})

	if el := time.Since(wallStart); el > time.Second {
		t.Fatalf("30s of virtual latency cost %v of real time, want < 1s", el)
	}
}

// TestRoundTripAdvancesTwiceLatency is the round-trip reading of "advances
// the bubble clock by exactly d": latency is applied per direction
// (faults.go installs one evaluator per pipe), so a full round trip -- the
// client's write delayed on the way in, the server's reply delayed on the
// way back -- costs 2d, not d. Per-write equality like this only holds with
// one unit in flight per direction at a time: a second write whose draw
// would land before the previous unit's releaseAt gets clamped
// (faults.go's pending-queue clamp), so this test drains each direction
// before using it in the other.
func TestRoundTripAdvancesTwiceLatency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const delay = 40 * time.Millisecond
		_, client, server := newLatencyTestNetwork(t, delay, delay)

		start := time.Now()
		if _, err := client.Write([]byte("ping")); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 4)
		if n, err := server.Read(buf); err != nil || n != 4 || string(buf[:n]) != "ping" {
			t.Fatalf("server.Read = (%d, %q, %v), want (4, %q, nil)", n, buf[:n], err, "ping")
		}
		if _, err := server.Write([]byte("pong")); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Read(buf); err != nil {
			t.Fatal(err)
		}

		if elapsed := time.Since(start); elapsed != 2*delay {
			t.Fatalf("round trip elapsed = %v, want exactly %v (latency is per-direction: two crossings)", elapsed, 2*delay)
		}
	})
}

// readResult is shared by the attempt closures below.
type readResult struct {
	n   int
	err error
}

// TestContextTimeoutUnderLatency exercises the scenario docs/05 names for
// latency: a caller wraps a blocking call in context.WithTimeout, and a
// timeout shorter than the injected latency must cancel while a longer one
// must succeed, both with virtual elapsed time equal to the exact bound
// that governed the outcome.
//
// attempt's cancelled-branch return value must still be joined: the
// server.Read goroutine it starts is not cancelled by ctx expiring (conn.Read
// has no ctx parameter), so it stays running until the latency it's
// actually waiting on elapses. Not joining it would leave a goroutine
// running past this test's use of the pipe, racing a later attempt's read
// on the same direction.
func TestContextTimeoutUnderLatency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const latency = 100 * time.Millisecond
		_, client, server := newLatencyTestNetwork(t, latency, latency)

		attempt := func(timeout time.Duration) (elapsed time.Duration, join func() readResult, ctxErr error) {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			if _, err := client.Write([]byte("x")); err != nil {
				t.Fatal(err)
			}

			start := time.Now()
			done := make(chan readResult, 1)
			go func() {
				n, err := server.Read(make([]byte, 1))
				done <- readResult{n, err}
			}()

			select {
			case r := <-done:
				return time.Since(start), func() readResult { return r }, nil
			case <-ctx.Done():
				return time.Since(start), func() readResult { return <-done }, ctx.Err()
			}
		}

		// Timeout shorter than the latency: cancels, then the read still
		// completes on its own once the latency elapses.
		elapsed, join, ctxErr := attempt(50 * time.Millisecond)
		if !errors.Is(ctxErr, context.DeadlineExceeded) {
			t.Fatalf("ctxErr = %v, want context.DeadlineExceeded", ctxErr)
		}
		if elapsed != 50*time.Millisecond {
			t.Fatalf("elapsed = %v, want exactly 50ms (the timeout)", elapsed)
		}
		if r := join(); r.err != nil || r.n != 1 {
			t.Fatalf("cancelled attempt's read eventually = (%d, %v), want (1, nil): latency still delivers after the caller gives up", r.n, r.err)
		}

		// Timeout longer than the latency: succeeds, elapsed equals the
		// latency rather than the timeout.
		elapsed, join, ctxErr = attempt(200 * time.Millisecond)
		if ctxErr != nil {
			t.Fatalf("ctxErr = %v, want nil (timeout exceeds latency)", ctxErr)
		}
		if elapsed != latency {
			t.Fatalf("elapsed = %v, want exactly %v (the latency)", elapsed, latency)
		}
		if r := join(); r.err != nil || r.n != 1 {
			t.Fatalf("read = (%d, %v), want (1, nil)", r.n, r.err)
		}
	})
}

// TestMultipleLatenciesInOneBubble asserts several connections at different
// latencies interleave correctly in one bubble. WithLatency is global to a
// Network (M0-2's scoping decision), so differing per-connection latency
// needs separate Networks sharing one bubble, not one Network with
// per-connection configuration -- a real consequence of that scoping
// decision worth stating explicitly here.
func TestMultipleLatenciesInOneBubble(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		delays := []time.Duration{10 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond}

		type pair struct {
			client, server net.Conn
			delay          time.Duration
		}
		pairs := make([]pair, len(delays))
		for i, d := range delays {
			_, client, server := newLatencyTestNetwork(t, d, d)
			pairs[i] = pair{client, server, d}
		}

		start := time.Now()
		for _, p := range pairs {
			if _, err := p.client.Write([]byte("x")); err != nil {
				t.Fatal(err)
			}
		}

		// All three readers below have not yet been started; synctest.Wait
		// here only confirms the writes themselves didn't block. The real
		// per-pair idleness check happens as each Read call is issued.
		synctest.Wait()

		for _, p := range pairs {
			buf := make([]byte, 1)
			if _, err := p.server.Read(buf); err != nil {
				t.Fatal(err)
			}
			if elapsed := time.Since(start); elapsed != p.delay {
				t.Fatalf("connection at %v latency arrived at %v, want exactly %v", p.delay, elapsed, p.delay)
			}
		}
	})
}

// TestLatencyDoesNotWedgeSlowReader is the latency counterpart to
// TestLossDoesNotWedgeWriterOnRepeatedDrops (loss_test.go), which had no
// equivalent. It is the case where the pipe's 64 KiB bound and the pending
// queue actually interact: a unit held back by latency keeps its bufBytes
// accounting until it is released, so a reader slower than the injected
// delay is exactly the shape in which an accounting bug would wedge the
// writer permanently rather than merely slowing it.
//
// Run in a bubble so the delay costs no real time and so the assertion is a
// deterministic signal rather than a wall-clock timeout: synctest.Test fails
// the test if the writer is still blocked and nothing can advance, so
// reaching the end is the proof that no write wedged.
func TestLatencyDoesNotWedgeSlowReader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const (
			bound   = 1024
			chunk   = 256
			writes  = 40
			latency = 50 * time.Millisecond
		)

		client, server := newConnPairWithBound(&addr{"tcp", "client"}, &addr{"tcp", "server"}, 0, "tcp", bound)
		defer func() { _ = client.Close() }()
		defer func() { _ = server.Close() }()

		installFaultPolicy(client.writePipe, faultPolicy{
			latencyEnabled: true,
			latencyMin:     latency,
			latencyMax:     latency,
		})

		written := make(chan error, 1)
		go func() {
			payload := make([]byte, chunk)
			for i := 0; i < writes; i++ {
				if _, err := client.Write(payload); err != nil {
					written <- err
					return
				}
			}
			written <- nil
		}()

		// Read strictly slower than the writer produces, so the bound is
		// reached and the writer has to wait on released capacity. The sleep
		// comes *after* the read, deliberately: that way each Read blocks
		// until the latency timer releases a unit and broadcasts, so this
		// exercises the release path rather than letting a pre-read sleep
		// advance the clock and make the data already available.
		got := 0
		buf := make([]byte, chunk/2)
		for got < writes*chunk {
			n, err := server.Read(buf)
			if err != nil {
				t.Fatalf("read after %d of %d bytes: %v", got, writes*chunk, err)
			}
			got += n
			time.Sleep(latency * 2)
		}

		if err := <-written; err != nil {
			t.Fatalf("write: %v", err)
		}
		if got != writes*chunk {
			t.Fatalf("read %d bytes, want %d", got, writes*chunk)
		}

		// Everything delivered means everything was released, so no unit is
		// still holding accounting against the bound.
		client.writePipe.mu.Lock()
		leftover := client.writePipe.bufBytes
		stillPending := len(client.writePipe.pending)
		client.writePipe.mu.Unlock()
		if leftover != 0 || stillPending != 0 {
			t.Fatalf("after draining: bufBytes = %d, pending = %d, want 0 and 0", leftover, stillPending)
		}
	})
}
