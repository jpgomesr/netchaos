package netchaos

// M3-4: end-to-end scenario tests. Each scenario tests the exact reason
// docs/05-fault-injection.md names each fault as existing, using a small
// in-test client stub -- deliberately minimal, since the point is to
// exercise netchaos, not to ship a client library. Every scenario runs
// inside synctest.Test with a fixed seed and asserts on virtual time, and
// is also run through the M3-3 harness (runScenario/canonicalTrace) to
// confirm it reproduces (TestScenariosAreReproducible below).
//
// TestCircuitBreakerScenario (previously partition_test.go) and
// TestRetrySucceedsUnderLoss (previously loss_test.go) were grown into
// TestScenarioCircuitBreakerPartitionHeal and TestScenarioRetryUnderPacketLoss
// below and removed from their original files -- one home per scenario.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

// startEcho runs a trivial echo server on conn until it errors (typically
// on Close), shared by every scenario below that needs a round trip.
func startEcho(t *testing.T, conn net.Conn) {
	t.Helper()
	go func() {
		buf := make([]byte, 64)
		for {
			nr, err := conn.Read(buf)
			if err != nil {
				return
			}
			if _, err := conn.Write(buf[:nr]); err != nil {
				return
			}
		}
	}()
}

// --- 1. Retry under packet loss -------------------------------------------

// retryClient retries a request/response round trip up to budget times
// against a possibly-lossy conn -- the README's headline example.
type retryClient struct {
	budget int
}

// send writes payload and waits for it to be echoed back, retrying up to
// c.budget times. Returns the last error, wrapped, once the budget is
// exhausted.
func (c retryClient) send(conn net.Conn, payload []byte) error {
	var lastErr error
	for attempt := 0; attempt < c.budget; attempt++ {
		if _, err := conn.Write(payload); err != nil {
			return err
		}
		if err := conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
			return err
		}
		got := make([]byte, len(payload))
		if _, err := readFull(conn, got); err != nil {
			lastErr = err
			continue
		}
		if string(got) != string(payload) {
			lastErr = fmt.Errorf("echo mismatch: got %q, want %q", got, payload)
			continue
		}
		return nil
	}
	return fmt.Errorf("retry budget (%d) exhausted: %w", c.budget, lastErr)
}

// scenarioRetryUnderLoss dials a named pair against rate loss and drives
// retryClient with the given budget, failing the scenario (via t.Fatal) if
// the retry loop doesn't succeed.
func scenarioRetryUnderLoss(rate float64, budget int) scenario {
	return scenario{
		name: "retry-under-loss",
		fn: func(t *testing.T, seed int64) []net.Conn {
			n := NewNetwork(WithSeed(seed), WithPacketLoss(rate))
			client, server := dialNamedPair(t, n)
			startEcho(t, server)

			c := retryClient{budget: budget}
			if err := c.send(client, []byte("ping")); err != nil {
				t.Fatalf("retry under %v loss with budget %d: %v", rate, budget, err)
			}
			return []net.Conn{client, server}
		},
	}
}

// TestScenarioRetryUnderPacketLoss grows the moved TestRetrySucceedsUnderLoss:
// rate and seed are unchanged from that test (already empirically known to
// succeed well within budget), so no new guessing is introduced here.
func TestScenarioRetryUnderPacketLoss(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scenarioRetryUnderLoss(0.3, 100).fn(t, 1)
	})
}

// TestScenarioRetryExhaustsBudget uses rate 1.0, not a value below it: below
// 1.0 the exhaustion outcome is seed-dependent and brittle, while at 1.0
// every write is guaranteed dropped, so the budget's exhaustion -- and the
// specific error returned -- is guaranteed by construction rather than by
// probability.
func TestScenarioRetryExhaustsBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork(WithSeed(1), WithPacketLoss(1.0))
		client, server := dialNamedPair(t, n)
		startEcho(t, server)

		c := retryClient{budget: 5}
		err := c.send(client, []byte("ping"))
		if err == nil {
			t.Fatal("retry succeeded despite rate-1.0 packet loss, want the retry budget to exhaust")
		}
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("exhausted-budget error = %v, want it to wrap os.ErrDeadlineExceeded (every attempt should time out waiting for an echo that never arrives)", err)
		}
	})
}

