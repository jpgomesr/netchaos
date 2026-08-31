package netchaos

import (
	"net"
	"testing"
	"time"
)

// TestNetworkTraceReportsDroppedWrites is #51's own use case: a user
// outside the package writes a test exercising packet loss and asserts an
// exact drop count, which was impossible before this task -- only the
// downstream consequence (a short read) was observable.
func TestNetworkTraceReportsDroppedWrites(t *testing.T) {
	n := NewNetwork(WithSeed(7), WithPacketLoss(1.0))
	client, _ := dialPair(t, n)

	for i := 0; i < 3; i++ {
		if _, err := client.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}

	got := n.Trace()
	dropped := 0
	for _, ev := range got {
		if ev.Dropped {
			dropped++
		}
	}
	if dropped != 3 {
		t.Fatalf("dropped = %d, want 3 (rate 1.0 drops every unit): %+v", dropped, got)
	}
}

// TestExportedTraceIsACopy mirrors TestTraceSnapshotIsACopy (trace_test.go)
// at the exported boundary: mutating a slice returned by Trace must never
// affect a later call.
func TestExportedTraceIsACopy(t *testing.T) {
	n := NewNetwork(WithSeed(1), WithPacketLoss(1.0))
	client, _ := dialPair(t, n)
	if _, err := client.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}

	got := n.Trace()
	if len(got) == 0 {
		t.Fatal("Trace() returned no events")
	}
	got[0].Dropped = false

	if again := n.Trace(); !again[0].Dropped {
		t.Fatalf("mutating a Trace() result affected a later call: %+v", again)
	}
}

// TestTraceSurvivesConnClose is the property that makes Network.Trace
// different from Network.Reset's registry (M7-7): a trace has to be
// readable after the connection producing it has already been closed --
// the common defer c.Close() case -- or the accessor is useless for the
// most ordinary test shape.
func TestTraceSurvivesConnClose(t *testing.T) {
	n := NewNetwork(WithSeed(3), WithPacketLoss(1.0))
	client, server := dialPair(t, n)
	if _, err := client.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}

	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}

	got := n.Trace()
	dropped := 0
	for _, ev := range got {
		if ev.Dropped {
			dropped++
		}
	}
	if dropped != 1 {
		t.Fatalf("dropped = %d after both ends closed, want 1: %+v", dropped, got)
	}
}

// TestTraceCoversEveryFaultKind exercises the two evaluator paths
// separately: a loss-heavy Network never reaches the bandwidth/latency
// stages (installFaultPolicy's early-return for a dropped unit), so no
// single configuration exercises every field. Two sub-tests, each covering
// one path, together satisfy "every fault kind is representable".
func TestTraceCoversEveryFaultKind(t *testing.T) {
	t.Run("dropped path", func(t *testing.T) {
		n := NewNetwork(WithSeed(11), WithPacketLoss(1.0), WithDuplication(1.0), WithCorruption(1.0))
		client, _ := dialPair(t, n)
		if _, err := client.Write([]byte("xyz")); err != nil {
			t.Fatal(err)
		}

		got := n.Trace()
		if len(got) != 1 {
			t.Fatalf("len(Trace()) = %d, want 1", len(got))
		}
		ev := got[0]
		if !ev.Dropped || !ev.Duplicated || !ev.Corrupted {
			t.Fatalf("dropped-path event missing a configured fault: %+v", ev)
		}
	})

	t.Run("delivery path", func(t *testing.T) {
		n := NewNetwork(WithSeed(13), WithBandwidth(1024), WithLatency(10*time.Millisecond, 10*time.Millisecond))
		client, _ := dialPair(t, n)
		if _, err := client.Write([]byte("xyz")); err != nil {
			t.Fatal(err)
		}

		got := n.Trace()
		if len(got) != 1 {
			t.Fatalf("len(Trace()) = %d, want 1", len(got))
		}
		ev := got[0]
		if ev.Dropped {
			t.Fatalf("delivery-path event unexpectedly dropped: %+v", ev)
		}
		if ev.Serialization <= 0 {
			t.Fatalf("Serialization = %v, want > 0 with WithBandwidth configured", ev.Serialization)
		}
		if ev.Delay != 10*time.Millisecond {
			t.Fatalf("Delay = %v, want exactly 10ms", ev.Delay)
		}
	})
}

// TestTraceOrderIsOrdinalSideSeq asserts the canonical order two
// connections' events come back in: dial order (== ordinal order), then
// side, then per-direction sequence -- the same order the reproducibility
// harness (M3-3) compares golden traces in.
func TestTraceOrderIsOrdinalSideSeq(t *testing.T) {
	n := NewNetwork(WithSeed(5))
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

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
	s1 := <-accepted
	c2, err := n.Dial("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	s2 := <-accepted
	_ = s1
	_ = s2

	if _, err := c1.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}

	got := n.Trace()
	var last *FaultEvent
	for i := range got {
		ev := got[i]
		if last != nil {
			if ev.Ordinal < last.Ordinal {
				t.Fatalf("event %d: ordinal %d < previous %d, not in ascending order", i, ev.Ordinal, last.Ordinal)
			}
			if ev.Ordinal == last.Ordinal && ev.Side == last.Side && ev.Seq <= last.Seq {
				t.Fatalf("event %d: seq %d did not increase within (ordinal=%d, side=%v)", i, ev.Seq, ev.Ordinal, ev.Side)
			}
		}
		last = &got[i]
	}
	if len(got) == 0 {
		t.Fatal("Trace() returned no events for two established connections")
	}
}

// TestTraceRecordsPartitionedUnits confirms a unit stopped at the
// partition gate records Partitioned and nothing else -- partition draws
// no fault, per the determinism contract.
func TestTraceRecordsPartitionedUnits(t *testing.T) {
	n := NewNetwork(WithSeed(9))
	client, _ := dialNamedPair(t, n)
	n.Partition("client", "server")

	if _, err := client.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}

	got := n.Trace()
	found := false
	for _, ev := range got {
		if ev.Partitioned {
			found = true
			if ev.Dropped || ev.Duplicated || ev.Corrupted || ev.Delay != 0 || ev.Serialization != 0 || ev.Effective != 0 {
				t.Fatalf("partitioned event carries extra state: %+v", ev)
			}
		}
	}
	if !found {
		t.Fatal("Trace() has no Partitioned event for a write against a partitioned pair")
	}
}

// TestSideString covers the exported Side enum's String method, which
// TestTraceOrderIsOrdinalSideSeq's failure messages already rely on being
// readable.
func TestSideString(t *testing.T) {
	if got := SideDialer.String(); got != "dialer" {
		t.Fatalf("SideDialer.String() = %q, want %q", got, "dialer")
	}
	if got := SideAcceptor.String(); got != "acceptor" {
		t.Fatalf("SideAcceptor.String() = %q, want %q", got, "acceptor")
	}
}
