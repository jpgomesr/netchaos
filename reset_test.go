package netchaos

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

// TestResetSurfacesECONNRESETToReader is the headline claim: after
// Network.Reset, both ends' Read fail with an error satisfying
// errors.Is(err, syscall.ECONNRESET), wrapped in a *net.OpError (M6-2's
// uniform shape).
func TestResetSurfacesECONNRESETToReader(t *testing.T) {
	n := NewNetwork()
	client, server := dialNamedPair(t, n)

	n.Reset("client", "server")

	buf := make([]byte, 1)
	if _, err := client.Read(buf); !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("client.Read after Reset = %v, want errors.Is(syscall.ECONNRESET)", err)
	}
	var opErr *net.OpError
	if _, err := client.Read(buf); !errors.As(err, &opErr) {
		t.Fatalf("client.Read after Reset = %v, want a *net.OpError", err)
	}

	if _, err := server.Read(buf); !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("server.Read after Reset = %v, want errors.Is(syscall.ECONNRESET): a reset affects both ends", err)
	}
}

// TestResetSurfacesECONNRESETToWriter mirrors the read case for Write: a
// reset connection fails Write the same way, not just Read.
func TestResetSurfacesECONNRESETToWriter(t *testing.T) {
	n := NewNetwork()
	client, server := dialNamedPair(t, n)

	n.Reset("client", "server")

	if _, err := client.Write([]byte("x")); !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("client.Write after Reset = %v, want errors.Is(syscall.ECONNRESET)", err)
	}
	if _, err := server.Write([]byte("x")); !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("server.Write after Reset = %v, want errors.Is(syscall.ECONNRESET)", err)
	}
}

// TestResetUnblocksInFlightRead confirms a Read already blocked on another
// goroutine unblocks with ECONNRESET rather than hanging to its deadline --
// the property that makes Reset useful for testing reconnect logic instead
// of a caller having to poll.
//
// Runs inside a synctest bubble, using synctest.Wait (mirroring
// TestDialUnblocksOnHeal, partition_test.go) rather than a real
// time.Sleep to let the reader goroutine reach its blocking point before
// Reset fires -- a wall-clock sleep here would be a timing assumption a
// slow or -race-instrumented CI runner could violate.
func TestResetUnblocksInFlightRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork()
		_, server := dialNamedPair(t, n)

		if err := server.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}

		result := make(chan error, 1)
		go func() {
			buf := make([]byte, 1)
			_, err := server.Read(buf)
			result <- err
		}()

		// Blocks until every other goroutine in the bubble is durably
		// blocked, which for the goroutine above means it has reached the
		// select inside Read -- deterministic, unlike a real sleep.
		synctest.Wait()

		n.Reset("client", "server")

		select {
		case err := <-result:
			if !errors.Is(err, syscall.ECONNRESET) {
				t.Fatalf("blocked Read unblocked with %v, want errors.Is(syscall.ECONNRESET)", err)
			}
		case <-time.After(time.Second):
			t.Fatal("blocked Read did not unblock after Reset")
		}
	})
}

// TestResetConnectionStaysReset asserts a reset connection has no path back
// to successful I/O: repeated Read/Write calls after the first all fail the
// same way, unlike a partition, which Heal reverses.
func TestResetConnectionStaysReset(t *testing.T) {
	n := NewNetwork()
	client, _ := dialNamedPair(t, n)

	n.Reset("client", "server")

	buf := make([]byte, 1)
	for i := 0; i < 3; i++ {
		if _, err := client.Read(buf); !errors.Is(err, syscall.ECONNRESET) {
			t.Fatalf("Read attempt %d after Reset = %v, want errors.Is(syscall.ECONNRESET)", i, err)
		}
		if _, err := client.Write([]byte("x")); !errors.Is(err, syscall.ECONNRESET) {
			t.Fatalf("Write attempt %d after Reset = %v, want errors.Is(syscall.ECONNRESET)", i, err)
		}
	}
}

// TestResetIsNoOpForUnestablishedPair mirrors Partition/Heal's no-op
// convention (partition_test.go: TestPartitionUnknownPeerIsNoop,
// TestHealUnpartitionedPairIsNoop): Reset on a pair with nothing currently
// established must not panic or block.
func TestResetIsNoOpForUnestablishedPair(t *testing.T) {
	n := NewNetwork()
	n.Reset("nobody", "here") // must not panic
}

// TestResetDoesNotAffectFutureDial confirms Reset acts only on connections
// that exist at the moment it is called, matching a real RST's effect on
// existing TCP state rather than a standing block on the pair -- the
// opposite of Partition, which does gate future dials until Heal.
func TestResetDoesNotAffectFutureDial(t *testing.T) {
	n := NewNetwork()
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	accepted := make(chan net.Conn, 2)
	go func() {
		for i := 0; i < 2; i++ {
			c, err := l.Accept()
			if err == nil {
				accepted <- c
			}
		}
	}()

	dial := func() net.Conn {
		t.Helper()
		ctx := WithPeerName(context.Background(), "client")
		c, err := n.DialContext(ctx, "tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	old := dial()
	defer func() { _ = old.Close() }()
	<-accepted // old's server side, unused beyond accounting for the accept queue

	n.Reset("client", "server")

	buf := make([]byte, 1)
	if _, err := old.Read(buf); !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("old connection Read after Reset = %v, want errors.Is(syscall.ECONNRESET)", err)
	}

	fresh := dial()
	defer func() { _ = fresh.Close() }()
	freshServer := <-accepted
	defer func() { _ = freshServer.Close() }()

	if _, err := fresh.Write([]byte("ok")); err != nil {
		t.Fatalf("Write on a connection dialed after Reset = %v, want nil", err)
	}
	got := make([]byte, 2)
	if _, err := readFull(freshServer, got); err != nil {
		t.Fatalf("Read on a connection dialed after Reset = %v, want nil", err)
	}
	if string(got) != "ok" {
		t.Fatalf("got %q, want %q: a connection dialed after Reset must behave normally", got, "ok")
	}
}

// TestResetIsolatedToPair mirrors TestPartitionIsolatedToPair
// (partition_test.go): resetting one pair must not affect an unrelated
// connection.
func TestResetIsolatedToPair(t *testing.T) {
	n := NewNetwork()
	l, err := n.Listen("tcp", "other-server")
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
	unrelatedClient, err := n.Dial("tcp", "other-server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unrelatedClient.Close() }()
	unrelatedServer := <-accepted
	defer func() { _ = unrelatedServer.Close() }()

	client, _ := dialNamedPair(t, n)
	n.Reset("client", "server")

	buf := make([]byte, 1)
	if _, err := client.Read(buf); !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("Read on the targeted pair = %v, want errors.Is(syscall.ECONNRESET)", err)
	}

	if _, err := unrelatedClient.Write([]byte("x")); err != nil {
		t.Fatalf("Write on an unrelated connection after an unrelated Reset = %v, want nil", err)
	}
	got := make([]byte, 1)
	if _, err := readFull(unrelatedServer, got); err != nil {
		t.Fatalf("Read on an unrelated connection after an unrelated Reset = %v, want nil", err)
	}
}