// TestScenarioRetryUnderLossGolden gives the retry-under-loss scenario a
// checked-in golden trace, per M3-4's "at least one scenario also gets a
// golden file." Regenerate with:
//
//	go test -run TestScenarioRetryUnderLossGolden -update .
func TestScenarioRetryUnderLossGolden(t *testing.T) {
	sc := scenarioRetryUnderLoss(0.3, 100)
	const seed = 1

	var trace canonicalTrace
	synctest.Test(t, func(t *testing.T) {
		trace = runScenario(t, sc, seed)
	})

	path := filepath.Join("testdata", "traces", fmt.Sprintf("%s-seed%d.golden", sc.name, seed))
	if *updateGolden {
		if err := writeGolden(path, sc.name, seed, trace); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := readGolden(path)
	if err != nil {
		t.Fatalf("reading golden file %s: %v (run with -update to generate it)", path, err)
	}
	if !trace.equal(want) {
		t.Fatalf("trace does not match golden file %s:\n%s", path, trace.diff(want))
	}
}

// --- 2. Timeout and backoff under latency ---------------------------------

// backoffClient writes a single request, then retries the READ with a
// doubling deadline until the response arrives or its attempt budget is
// exhausted. This -- growing how long it's willing to wait for the same
// pending response, rather than re-sending a fresh request each attempt --
// is what "propagating a deadline through a slow simulated call" (docs/05)
// actually demonstrates: conn.Read has no ctx parameter, so the natural way
// netchaos-flavoured code propagates a context deadline onto a slow call is
// by deriving the conn's own read deadline from it (context.WithTimeout's
// Deadline() feeds SetReadDeadline directly). Re-sending a fresh request
// each attempt while an earlier one is still in flight would leave a stale
// echoed response sitting in the pipe for a LATER attempt's Read to
// mistakenly pick up -- retrying the read, not the write, avoids that
// class of bug entirely.
type backoffClient struct {
	initialTimeout time.Duration
	maxAttempts    int
}

// send returns the total elapsed time (virtual, inside a bubble) from the
// initial write to a successful read, and the final error if every attempt
// timed out waiting for it.
func (c backoffClient) send(conn net.Conn, payload []byte) (time.Duration, error) {
	start := time.Now()
	if _, err := conn.Write(payload); err != nil {
		return time.Since(start), err
	}

	got := make([]byte, len(payload))
	timeout := c.initialTimeout
	var lastErr error
	for attempt := 0; attempt < c.maxAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		deadline, _ := ctx.Deadline() // WithTimeout always sets one
		if err := conn.SetReadDeadline(deadline); err != nil {
			cancel()
			return time.Since(start), err
		}
		_, err := readFull(conn, got)
		cancel()
		if err == nil {
			return time.Since(start), nil
		}
		lastErr = err
		timeout *= 2
	}
	return time.Since(start), fmt.Errorf("exhausted %d attempts: %w", c.maxAttempts, lastErr)
}

func scenarioTimeoutBackoffUnderLatency(latency, initialTimeout time.Duration, maxAttempts int) scenario {
	return scenario{
		name: "timeout-backoff-under-latency",
		fn: func(t *testing.T, seed int64) []net.Conn {
			n := NewNetwork(WithSeed(seed), WithLatency(latency, latency))
			client, server := dialNamedPair(t, n)
			startEcho(t, server)

			c := backoffClient{initialTimeout: initialTimeout, maxAttempts: maxAttempts}
			if _, err := c.send(client, []byte("ping")); err != nil {
				t.Fatalf("timeout/backoff client: %v", err)
			}
			return []net.Conn{client, server}
		},
	}
}

