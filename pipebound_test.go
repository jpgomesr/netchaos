package netchaos

// M7-6: WithPipeBound and WithListenerBacklog. Both are structural knobs
// that already existed as package constants (defaultPipeBound, pipe.go;
// listenerBacklog, listener.go) reachable only from in-package tests --
// newConnPairWithBound (conn.go) is the direct evidence the bound mattered
// to at least one kind of test before this. These options make both
// settable from outside the package, validated by the same
// networkConfig.validate() pass every other option goes through.

import (
	"errors"
	"net"
	"testing"
	"time"
)

// TestWithPipeBoundAppliesBackPressure exercises the back-pressure claim
// through a throttled connection (M7-5's WithBandwidth), the real scenario
// #52 named rather than a synthetic one: a slow enough link plus a small
// enough bound makes a Write block on an unread connection, at the
// configured size rather than the 64 KiB default.
func TestWithPipeBoundAppliesBackPressure(t *testing.T) {
	const bound = 16
	const bps = 1_000_000 // fast enough that throttling itself isn't the delay under test

	n := NewNetwork(WithPipeBound(bound), WithBandwidth(bps))
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

	client, err := n.Dial("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	server := <-accepted
	defer func() { _ = server.Close() }()

	// The first write fits exactly at the bound and must not block.
	if _, err := client.Write(make([]byte, bound)); err != nil {
		t.Fatalf("first write (fills the bound exactly) = %v, want nil", err)
	}

	// A second write, unread, must block: the direction is already at the
	// configured bound of 16 bytes, not the 64 KiB default.
	blocked := make(chan struct{})
	go func() {
		_, _ = client.Write(make([]byte, 1))
		close(blocked)
	}()

	select {
	case <-blocked:
		t.Fatal("second write returned before anything was read; back-pressure did not apply at the configured bound")
	case <-time.After(50 * time.Millisecond):
	}

	// Reading the first write's bytes frees exactly enough room for the
	// second, one-byte write to be admitted.
	buf := make([]byte, bound)
	if _, err := server.Read(buf); err != nil {
		t.Fatal(err)
	}

	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("second write did not unblock after the first was read")
	}
}

// TestWithListenerBacklogBoundsAcceptQueue mirrors
// TestBacklogFullOrdinalAccounting (dial_test.go) at a configured backlog
// smaller than the package default, confirming ErrBacklogFull triggers at
// the configured size rather than the default 128.
func TestWithListenerBacklogBoundsAcceptQueue(t *testing.T) {
	const backlog = 4

	n := NewNetwork(WithListenerBacklog(backlog))
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	for i := 0; i < backlog; i++ {
		c, err := n.Dial("tcp", "server")
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		defer func() { _ = c.Close() }()
	}

	if _, err := n.Dial("tcp", "server"); !errors.Is(err, ErrBacklogFull) {
		t.Fatalf("dial past the configured backlog of %d = %v, want errors.Is(ErrBacklogFull)", backlog, err)
	}
}

// TestInvalidBoundPanics covers both new options' panic-on-invalid
// convention, matching every other Option (M0-5).
func TestInvalidBoundPanics(t *testing.T) {
	cases := []struct {
		name string
		opt  Option
		want []string
	}{
		{"pipe bound zero", WithPipeBound(0), []string{"WithPipeBound", "0"}},
		{"pipe bound negative", WithPipeBound(-1), []string{"WithPipeBound", "-1"}},
		{"listener backlog zero", WithListenerBacklog(0), []string{"WithListenerBacklog", "0"}},
		{"listener backlog negative", WithListenerBacklog(-1), []string{"WithListenerBacklog", "-1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expectPanic(t, c.want, func() {
				NewNetwork(c.opt)
			})
		})
	}
}

// TestUnconfiguredBoundsUseDefaults confirms a Network built without either
// option keeps behaving exactly as before this task: the package constants
// defaultPipeBound and listenerBacklog are still what a plain NewNetwork()
// uses.
func TestUnconfiguredBoundsUseDefaults(t *testing.T) {
	n := NewNetwork()
	if n.pipeBound != defaultPipeBound {
		t.Errorf("pipeBound = %d, want the default %d", n.pipeBound, defaultPipeBound)
	}
	if n.listenerBacklog != listenerBacklog {
		t.Errorf("listenerBacklog = %d, want the default %d", n.listenerBacklog, listenerBacklog)
	}
}
