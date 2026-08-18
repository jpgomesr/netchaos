package netchaos

import (
	"errors"
	"net"
	"testing"
	"testing/synctest"
	"time"
)

func TestListenRegistersAddr(t *testing.T) {
	n := NewNetwork()
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	if got, want := l.Addr().String(), "server"; got != want {
		t.Fatalf("Addr() = %q, want %q", got, want)
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
	started := make(chan struct{})
	go func() {
		close(started)
		c, err := l.Accept()
		result <- acceptResult{c, err}
	}()

	<-started
	time.Sleep(20 * time.Millisecond)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case r := <-result:
		if !errors.Is(r.err, net.ErrClosed) {
			t.Fatalf("Accept after Close = %v, want errors.Is(net.ErrClosed)", r.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Accept did not unblock after Close")
	}
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
	c, _ := newConnPair(&addr{"tcp", "x"}, &addr{"tcp", "y"}, 0, "tcp")
	return c
}

func TestListenerSatisfiesNetListener(t *testing.T) {
	var _ net.Listener = (*listener)(nil)
}
