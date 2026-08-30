package netchaos

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
)

func TestDialAcceptRoundTrip(t *testing.T) {
	n := NewNetwork()
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

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

	client, err := n.Dial("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	var server net.Conn
	select {
	case server = <-accepted:
	case err := <-acceptErr:
		t.Fatalf("Accept: %v", err)
	}
	defer func() { _ = server.Close() }()

	if got, want := client.RemoteAddr().String(), "server"; got != want {
		t.Fatalf("client.RemoteAddr() = %q, want %q", got, want)
	}
	if got, want := server.LocalAddr().String(), "server"; got != want {
		t.Fatalf("server.LocalAddr() = %q, want %q", got, want)
	}
	if client.LocalAddr().String() != server.RemoteAddr().String() {
		t.Fatalf("client.LocalAddr() = %v, server.RemoteAddr() = %v", client.LocalAddr(), server.RemoteAddr())
	}

	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	n2, err := server.Read(buf)
	if err != nil || string(buf[:n2]) != "ping" {
		t.Fatalf("server read = (%d, %q, %v), want (4, \"ping\", nil)", n2, buf[:n2], err)
	}

	if _, err := server.Write([]byte("pong")); err != nil {
		t.Fatal(err)
	}
	n2, err = client.Read(buf)
	if err != nil || string(buf[:n2]) != "pong" {
		t.Fatalf("client read = (%d, %q, %v), want (4, \"pong\", nil)", n2, buf[:n2], err)
	}
}

func TestDialUnregisteredAddr(t *testing.T) {
	n := NewNetwork()
	_, err := n.Dial("tcp", "nobody")
	if !errors.Is(err, ErrConnectionRefused) {
		t.Fatalf("Dial to unregistered addr = %v, want errors.Is(ErrConnectionRefused)", err)
	}
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		t.Fatalf("Dial to unregistered addr = %v, want a *net.OpError", err)
	}
}

func TestDialRejectsUDP(t *testing.T) {
	n := NewNetwork()
	_, err := n.Dial("udp", "server")
	if !errors.Is(err, ErrUnsupportedNetwork) {
		t.Fatalf("Dial(\"udp\", ...) = %v, want errors.Is(ErrUnsupportedNetwork)", err)
	}
}

func TestDialOrdinalsDeterministic(t *testing.T) {
	dialTwice := func() (first, second uint64) {
		n := NewNetwork()
		l, err := n.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()

		go func() {
			for i := 0; i < 2; i++ {
				c, err := l.Accept()
				if err == nil {
					defer func() { _ = c.Close() }()
				}
			}
		}()

		c1, err := n.Dial("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c1.Close() }()
		c2, err := n.Dial("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c2.Close() }()

		return c1.(*conn).ordinal, c2.(*conn).ordinal
	}

	first1, second1 := dialTwice()
	first2, second2 := dialTwice()

	if first1 == second1 {
		t.Fatalf("two Dial calls got the same ordinal: %d", first1)
	}
	if first1 != first2 || second1 != second2 {
		t.Fatalf("ordinals were not deterministic across runs: (%d, %d) vs (%d, %d)", first1, second1, first2, second2)
	}
}

func TestConcurrentDials(t *testing.T) {
	n := NewNetwork()
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	const dialers = 16
	go func() {
		for i := 0; i < dialers; i++ {
			c, err := l.Accept()
			if err == nil {
				_ = c.Close()
			}
		}
	}()

	var wg sync.WaitGroup
	ordinals := make([]uint64, dialers)
	for i := 0; i < dialers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := n.Dial("tcp", "server")
			if err != nil {
				t.Errorf("Dial %d: %v", i, err)
				return
			}
			ordinals[i] = c.(*conn).ordinal
			_ = c.Close()
		}(i)
	}
	wg.Wait()

	seen := make(map[uint64]bool, dialers)
	for _, o := range ordinals {
		if seen[o] {
			t.Fatalf("duplicate ordinal %d among concurrent dials", o)
		}
		seen[o] = true
	}
}