// TestScenarioTimeoutAndBackoffUnderLatency picks timeouts/latency so the
// exact elapsed time is computable in advance and asserted as an equality,
// per the bubble clock being exact. A round trip costs 2x the configured
// latency (one crossing per direction, per M3-2's
// TestRoundTripAdvancesTwiceLatency): at latency=150ms a round trip
// completes at virtual T=300ms from the initial write. Since backoffClient
// never re-writes, elapsed on success is exactly that arrival time
// regardless of how many deadline resets happened along the way — as long
// as none of them lands exactly on T=300ms itself (a same-instant
// collision between the read deadline's timer and the delivery timer,
// which is why initialTimeout is 120ms rather than a value like 150ms that
// would double onto 300 exactly). With initialTimeout=120ms over 2
// attempts: attempt 0's deadline (120ms < 300ms round trip) fires first,
// so it times out; attempt 1 re-arms the deadline for 240ms further out
// (absolute T=360ms), comfortably past T=300ms, so the read succeeds when
// the response actually arrives at T=300ms.
func TestScenarioTimeoutAndBackoffUnderLatency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const latency = 150 * time.Millisecond
		n := NewNetwork(WithSeed(1), WithLatency(latency, latency))
		client, server := dialNamedPair(t, n)
		startEcho(t, server)

		c := backoffClient{initialTimeout: 120 * time.Millisecond, maxAttempts: 2}
		elapsed, err := c.send(client, []byte("ping"))
		if err != nil {
			t.Fatalf("timeout/backoff client: %v", err)
		}
		if want := 2 * latency; elapsed != want {
			t.Fatalf("elapsed = %v, want exactly %v (the round trip: one latency crossing per direction)", elapsed, want)
		}
	})
}

// --- 3. Circuit breaker across partition and heal -------------------------

type breakerState int

const (
	breakerClosed breakerState = iota
	breakerOpen
	breakerHalfOpen
)

// breaker is a minimal circuit breaker: threshold consecutive failures
// opens it; probe attempts a single call regardless of open state to test
// whether the link has recovered, moving to half-open for the attempt.
type breaker struct {
	threshold int
	state     breakerState
	fails     int
}

func (b *breaker) call(f func() error) error {
	if b.state == breakerOpen {
		return errors.New("circuit breaker open")
	}
	if err := f(); err != nil {
		b.fails++
		if b.fails >= b.threshold {
			b.state = breakerOpen
		}
		return err
	}
	b.fails = 0
	b.state = breakerClosed
	return nil
}

func (b *breaker) probe(f func() error) error {
	b.state = breakerHalfOpen
	if err := f(); err != nil {
		b.state = breakerOpen
		return err
	}
	b.state = breakerClosed
	b.fails = 0
	return nil
}

// scenarioCircuitBreakerPartitionHeal drives the full open -> heal -> close
// cycle on one *Network with no reconstruction and no restart, which
// docs/05 calls out specifically. Ordering matters for the resulting
// trace: a partitioned dial never establishes and so never produces a
// partitioned faultEvent (those only come from a write over an already-
// established pipe, faults.go) -- so the pair must dial and prove healthy
// first, then be partitioned.
func scenarioCircuitBreakerPartitionHeal() scenario {
	return scenario{
		name: "circuit-breaker-partition-heal",
		fn: func(t *testing.T, seed int64) []net.Conn {
			n := NewNetwork(WithSeed(seed))
			client, server := dialNamedPair(t, n)
			startEcho(t, server)

			ping := func() error {
				if err := client.SetDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
					return err
				}
				if _, err := client.Write([]byte("ping")); err != nil {
					return err
				}
				buf := make([]byte, 4)
				_, err := client.Read(buf)
				return err
			}

			b := &breaker{threshold: 1}

			if err := b.call(ping); err != nil {
				t.Fatalf("initial ping failed: %v", err)
			}
			if b.state != breakerClosed {
				t.Fatalf("breaker state after a healthy ping = %v, want closed", b.state)
			}

			n.Partition("client", "server")
			if err := b.call(ping); err == nil {
				t.Fatal("ping succeeded while partitioned, want a deadline failure")
			}
			if b.state != breakerOpen {
				t.Fatalf("breaker state after threshold failures = %v, want open", b.state)
			}

			n.Heal("client", "server")
			if err := b.probe(ping); err != nil {
				t.Fatalf("probe ping after Heal failed: %v (breaker should recover without a re-dial)", err)
			}
			if b.state != breakerClosed {
				t.Fatalf("breaker state after a successful post-heal probe = %v, want closed", b.state)
			}

			return []net.Conn{client, server}
		},
	}
}

func TestScenarioCircuitBreakerPartitionHeal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scenarioCircuitBreakerPartitionHeal().fn(t, 1)
	})
}

// --- 4. Three-peer failover ------------------------------------------------

