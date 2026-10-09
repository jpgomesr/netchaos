package netchaos

import (
	"context"
	"io"
	"net"
	"testing"
	"testing/synctest"
)

// Compile-time assertions that the v1 API surface (docs/04-api-design.md) is
// honoured.
var _ net.Conn = (*conn)(nil)

// TestDialAssignableAsDialFunc asserts Network.Dial has exactly the shape of
// net.Dial: func(network, addr string) (net.Conn, error). This is the whole
// point of Dial's signature — a caller can pass it anywhere a net.Dial-shaped
// function is accepted without adapting it. That is not every dialer slot:
// http.Transport.DialContext and grpc.WithContextDialer take a context and
// need DialContext instead (see the tests below, issue #108).
func TestDialAssignableAsDialFunc(_ *testing.T) {
	n := NewNetwork()
	_ = (func(network, addr string) (net.Conn, error))(n.Dial)
}

// TestDialerForAssignableAsDialFunc: DialerFor's closure has net.Dial's
// shape too, the same as Dial.
func TestDialerForAssignableAsDialFunc(_ *testing.T) {
	n := NewNetwork()
	_ = (func(network, addr string) (net.Conn, error))(n.DialerFor("client"))
}

// TestDialContextAssignableToHTTPTransport: DialContext is the one entry
// point with http.Transport.DialContext's three-argument shape
// (func(ctx, network, addr string)), so it goes into
// http.Transport{DialContext: n.DialContext} unadapted (issue #108).
func TestDialContextAssignableToHTTPTransport(_ *testing.T) {
	n := NewNetwork()
	_ = (func(ctx context.Context, network, addr string) (net.Conn, error))(n.DialContext)
}

// TestGRPCContextDialerNeedsAdapter: grpc.WithContextDialer takes
// func(ctx, addr string), a shape none of netchaos's dialers have, so it
// needs a one-line adapter fixing the network (issue #108). Pins that the
// adapter the skill and docs/04 show compiles.
func TestGRPCContextDialerNeedsAdapter(_ *testing.T) {
	n := NewNetwork()
	_ = (func(ctx context.Context, addr string) (net.Conn, error))(func(ctx context.Context, addr string) (net.Conn, error) {
		return n.DialContext(ctx, "tcp", addr)
	})
}

// TestPlainDialIgnoresPartition pins what the skill used to get wrong
// (issue #108): an unnamed Dial is never partition-targetable, so a
// partition naming "client" neither blocks its dial nor drops its writes.
// Only a named dial (DialerFor, WithPeerName) is subject to the partition.
func TestPlainDialIgnoresPartition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork(WithPartition("client", "server"))
		l, err := n.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()
		accepted := make(chan net.Conn, 1)
		go func() {
			if s, err := l.Accept(); err == nil {
				accepted <- s
			}
		}()

		client, err := n.Dial("tcp", "server")
		if err != nil {
			t.Fatalf("plain Dial with WithPartition(\"client\", \"server\") = %v, want a connection", err)
		}
		defer func() { _ = client.Close() }()
		server := <-accepted
		defer func() { _ = server.Close() }()

		if _, err := client.Write([]byte("hi")); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 2)
		if _, err := io.ReadFull(server, buf); err != nil || string(buf) != "hi" {
			t.Fatalf("server read = %q, %v; want \"hi\", nil", buf, err)
		}
	})
}

func TestConnSatisfiesNetConn(t *testing.T) {
	client, server := newConnPair(&addr{network: "tcp", peer: "client"}, &addr{network: "tcp", peer: "server"}, 0, "tcp")
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	msg := []byte("ping")
	n, err := client.Write(msg)
	if err != nil || n != len(msg) {
		t.Fatalf("Write = (%d, %v), want (%d, nil)", n, err, len(msg))
	}
	buf := make([]byte, len(msg))
	n, err = server.Read(buf)
	if err != nil || n != len(msg) || string(buf) != string(msg) {
		t.Fatalf("Read = (%d, %q, %v), want (%d, %q, nil)", n, buf, err, len(msg), msg)
	}
}
