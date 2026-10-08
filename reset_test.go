package netchaos

import (
	"context"
	"errors"
	"net"
	"strings"
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

// TestResetThenCloseIsDeterministicallyClosed pins issue #111: once a reset
// connection is also closed locally -- the usual reaction to seeing
// ECONNRESET -- every later Read/Write fails with net.ErrClosed, never
// ECONNRESET. The local Close wins, as Close's own contract (conn.go) and a
// real net.Conn both promise. Looped because the defect was a runtime
// select picking between two ready cases pseudo-randomly: a single
// iteration passes about half the time.
func TestResetThenCloseIsDeterministicallyClosed(t *testing.T) {
	buf := make([]byte, 1)
	for i := range 10000 {
		n := NewNetwork()
		client, server := dialNamedPair(t, n)

		n.Reset("client", "server")
		_ = client.Close()

		if _, err := client.Read(buf); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("iteration %d: Read after Reset then Close = %v, want errors.Is(net.ErrClosed)", i, err)
		}
		if _, err := client.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("iteration %d: Write after Reset then Close = %v, want errors.Is(net.ErrClosed)", i, err)
		}
		// The peer end was reset but not closed, so it still sees the reset.
		if _, err := server.Read(buf); !errors.Is(err, syscall.ECONNRESET) {
			t.Fatalf("iteration %d: peer Read after Reset = %v, want errors.Is(syscall.ECONNRESET)", i, err)
		}
	}
}

// TestCloseThenReset is the reverse order: a conn closed locally before a
// Reset reaches it still reports net.ErrClosed afterwards.
func TestCloseThenReset(t *testing.T) {
	buf := make([]byte, 1)
	for i := range 1000 {
		n := NewNetwork()
		client, _ := dialNamedPair(t, n)

		_ = client.Close()
		n.Reset("client", "server")

		if _, err := client.Read(buf); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("iteration %d: Read after Close then Reset = %v, want errors.Is(net.ErrClosed)", i, err)
		}
		if _, err := client.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("iteration %d: Write after Close then Reset = %v, want errors.Is(net.ErrClosed)", i, err)
		}
	}
}

// TestResetIsNoOpForUnestablishedPair mirrors Partition/Heal's no-op
// convention (partition_test.go: TestPartitionUnknownPeerIsNoop,
// TestHealUnpartitionedPairIsNoop): Reset on a pair with nothing currently
// established must not panic or block.
func TestResetIsNoOpForUnestablishedPair(_ *testing.T) {
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

// TestResetPanicsOnInvalidPair is Reset's half of M9-1 (#83): Reset must
// validate the same raw arguments Partition/Heal/WithPartition already do,
// panicking on an empty peer name or a self-pair and naming itself.
func TestResetPanicsOnInvalidPair(t *testing.T) {
	tests := []struct {
		name string
		call func(*Network)
	}{
		{"empty peerA", func(n *Network) { n.Reset("", "b") }},
		{"empty peerB", func(n *Network) { n.Reset("a", "") }},
		{"self-pair", func(n *Network) { n.Reset("a", "a") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("no panic, want one naming Reset and the offending value")
				}
				msg, ok := r.(string)
				if !ok || !strings.Contains(msg, "Reset") {
					t.Fatalf("panic = %v, want a message mentioning %q", r, "Reset")
				}
			}()
			tt.call(NewNetwork())
		})
	}
}