// TestBacklogFullOrdinalAccounting pins the determinism contract's claim
// that "a dial that never establishes never burns one" against the dial path
// itself. TestBacklogFull (listener_test.go) fills the queue by calling
// enqueue directly, so it proves the capacity bound without ever reaching
// the ordinal assignment; this test goes through Dial, which is the only way
// the accounting is observable.
func TestBacklogFullOrdinalAccounting(t *testing.T) {
	n := NewNetwork()
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	// Fill the accept queue without accepting anything: ordinals 0..127.
	for i := 0; i < listenerBacklog; i++ {
		c, err := n.Dial("tcp", "server")
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		defer func() { _ = c.Close() }()
	}

	if _, err := n.Dial("tcp", "server"); !errors.Is(err, ErrBacklogFull) {
		t.Fatalf("dial past the backlog = %v, want errors.Is(ErrBacklogFull)", err)
	}

	// Make room and dial again. The failed dial established nothing, so the
	// next connection that does establish must take the ordinal the failure
	// did not consume.
	if _, err := l.Accept(); err != nil {
		t.Fatal(err)
	}
	c, err := n.Dial("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	if got, want := c.(*conn).ordinal, uint64(listenerBacklog); got != want {
		t.Errorf("ordinal after a dial that failed with ErrBacklogFull = %d, want %d "+
			"(the failed dial must not consume one, or every later connection draws from a shifted RNG stream)", got, want)
	}
	// The same fact, in the form a user can actually see.
	if got, want := c.LocalAddr().String(), fmt.Sprintf("ephemeral:%d", listenerBacklog); got != want {
		t.Errorf("LocalAddr() after a dial that failed with ErrBacklogFull = %q, want %q", got, want)
	}
}

// TestClosedListenerDialOrdinalAccounting covers the second way the dial path
// can fail after the listener lookup succeeds: the listener is closed in the
// window between that lookup and the hand-off. Close deregisters the address,
// so a plain dial afterwards is refused at the lookup — the path that was
// always correct — and re-registering the closed listener is what reproduces
// the window deterministically instead of racing for it.
func TestClosedListenerDialOrdinalAccounting(t *testing.T) {
	n := NewNetwork()
	stale, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}

	n.mu.Lock()
	n.listeners["server"] = stale.(*listener)
	n.mu.Unlock()

	if _, err := n.Dial("tcp", "server"); !errors.Is(err, ErrConnectionRefused) {
		t.Fatalf("dial into a closed listener = %v, want errors.Is(ErrConnectionRefused)", err)
	}

	n.mu.Lock()
	delete(n.listeners, "server")
	n.mu.Unlock()

	live, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = live.Close() }()

	c, err := n.Dial("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	if got, want := c.(*conn).ordinal, uint64(0); got != want {
		t.Errorf("ordinal after a dial refused by a closed listener = %d, want %d "+
			"(the refused dial established nothing, so the first connection to establish is still the first)", got, want)
	}
}

// TestConcurrentDialsAtBacklogBoundary guards the reserve/fill counter
// itself, which is new machinery rather than the bug M6-1 started from: with
// more dialers than the backlog can take and nobody accepting, exactly
// listenerBacklog dials must succeed and the ordinals handed out must be
// exactly 0..k-1. A reservation that is never released shows up here as
// fewer successes than capacity; one released twice, as a duplicate ordinal
// or a gap.
//
// It is deliberately not the regression test for M6-1 — it was checked
// against the pre-fix code and passed, because the dials that win the
// ordinal race are usually the same ones that win the enqueue race. The
// deterministic assertions for the original defect are
// TestBacklogFullOrdinalAccounting and TestClosedListenerDialOrdinalAccounting.
func TestConcurrentDialsAtBacklogBoundary(t *testing.T) {
	n := NewNetwork()
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	const dialers = listenerBacklog + 32

	var wg sync.WaitGroup
	var mu sync.Mutex
	var ordinals []uint64
	failures := 0

	for i := 0; i < dialers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := n.Dial("tcp", "server")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if !errors.Is(err, ErrBacklogFull) {
					t.Errorf("Dial = %v, want nil or errors.Is(ErrBacklogFull)", err)
				}
				failures++
				return
			}
			ordinals = append(ordinals, c.(*conn).ordinal)
		}()
	}
	wg.Wait()

	if got := len(ordinals); got != listenerBacklog {
		t.Errorf("successful dials = %d, want %d (the backlog's capacity)", got, listenerBacklog)
	}
	if want := dialers - len(ordinals); failures != want {
		t.Errorf("failed dials = %d, want %d", failures, want)
	}

	seen := make(map[uint64]bool, len(ordinals))
	for _, o := range ordinals {
		if seen[o] {
			t.Fatalf("duplicate ordinal %d", o)
		}
		seen[o] = true
	}
	for i := uint64(0); i < uint64(len(ordinals)); i++ {
		if !seen[i] {
			t.Fatalf("ordinal %d was never handed out among %d successful dials", i, len(ordinals))
		}
	}
}

func TestDialContextCancelled(t *testing.T) {
	n := NewNetwork()
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = n.DialContext(ctx, "tcp", "server")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DialContext with a pre-cancelled ctx = %v, want errors.Is(context.Canceled)", err)
	}

	// No queue entry should have been left behind.
	ln := l.(*listener)
	select {
	case c := <-ln.incoming:
		t.Fatalf("cancelled DialContext still enqueued a connection: %v", c)
	default:
	}
}
