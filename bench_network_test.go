package netchaos

import (
	"io"
	"math"
	"net"
	"testing"
	"time"
)

// These benchmarks go through a Network -- Dial, the listener hand-off, the
// fault evaluator, the live-config lock and trace growth -- which is the path
// every user takes. The older ones in bench_test.go build raw conn pairs and
// install a fault policy directly, a configuration users cannot create; they
// stay as the floor for the pipe itself. Baseline results are recorded in
// docs/tasks/m11-v0.3.2-hardening.md (M11-9).
//
// Benchmarks cannot run inside a synctest bubble (synctest.Test takes a
// *testing.T), so timing faults use the smallest values that still exercise
// their stage: 1ns latency still arms and fires a real timer per unit, and a
// math.MaxInt bytes/s throttle still runs the serialization clock (MaxInt
// rather than a fixed huge constant, so it also compiles where int is 32
// bits). What these measure is
// netchaos's own overhead, not a simulated delay.

// benchDialPair dials "server" on n as the named peer "client" and returns
// both ends.
func benchDialPair(b *testing.B, n *Network) (client, server net.Conn) {
	b.Helper()
	l, err := n.Listen("tcp", "server")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = l.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := l.Accept(); err == nil {
			accepted <- c
		}
	}()
	client, err = n.DialerFor("client")("tcp", "server")
	if err != nil {
		b.Fatal(err)
	}
	server = <-accepted
	b.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	return client, server
}

// BenchmarkDialAccept measures establishing and tearing down one
// connection: Dial, the listener hand-off to Accept, and Close on both ends.
func BenchmarkDialAccept(b *testing.B) {
	n := NewNetwork()
	l, err := n.Listen("tcp", "server")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	accepted := make(chan net.Conn, 1)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c, err := n.Dial("tcp", "server")
		if err != nil {
			b.Fatal(err)
		}
		s := <-accepted
		_ = c.Close()
		_ = s.Close()
	}
}

// BenchmarkNetworkRoundTrip measures a 512-byte write and the matching read
// through a Network, fault-free and with each fault stage on. "loss" uses
// rate 0: the stage still draws for every unit (the cost being measured)
// but drops nothing, so the read never waits on a unit that will not come.
// "all" turns on every drawing stage plus bandwidth the same way.
func BenchmarkNetworkRoundTrip(b *testing.B) {
	cases := []struct {
		name string
		opts []Option
	}{
		{"nofault", nil},
		{"latency", []Option{WithLatency(time.Nanosecond, time.Nanosecond)}},
		{"loss", []Option{WithPacketLoss(0)}},
		{"bandwidth", []Option{WithBandwidth(math.MaxInt)}},
		{"all", []Option{
			WithLatency(time.Nanosecond, time.Nanosecond),
			WithPacketLoss(0),
			WithBandwidth(math.MaxInt),
			WithDuplication(0),
			WithCorruption(0),
		}},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			client, server := benchDialPair(b, NewNetwork(tc.opts...))
			payload := make([]byte, benchPayload)
			buf := make([]byte, benchPayload)

			b.ReportAllocs()
			b.SetBytes(benchPayload)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := client.Write(payload); err != nil {
					b.Fatal(err)
				}
				if _, err := io.ReadFull(server, buf); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkTrace measures one Network.Trace call over 100,000 recorded
// events -- the cost of reading back a long test's trace, including the
// sort over per-direction handles. WithPacketLoss(1) records an event per
// write without anything reaching the reader, so the setup needs no reads.
func BenchmarkTrace(b *testing.B) {
	const events = 100_000
	n := NewNetwork(WithPacketLoss(1))
	client, _ := benchDialPair(b, n)
	payload := make([]byte, 1)
	for i := 0; i < events; i++ {
		if _, err := client.Write(payload); err != nil {
			b.Fatal(err)
		}
	}
	if got := len(n.Trace()); got != events {
		b.Fatalf("setup recorded %d events, want %d", got, events)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = n.Trace()
	}
}

// BenchmarkManyConns measures a round trip on one of 1,000 open
// connections, cycling through them, so per-connection state (the
// partition and reset registries, the trace handle list) is at a realistic
// size for a test that opens many conns.
func BenchmarkManyConns(b *testing.B) {
	const conns = 1000
	n := NewNetwork()
	l, err := n.Listen("tcp", "server")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	clients := make([]net.Conn, conns)
	servers := make([]net.Conn, conns)
	for i := range clients {
		accepted := make(chan net.Conn, 1)
		go func() {
			if c, err := l.Accept(); err == nil {
				accepted <- c
			}
		}()
		c, err := n.Dial("tcp", "server")
		if err != nil {
			b.Fatal(err)
		}
		clients[i], servers[i] = c, <-accepted
	}
	defer func() {
		for i := range clients {
			_ = clients[i].Close()
			_ = servers[i].Close()
		}
	}()

	payload := make([]byte, benchPayload)
	buf := make([]byte, benchPayload)

	b.ReportAllocs()
	b.SetBytes(benchPayload)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := i % conns
		if _, err := clients[k].Write(payload); err != nil {
			b.Fatal(err)
		}
		if _, err := io.ReadFull(servers[k], buf); err != nil {
			b.Fatal(err)
		}
	}
}
