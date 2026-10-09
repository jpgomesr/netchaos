package netchaos

import (
	"errors"
	"io"
	"net"
	"testing"
	"testing/synctest"
)

func TestListenRegistersAddr(t *testing.T) {
	n := NewNetwork()
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	host, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatalf("net.SplitHostPort(%q): %v", l.Addr(), err)
	}
	if got, want := host, "server"; got != want {
		t.Fatalf("host of Addr() = %q, want %q", got, want)
	}
	if port == "" || port == "0" {
		t.Fatalf("Addr() = %q, want a synthesized port", l.Addr())
	}
}

func TestListenDuplicateAddr(t *testing.T) {
	n := NewNetwork()
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	_, err = n.Listen("tcp", "server")
	if !errors.Is(err, ErrAddressInUse) {
		t.Fatalf("second Listen on the same addr = %v, want errors.Is(ErrAddressInUse)", err)
	}
}

func TestAcceptBlocksWhenEmpty(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork()
		l, err := n.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()

		type acceptResult struct {
			c   net.Conn
			err error
		}
		result := make(chan acceptResult, 1)
		go func() {
			c, err := l.Accept()
			result <- acceptResult{c, err}
		}()

		synctest.Wait()

		select {
		case r := <-result:
			t.Fatalf("Accept returned early: (%v, %v)", r.c, r.err)
		default:
		}

		// Unblock so the bubble can exit cleanly.
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
		r := <-result
		if !errors.Is(r.err, net.ErrClosed) {
			t.Fatalf("Accept after Close = %v, want errors.Is(net.ErrClosed)", r.err)
		}
	})
}

func TestCloseUnblocksAccept(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork()
		l, err := n.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}

		type acceptResult struct {
			c   net.Conn
			err error
		}
		result := make(chan acceptResult, 1)
		go func() {
			c, err := l.Accept()
			result <- acceptResult{c, err}
		}()

		synctest.Wait()
		select {
		case r := <-result:
			t.Fatalf("Accept returned before Close: (%v, %v)", r.c, r.err)
		default:
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
		if r := <-result; !errors.Is(r.err, net.ErrClosed) {
			t.Fatalf("Accept after Close = %v, want errors.Is(net.ErrClosed)", r.err)
		}
	})
}

func TestAcceptAfterClose(t *testing.T) {
	n := NewNetwork()
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept after Close = %v, want errors.Is(net.ErrClosed)", err)
	}
}

func TestCloseReleasesAddr(t *testing.T) {
	n := NewNetwork()
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	l2, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatalf("Listen after Close of the same addr = %v, want nil", err)
	}
	_ = l2.Close()
}

func TestBacklogFull(t *testing.T) {
	n := NewNetwork()
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	ln := l.(*listener)
	for i := 0; i < cap(ln.incoming); i++ {
		if err := ln.enqueue(dummyConn()); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if err := ln.enqueue(dummyConn()); !errors.Is(err, ErrBacklogFull) {
		t.Fatalf("enqueue past capacity = %v, want errors.Is(ErrBacklogFull)", err)
	}
}

func dummyConn() *conn {
	c, _ := newConnPair(&addr{network: "tcp", peer: "x"}, &addr{network: "tcp", peer: "y"}, 0, "tcp")
	return c
}

func TestListenerSatisfiesNetListener(_ *testing.T) {
	var _ net.Listener = (*listener)(nil)
}

// TestFillAfterCloseClosesTheConn covers the window between a dial's
// reserve and its fill: if the listener closes in between, fill must not
// queue the conn on a listener nobody will Accept from again; it closes
// the conn instead, so the dialer's next Read/Write reports a closed
// connection rather than waiting forever on a peer that never existed.
func TestFillAfterCloseClosesTheConn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork()
		nl, err := n.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		l := nl.(*listener)

		if err := l.reserve(); err != nil {
			t.Fatal(err)
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}

		client, server := newTestConnPair()
		defer func() { _ = client.Close() }()
		l.fill(server)

		if _, err := client.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
			t.Fatalf("dialer Read after fill on a closed listener = %v, want io.EOF (the accept side was closed)", err)
		}
		if _, err := server.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Write on the conn fill closed = %v, want net.ErrClosed", err)
		}
		if _, err := l.Accept(); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Accept after Close = %v, want net.ErrClosed", err)
		}
	})
}
