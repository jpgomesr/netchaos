package netchaos

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

// TestErrorSentinels exercises every documented failure mode in errors.go
// and confirms it is matchable with errors.Is against the sentinel errors.go
// says it uses.
func TestErrorSentinels(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{
			name: "unsupported network on Listen",
			err:  func() error { _, err := NewNetwork().Listen("udp", "server"); return err }(),
			want: ErrUnsupportedNetwork,
		},
		{
			name: "unsupported network on Dial",
			err:  func() error { _, err := NewNetwork().Dial("udp", "server"); return err }(),
			want: ErrUnsupportedNetwork,
		},
		{
			name: "connection refused",
			err:  func() error { _, err := NewNetwork().Dial("tcp", "nobody"); return err }(),
			want: ErrConnectionRefused,
		},
		{
			name: "address in use",
			err: func() error {
				n := NewNetwork()
				l, err := n.Listen("tcp", "server")
				if err != nil {
					return err
				}
				defer func() { _ = l.Close() }()
				_, err = n.Listen("tcp", "server")
				return err
			}(),
			want: ErrAddressInUse,
		},
		{
			name: "backlog full",
			err: func() error {
				n := NewNetwork()
				l, err := n.Listen("tcp", "server")
				if err != nil {
					return err
				}
				defer func() { _ = l.Close() }()
				ln := l.(*listener)
				for i := 0; i < cap(ln.incoming); i++ {
					if err := ln.enqueue(dummyConn()); err != nil {
						return err
					}
				}
				return ln.enqueue(dummyConn())
			}(),
			want: ErrBacklogFull,
		},
		{
			name: "use of closed conn",
			err: func() error {
				client, server := newTestConnPair()
				defer func() { _ = server.Close() }()
				_ = client.Close()
				_, err := client.Write([]byte("x"))
				return err
			}(),
			want: net.ErrClosed,
		},
		{
			name: "use of closed listener",
			err: func() error {
				n := NewNetwork()
				l, err := n.Listen("tcp", "server")
				if err != nil {
					return err
				}
				_ = l.Close()
				_, err = l.Accept()
				return err
			}(),
			want: net.ErrClosed,
		},
		{
			name: "read deadline exceeded",
			err: func() error {
				client, server := newTestConnPair()
				defer func() { _ = client.Close() }()
				defer func() { _ = server.Close() }()
				_ = server.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
				_, err := server.Read(make([]byte, 1))
				return err
			}(),
			want: os.ErrDeadlineExceeded,
		},
		{
			name: "dial context cancelled",
			err: func() error {
				n := NewNetwork()
				l, err := n.Listen("tcp", "server")
				if err != nil {
					return err
				}
				defer func() { _ = l.Close() }()
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				_, err = n.DialContext(ctx, "tcp", "server")
				return err
			}(),
			want: context.Canceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !errors.Is(tt.err, tt.want) {
				t.Fatalf("err = %v, want errors.Is(%v)", tt.err, tt.want)
			}
		})
	}
}

func TestUseAfterCloseNoPanic(t *testing.T) {
	n := NewNetwork()
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	client, server := newTestConnPair()

	_ = client.Close()
	_ = client.Close()
	_ = server.Close()
	_ = l.Close()
	_ = l.Close()

	if _, err := client.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Read after Close = %v, want errors.Is(net.ErrClosed)", err)
	}
	if _, err := client.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Write after Close = %v, want errors.Is(net.ErrClosed)", err)
	}
	if _, err := l.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept after Close = %v, want errors.Is(net.ErrClosed)", err)
	}
}

// TestErrorsAreUniformlyOpError is M6-2's decision made testable: every error
// leaving Listen, Dial and DialContext is a *net.OpError, matching what real
// net.Listen and net.Dial return for the same failures.
//
// Before this, the shape depended on which line produced the error, and the
// split followed no rule anyone could state -- a dial refusal was wrapped, a
// bad network was not, Listen's address-in-use was not. Code under test that
// type-asserts to *net.OpError, or calls Timeout()/Temporary() on the result,
// therefore behaved differently against netchaos than against the standard
// library, which cuts against the substitutability claim the library is sold
// on.
//
// errors.Is against every sentinel keeps working because OpError unwraps;
// that is what makes the change safe for the comparison style the package's
// own docs and tests use.
func TestErrorsAreUniformlyOpError(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name    string
		call    func(n *Network) error
		wantOp  string
		wantIs  error
		wantNet string
	}{
		{
			name:    "Listen with an unsupported network",
			call:    func(n *Network) error { _, err := n.Listen("udp", "server"); return err },
			wantOp:  "listen",
			wantIs:  ErrUnsupportedNetwork,
			wantNet: "udp",
		},
		{
			name: "Listen on an address already in use",
			call: func(n *Network) error {
				if _, err := n.Listen("tcp", "taken"); err != nil {
					return err
				}
				_, err := n.Listen("tcp", "taken")
				return err
			},
			wantOp:  "listen",
			wantIs:  ErrAddressInUse,
			wantNet: "tcp",
		},
		{
			name:    "Dial with an unsupported network",
			call:    func(n *Network) error { _, err := n.Dial("udp", "server"); return err },
			wantOp:  "dial",
			wantIs:  ErrUnsupportedNetwork,
			wantNet: "udp",
		},
		{
			name:    "Dial an address nobody listens on",
			call:    func(n *Network) error { _, err := n.Dial("tcp", "nobody"); return err },
			wantOp:  "dial",
			wantIs:  ErrConnectionRefused,
			wantNet: "tcp",
		},
		{
			name: "DialContext with an already-cancelled context",
			call: func(n *Network) error {
				_, err := n.DialContext(cancelled, "tcp", "server")
				return err
			},
			wantOp:  "dial",
			wantIs:  context.Canceled,
			wantNet: "tcp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(NewNetwork())
			if err == nil {
				t.Fatal("got nil, want an error")
			}

			var opErr *net.OpError
			if !errors.As(err, &opErr) {
				t.Fatalf("err = %v (%T), want a *net.OpError", err, err)
			}
			if opErr.Op != tt.wantOp {
				t.Errorf("Op = %q, want %q", opErr.Op, tt.wantOp)
			}
			if opErr.Net != tt.wantNet {
				t.Errorf("Net = %q, want %q", opErr.Net, tt.wantNet)
			}
			if opErr.Addr == nil {
				t.Error("Addr = nil, want the address the call named")
			}
			if !errors.Is(err, tt.wantIs) {
				t.Errorf("errors.Is(err, %v) = false; wrapping must not break sentinel matching", tt.wantIs)
			}
		})
	}
}
