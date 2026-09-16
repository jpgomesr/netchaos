package netchaos

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// TestDialerForWithDialTimeoutFailsOnPartition is F5's headline claim
// (M9-2, issue #86): a DialerFor dialer given WithDialTimeout fails fast,
// after exactly the configured virtual duration, instead of hanging until
// Heal -- unlike plain DialerFor(name), which has no context to bound the
// wait with.
func TestDialerForWithDialTimeoutFailsOnPartition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork()
		l, err := n.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()

		n.Partition("client", "server")

		dial := n.DialerFor("client", WithDialTimeout(time.Second))

		start := time.Now()
		_, err = dial("tcp", "server")
		if elapsed := time.Since(start); elapsed != time.Second {
			t.Fatalf("dial returned after %v virtual time, want exactly the 1s timeout", elapsed)
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("dial error = %v, want errors.Is(context.DeadlineExceeded)", err)
		}
		var opErr *net.OpError
		if !errors.As(err, &opErr) {
			t.Fatalf("dial error = %v (%T), want a *net.OpError (M6-2's uniform shape)", err, err)
		}
		if !opErr.Timeout() {
			t.Fatalf("opErr.Timeout() = false, want true for a context.DeadlineExceeded-wrapped error")
		}
	})
}

// TestDialerForTimeoutIsPerCall confirms the timeout is scoped to each dial
// attempt, not accumulated or shared across calls from the same DialerFor
// closure: a call that times out against a still-partitioned peer does not
// poison a later call made after Heal.
func TestDialerForTimeoutIsPerCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork()
		l, err := n.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()

		n.Partition("client", "server")
		dial := n.DialerFor("client", WithDialTimeout(time.Second))

		if _, err := dial("tcp", "server"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("first dial = %v, want errors.Is(context.DeadlineExceeded)", err)
		}

		n.Heal("client", "server")

		accepted := make(chan net.Conn, 1)
		go func() {
			c, err := l.Accept()
			if err == nil {
				accepted <- c
			}
		}()

		conn, err := dial("tcp", "server")
		if err != nil {
			t.Fatalf("second dial after Heal = %v, want nil (timeout must not accumulate across calls)", err)
		}
		defer func() { _ = conn.Close() }()
		synctest.Wait()
		select {
		case c := <-accepted:
			_ = c.Close()
		default:
			t.Fatal("second dial did not reach the listener")
		}
	})
}

// TestWithDialTimeoutPanicsOnNonPositive holds WithDialTimeout to the same
// panic-on-invalid convention every other constructor in the package uses.
func TestWithDialTimeoutPanicsOnNonPositive(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
	}{
		{"zero", 0},
		{"negative", -time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("no panic, want one naming WithDialTimeout and the offending value")
				}
				msg, ok := r.(string)
				if !ok || !strings.Contains(msg, "WithDialTimeout") {
					t.Fatalf("panic = %v, want a message mentioning %q", r, "WithDialTimeout")
				}
			}()
			WithDialTimeout(tt.d)
		})
	}
}

// TestDialerForNoOptionKeepsDialShape is a compile-time-shaped check that
// DialerFor's variadic signature doesn't break existing no-option callers:
// n.DialerFor("client") must keep compiling and keep being assignable to
// net.Dial's shape.
func TestDialerForNoOptionKeepsDialShape(_ *testing.T) {
	n := NewNetwork()
	_ = (func(network, addr string) (net.Conn, error))(n.DialerFor("client"))
}
