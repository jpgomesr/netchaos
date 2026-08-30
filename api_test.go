package netchaos

import (
	"net"
	"testing"
)

// Compile-time assertions that the frozen v1 API surface (docs/04-api-design.md)
// is honoured.
var _ net.Conn = (*conn)(nil)

// TestDialAssignableAsDialFunc asserts Network.Dial has exactly the shape of
// net.Dial: func(network, addr string) (net.Conn, error). This is the whole
// point of Dial's signature — a caller can pass it anywhere a dial function
// is accepted (an HTTP transport, a gRPC dialer, ...) without adapting it.
func TestDialAssignableAsDialFunc(t *testing.T) {
	n := NewNetwork()
	_ = (func(network, addr string) (net.Conn, error))(n.Dial)
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
