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