// scenarioFailoverBetweenPeers builds one Network with client, server-a,
// and server-b, where server-b is partitioned from construction (docs/03's
// single-process multi-peer topology). The client must dial with
// DialContext + WithPeerName and a context timeout: a plain Dial against a
// partitioned peer, or a DialContext with no deadline, parks in
// waitUnpartitioned forever (netchaos.go), which would hang this scenario
// rather than exercise failover.
func scenarioFailoverBetweenPeers() scenario {
	const failoverTimeout = 30 * time.Millisecond

	return scenario{
		name: "failover-between-peers",
		fn: func(t *testing.T, seed int64) []net.Conn {
			n := NewNetwork(WithSeed(seed), WithPartition("client", "server-b"))

			la, err := n.Listen("tcp", "server-a")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = la.Close() })
			lb, err := n.Listen("tcp", "server-b")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = lb.Close() })

			acceptedA := make(chan net.Conn, 1)
			go func() {
				c, err := la.Accept()
				if err == nil {
					acceptedA <- c
				}
			}()

			ctx := WithPeerName(context.Background(), "client")

			dialCtx, cancel := context.WithTimeout(ctx, failoverTimeout)
			defer cancel()

			start := time.Now()
			if _, err := n.DialContext(dialCtx, "tcp", "server-b"); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("dial to partitioned server-b = %v, want context.DeadlineExceeded", err)
			}
			if elapsed := time.Since(start); elapsed != failoverTimeout {
				t.Fatalf("failover cost %v, want exactly %v (the configured timeout)", elapsed, failoverTimeout)
			}

			client, err := n.DialContext(ctx, "tcp", "server-a")
			if err != nil {
				t.Fatalf("failover dial to server-a: %v", err)
			}
			t.Cleanup(func() { _ = client.Close() })
			server := <-acceptedA
			t.Cleanup(func() { _ = server.Close() })

			if _, err := client.Write([]byte("ok")); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 2)
			if _, err := server.Read(buf); err != nil || string(buf) != "ok" {
				t.Fatalf("post-failover read = (%q, %v), want (\"ok\", nil)", buf, err)
			}

			return []net.Conn{client, server}
		},
	}
}

func TestScenarioFailoverBetweenPeers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scenarioFailoverBetweenPeers().fn(t, 1)
	})
}

// --- Harness integration ---------------------------------------------------

// TestScenariosAreReproducible satisfies "every scenario is deterministic
// under M3-3" without duplicating M3-3's own tests: it runs every scenario
// above twice under the same seed and asserts the resulting traces match.
func TestScenariosAreReproducible(t *testing.T) {
	scenarios := []scenario{
		scenarioRetryUnderLoss(0.3, 100),
		scenarioTimeoutBackoffUnderLatency(150*time.Millisecond, 120*time.Millisecond, 2),
		scenarioCircuitBreakerPartitionHeal(),
		scenarioFailoverBetweenPeers(),
	}

	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			var a, b canonicalTrace
			synctest.Test(t, func(t *testing.T) { a = runScenario(t, sc, 1) })
			synctest.Test(t, func(t *testing.T) { b = runScenario(t, sc, 1) })
			if !a.equal(b) {
				t.Fatalf("scenario %q not reproducible:\n%s", sc.name, a.diff(b))
			}
		})
	}
}

// TestScenarioSuiteCostsNoRealTime mirrors M3-2's TestLatencyCostsNoRealTime
// for the whole scenario suite: despite the largest configured latency here
// being 150ms and the retry scenario making up to 100 attempts, running
// every scenario end to end must still cost negligible real wall-clock
// time. The measurement is taken outside every bubble, since time.Since
// inside one reports virtual duration.
func TestScenarioSuiteCostsNoRealTime(t *testing.T) {
	wallStart := time.Now()

	synctest.Test(t, func(t *testing.T) { scenarioRetryUnderLoss(0.3, 100).fn(t, 1) })
	synctest.Test(t, func(t *testing.T) { scenarioTimeoutBackoffUnderLatency(150*time.Millisecond, 120*time.Millisecond, 2).fn(t, 1) })
	synctest.Test(t, func(t *testing.T) { scenarioCircuitBreakerPartitionHeal().fn(t, 1) })
	synctest.Test(t, func(t *testing.T) { scenarioFailoverBetweenPeers().fn(t, 1) })

	if el := time.Since(wallStart); el > time.Second {
		t.Fatalf("the whole scenario suite cost %v of real time, want < 1s", el)
	}
}
