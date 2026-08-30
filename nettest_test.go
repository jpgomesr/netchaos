package netchaos

import (
	"net"
	"testing"

	"golang.org/x/net/nettest"
)

// TestConnConformance runs the standard net.Conn conformance suite against a
// netchaos connection pair. netchaos's headline claim is that its net.Conn
// substitutes for the real one; nettest.TestConn is the harness that claim is
// normally checked against — it is what the standard library validates
// net.Pipe with — and everything the repo had before this was either a
// hand-written test or a compile-time interface assertion (api_test.go),
// which check the shape of net.Conn but not the semantics behind it.
//
// The Network here is deliberately fault-free: no WithLatency, no
// WithPacketLoss, no partition. nettest.TestConn asserts vanilla stream
// semantics that the faults exist to violate — a dropped write reports
// n = len(p), nil and never arrives (the silent-gap model from M0-3), which
// fails the suite's BasicIO by design. A failure under an injected fault
// would be the suite working correctly, not a defect in it.
func TestConnConformance(t *testing.T) {
	nettest.TestConn(t, makeNetchaosPipe)
}

// makeNetchaosPipe is nettest.MakePipe over netchaos: it stands up a fresh
// Network per invocation, so the suite's subtests cannot perturb each other's
// connection ordinals or share a listener address.
func makeNetchaosPipe() (c1, c2 net.Conn, stop func(), err error) {
	n := NewNetwork()

	l, err := n.Listen("tcp", "server")
	if err != nil {
		return nil, nil, nil, err
	}

	type accepted struct {
		c   net.Conn
		err error
	}
	acceptCh := make(chan accepted, 1)
	go func() {
		c, err := l.Accept()
		acceptCh <- accepted{c, err}
	}()

	client, err := n.Dial("tcp", "server")
	if err != nil {
		_ = l.Close()
		return nil, nil, nil, err
	}

	a := <-acceptCh
	if a.err != nil {
		_ = client.Close()
		_ = l.Close()
		return nil, nil, nil, a.err
	}

	stop = func() {
		_ = client.Close()
		_ = a.c.Close()
		_ = l.Close()
	}
	return client, a.c, stop, nil
}
