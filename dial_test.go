package netchaos

import (
	"context"
	"errors"
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
