package netchaos

import (
	"context"
	"net"
	"testing"
	"testing/synctest"
	"time"
)

// dialNamedPair dials a named "client" against a "server" listener on n,
// returning both ends already accepted. Shared by the composition tests
// below, which all need WithPeerName to make the pair partition-targetable.
func dialNamedPair(t *testing.T, n *Network) (client, server net.Conn) {
	t.Helper()
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

	ctx := WithPeerName(context.Background(), "client")
	client, err = n.DialContext(ctx, "tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	server = <-accepted
	t.Cleanup(func() { _ = server.Close() })
	return client, server
}

// TestFaultOrderPartitionWinsOverLoss asserts partition is evaluated first:
// a unit bound for a partitioned pair is discarded even when loss (rate
// 0.0, so it would never itself drop) is also configured, and -- because
// partition short-circuits before any fault draws -- the loss stream is
// left completely undrawn.
func TestFaultOrderPartitionWinsOverLoss(t *testing.T) {
	n := NewNetwork(WithSeed(9), WithPacketLoss(0.0))
	client, server := dialNamedPair(t, n)

	n.Partition("client", "server")

	if err := server.SetReadDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("blocked")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 7)
	if _, err := server.Read(buf); err == nil {
		t.Fatal("read succeeded despite the pair being partitioned")
	}

	trace := client.(*conn).writePipe.trace.snapshot()
	if len(trace) != 1 || !trace[0].partitioned || trace[0].dropped {
		t.Fatalf("trace = %+v, want a single partitioned (not dropped) event", trace)
	}

	// The loss stream must be completely untouched: its next draw must
	// match a fresh derivation, proving the write consumed zero draws.
	c := client.(*conn)
	want := deriveStream(9, c.ordinal, sideDialer, kindLoss)
	if c.writePipe.loss.next() != want.next() {
		t.Fatal("a partitioned write advanced the loss stream; partition must consume zero draws")
	}
}

// TestFaultOrderLossWinsOverLatency asserts that once loss drops a unit,
// latency's queuing/delivery never happens for it -- no data arrives, and
// no pending entry is left behind.
func TestFaultOrderLossWinsOverLatency(t *testing.T) {
	n := NewNetwork(WithSeed(4), WithPacketLoss(1.0), WithLatency(time.Millisecond, 50*time.Millisecond))
	client, server := dialNamedPair(t, n)

	if err := server.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("dropped")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 7)
	if _, err := server.Read(buf); err == nil {
		t.Fatal("read succeeded despite rate-1.0 loss being configured")
	}

	if pending := client.(*conn).writePipe.pending; len(pending) != 0 {
		t.Fatalf("a dropped unit was left in the latency pending queue: %+v", pending)
	}
}

// TestDrawDisciplineStable asserts the documented draw discipline directly:
// a unit dropped by loss still consumed a latency draw, keeping the
// latency stream's draw index equal to the unit index regardless of what
// loss decided.
func TestDrawDisciplineStable(t *testing.T) {
	n := NewNetwork(WithSeed(11), WithPacketLoss(1.0), WithLatency(10*time.Millisecond, 10*time.Millisecond))
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
	if trace[0].drawn != 10*time.Millisecond {
		t.Fatalf("trace[0].drawn = %v, want 10ms: a dropped unit must still draw its latency duration", trace[0].drawn)
	}
}

func TestAllFaultsDeterministic(t *testing.T) {
	// The pending-queue clamp in installFaultPolicy computes effective
	// delay from real gaps between successive writes (time.Now()), so
	// comparing it across two runs is only deterministic under synctest's
	// virtual time -- outside a bubble, real scheduling jitter between
	// writes (worse under -race) makes effective (not drawn) differ
	// between otherwise-identical runs.
	// run takes the synctest-provided t explicitly rather than closing over
	// the outer one: dialNamedPair registers t.Cleanup(conn.Close), and
	// that Close must run before its bubble exits -- against the outer t,
	// cleanup would fire only when the whole test function returns, by
	// which point the bubble is long gone ("close of synctest channel from
	// outside bubble").
	run := func(t *testing.T) []faultEvent {
		n := NewNetwork(WithSeed(21), WithPacketLoss(0.3), WithLatency(time.Millisecond, 20*time.Millisecond))
		client, _ := dialNamedPair(t, n)

		for i := 0; i < 30; i++ {
			if i == 15 {
				n.Partition("client", "server")
			}
			if _, err := client.Write([]byte{byte(i)}); err != nil {
				t.Fatal(err)
			}
		}
		return client.(*conn).writePipe.trace.snapshot()
	}

	var a, b []faultEvent
	synctest.Test(t, func(t *testing.T) { a = run(t) })
	synctest.Test(t, func(t *testing.T) { b = run(t) })

	if len(a) != len(b) {
		t.Fatalf("trace lengths differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("event %d differs across runs: %+v vs %+v", i, a[i], b[i])
		}
	}
}

// TestUnrelatedPartitionDoesNotPerturb repeats M2-4's isolation guarantee
// with the full composed evaluator (loss + latency + partition all
// configured), rather than loss alone.
func TestUnrelatedPartitionDoesNotPerturb(t *testing.T) {
	// See TestAllFaultsDeterministic: effective delay depends on real
	// inter-write timing outside a synctest bubble, so this comparison
	// needs virtual time to be reliable.
	// trace takes the synctest-provided t explicitly; see run in
	// TestAllFaultsDeterministic for why closing over the outer t panics.
	trace := func(t *testing.T, partitionUnrelated bool) []faultEvent {
		n := NewNetwork(WithSeed(6), WithPacketLoss(0.3), WithLatency(time.Millisecond, 20*time.Millisecond))
		if partitionUnrelated {
			n.Partition("someone-else", "unrelated")
		}
		client, _ := dialNamedPair(t, n)

		for i := 0; i < 30; i++ {
			if _, err := client.Write([]byte{byte(i)}); err != nil {
				t.Fatal(err)
			}
		}
		return client.(*conn).writePipe.trace.snapshot()
	}

	var a, b []faultEvent
	synctest.Test(t, func(t *testing.T) { a = trace(t, false) })
	synctest.Test(t, func(t *testing.T) { b = trace(t, true) })

	if len(a) != len(b) {
		t.Fatalf("trace lengths differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("event %d perturbed by partitioning an unrelated pair: %+v vs %+v", i, a[i], b[i])
		}
	}
}

// TestSingleDeliveryHookPerUnit asserts there is exactly one fault
// evaluation point: with all three faults configured, deliver is set once
// (to the composed evaluator), not chained/overwritten by separate
// per-fault installers.
func TestSingleDeliveryHookPerUnit(t *testing.T) {
	n := NewNetwork(WithPacketLoss(0.1), WithLatency(time.Millisecond, time.Millisecond))
	client, _ := dialNamedPair(t, n)

	// Partitioning after the dial (rather than via WithPartition, which
	// would block the dial itself) exercises the same composed hook for an
	// already-established connection.
	n.Partition("client", "server")

	// Best-effort structural check: the deliver closure must reflect all
	// three faults' effects from a single write, observable via the trace
	// alone (composition, not a chain of independently-overwriting hooks).
	if _, err := client.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	trace := client.(*conn).writePipe.trace.snapshot()
	if len(trace) != 1 || !trace[0].partitioned {
		t.Fatalf("trace = %+v, want a single partitioned event (partition was configured)", trace)
	}
}
