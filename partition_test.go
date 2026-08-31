package netchaos

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"testing/synctest"
	"time"
)

func TestStaticPartition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork(WithPartition("client", "server"))
		l, err := n.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()

		ctx := WithPeerName(context.Background(), "client")
		ctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		defer cancel()

		_, err = n.DialContext(ctx, "tcp", "server")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("DialContext against a statically partitioned pair = %v, want errors.Is(context.DeadlineExceeded)", err)
		}
	})
}

func TestDialUnderPartition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork()
		l, err := n.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()

		n.Partition("client", "server")

		ctx := WithPeerName(context.Background(), "client")
		ctx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
		defer cancel()

		start := time.Now()
		_, err = n.DialContext(ctx, "tcp", "server")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("DialContext under a partition = %v, want errors.Is(context.DeadlineExceeded)", err)
		}
		if elapsed := time.Since(start); elapsed != 30*time.Millisecond {
			t.Fatalf("dial returned after %v virtual time, want exactly the 30ms deadline (it must actually block, not fail fast)", elapsed)
		}
	})
}

func TestDialUnblocksOnHeal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork()
		l, err := n.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()

		n.Partition("client", "server")

		accepted := make(chan net.Conn, 1)
		acceptErr := make(chan error, 1)
		go func() {
			c, err := l.Accept()
			if err != nil {
				acceptErr <- err
				return
			}
			accepted <- c
		}()

		dialResult := make(chan error, 1)
		var client net.Conn
		go func() {
			ctx := WithPeerName(context.Background(), "client")
			c, err := n.DialContext(ctx, "tcp", "server")
			client = c
			dialResult <- err
		}()

		synctest.Wait()

		n.Heal("client", "server")

		if err := <-dialResult; err != nil {
			t.Fatalf("DialContext after Heal = %v, want nil", err)
		}
		defer func() { _ = client.Close() }()

		select {
		case s := <-accepted:
			defer func() { _ = s.Close() }()
		case err := <-acceptErr:
			t.Fatalf("Accept: %v", err)
		}
	})
}

func TestDynamicPartitionThenHeal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork()
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

		ctx := WithPeerName(context.Background(), "client")
		client, err := n.DialContext(ctx, "tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = client.Close() }()
		server := <-accepted
		defer func() { _ = server.Close() }()

		if _, err := client.Write([]byte("before")); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 6)
		nr, err := server.Read(buf)
		if err != nil || string(buf[:nr]) != "before" {
			t.Fatalf("read before partition = (%d, %q, %v), want (6, \"before\", nil)", nr, buf[:nr], err)
		}

		n.Partition("client", "server")

		if err := server.SetReadDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Write([]byte("dropped")); err != nil {
			t.Fatal(err)
		}
		if _, err := server.Read(buf); err == nil {
			t.Fatal("read while partitioned succeeded, want it to block until the deadline")
		}

		n.Heal("client", "server")

		if err := server.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Write([]byte("after!")); err != nil {
			t.Fatal(err)
		}
		nr, err = server.Read(buf)
		if err != nil || string(buf[:nr]) != "after!" {
			t.Fatalf("read after heal = (%d, %q, %v), want (6, \"after!\", nil): Heal must restore traffic without a re-dial", nr, buf[:nr], err)
		}
	})
}

func TestPartitionPairOrderIndependent(t *testing.T) {
	n := NewNetwork()
	n.Partition("a", "b")

	if !n.isPartitioned(newPairKey("a", "b")) {
		t.Fatal("Partition(\"a\", \"b\") did not register as partitioned")
	}
	if !n.isPartitioned(newPairKey("b", "a")) {
		t.Fatal("Partition(\"a\", \"b\") did not register for the reverse pair order")
	}

	n.Heal("b", "a")
	if n.isPartitioned(newPairKey("a", "b")) {
		t.Fatal("Heal(\"b\", \"a\") did not clear a partition established as Partition(\"a\", \"b\")")
	}
}

func TestPartitionIsolatedToPair(t *testing.T) {
	n := NewNetwork()
	n.Partition("a", "b")

	if n.isPartitioned(newPairKey("a", "c")) {
		t.Fatal("partitioning (a,b) affected the unrelated pair (a,c)")
	}
	if n.isPartitioned(newPairKey("c", "d")) {
		t.Fatal("partitioning (a,b) affected the unrelated pair (c,d)")
	}
}

func TestHealUnpartitionedPairIsNoop(t *testing.T) {
	n := NewNetwork()
	n.Heal("never", "partitioned") // must not panic
	if n.isPartitioned(newPairKey("never", "partitioned")) {
		t.Fatal("Heal on a never-partitioned pair registered a partition")
	}
}

func TestPartitionUnknownPeerIsNoop(t *testing.T) {
	n := NewNetwork()
	n.Partition("nobody", "nowhere") // must not panic; legitimate "start partitioned" setup
	if !n.isPartitioned(newPairKey("nobody", "nowhere")) {
		t.Fatal("Partition between unregistered peers did not register")
	}
}

func TestPartitionConsumesNoRandomness(t *testing.T) {
	trace := func(partitionUnrelated bool) []faultEvent {
		n := NewNetwork(WithSeed(3), WithPacketLoss(0.3))
		l, err := n.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()

		if partitionUnrelated {
			n.Partition("someone-else", "unrelated")
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
		defer func() { _ = client.Close() }()
		server := <-accepted
		defer func() { _ = server.Close() }()

		for i := 0; i < 50; i++ {
			if _, err := client.Write([]byte{byte(i)}); err != nil {
				t.Fatal(err)
			}
		}
		return client.(*conn).writePipe.trace.snapshot()
	}

	a, b := trace(false), trace(true)
	if len(a) != len(b) {
		t.Fatalf("trace lengths differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("event %d perturbed by partitioning an unrelated pair: %+v vs %+v", i, a[i], b[i])
		}
	}
}

// TestCircuitBreakerScenario was grown into
// TestScenarioCircuitBreakerPartitionHeal (scenario_test.go, M3-4) and moved
// there -- see that file for the full M3-4 shape (a real closed/open/
// half-open breaker, fixed seed, run through the M3-3 reproducibility
// harness).

func TestPartitionRace(t *testing.T) {
	n := NewNetwork()
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				buf := make([]byte, 16)
				for {
					if _, err := c.Read(buf); err != nil {
						return
					}
				}
			}()
		}
	}()

	ctx := WithPeerName(context.Background(), "client")
	client, err := n.DialContext(ctx, "tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			if i%2 == 0 {
				n.Partition("client", "server")
			} else {
				n.Heal("client", "server")
			}
		}
	}()

	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		for i := 0; i < 200; i++ {
			if _, err := client.Write([]byte("x")); err != nil {
				return
			}
		}
	}()

	<-done
	<-writeDone
}

// TestPartitionedWriteDiscardedSilently exercises the M0-3-consistent
// silent-gap behaviour for partitioned traffic: a write into a partitioned
// pair still reports success to the caller, matching what a real partition
// looks like at the sending socket.
func TestPartitionedWriteDiscardedSilently(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork()
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

		ctx := WithPeerName(context.Background(), "client")
		client, err := n.DialContext(ctx, "tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = client.Close() }()
		server := <-accepted
		defer func() { _ = server.Close() }()

		n.Partition("client", "server")

		nw, err := client.Write([]byte("gone"))
		if err != nil || nw != 4 {
			t.Fatalf("Write while partitioned = (%d, %v), want (4, nil)", nw, err)
		}

		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
		// Draining after close: the partitioned write must not appear, but
		// io.EOF must still follow (no error surfaces from a partition).
		buf := make([]byte, 32)
		n2, err := server.Read(buf)
		if n2 != 0 || !errors.Is(err, io.EOF) {
			t.Fatalf("server drain read = (%d, %v), want (0, io.EOF)", n2, err)
		}
	})
}

// TestPartitionTargetsHostHalf answers the sub-question M6-10 named inside
// its decision: what Partition("server") means once the peer's address is
// "server:8080". The port is presentation; the host is identity. A test that
// listened on an address with a port must still be partitionable by the name
// it was given, or every existing Partition call in a suite breaks the day
// addresses gain structure.
func TestPartitionTargetsHostHalf(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork(WithPartition("client", "server"))
		l, err := n.Listen("tcp", "server:8080")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()

		ctx := WithPeerName(context.Background(), "client")
		ctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		defer cancel()

		// The listener is "server:8080"; the partition names "server". If the
		// port were part of the identity these would be different peers and
		// the dial would succeed.
		if _, err := n.DialContext(ctx, "tcp", "server:8080"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("DialContext to a partitioned peer addressed with a port = %v, want errors.Is(context.DeadlineExceeded)", err)
		}
	})
}
