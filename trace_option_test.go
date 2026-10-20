package netchaos

import (
	"bytes"
	"io"
	"reflect"
	"testing"
	"unsafe"
)

func TestWithTraceFalseRetainsNoHistoryForMillionWrites(t *testing.T) {
	n := NewNetwork(WithTrace(false))
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	client, err := n.Dial("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	server, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	if c := client.(*conn); c.writePipe.trace != nil {
		t.Fatal("dialer recorder allocated")
	}
	if c := server.(*conn); c.writePipe.trace != nil {
		t.Fatal("acceptor recorder allocated")
	}
	drained := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, server); drained <- err }()
	for i := 0; i < 1_000_000; i++ {
		if _, err := client.Write([]byte{'x'}); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-drained; err != nil {
		t.Fatal(err)
	}
	if got := n.Trace(); len(got) != 0 {
		t.Fatalf("Trace retained %d events", len(got))
	}
	retained := uintptr(cap(n.traces)) * unsafe.Sizeof(traceHandle{})
	for _, h := range n.traces {
		retained += uintptr(cap(h.rec.events)) * unsafe.Sizeof(faultEvent{})
	}
	if retained >= 1<<20 {
		t.Fatalf("trace-related retained memory = %d bytes, want < 1 MB", retained)
	}
}

func TestWithTraceDoesNotChangeFaultResultsOrDefault(t *testing.T) {
	run := func(opts ...Option) ([]byte, []FaultEvent) {
		t.Helper()
		n := NewNetwork(append([]Option{WithSeed(2027), WithPacketLoss(.25), WithDuplication(.4), WithCorruption(.3)}, opts...)...)
		l, err := n.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()
		client, err := n.Dial("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		server, err := l.Accept()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = server.Close() }()
		for i := 0; i < 100; i++ {
			if _, err := client.Write([]byte{byte(i)}); err != nil {
				t.Fatal(err)
			}
		}
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
		payload, err := io.ReadAll(server)
		if err != nil {
			t.Fatal(err)
		}
		return payload, n.Trace()
	}
	defaultPayload, defaultTrace := run()
	truePayload, trueTrace := run(WithTrace(true))
	falsePayload, falseTrace := run(WithTrace(false))
	if !bytes.Equal(defaultPayload, truePayload) || !reflect.DeepEqual(defaultTrace, trueTrace) {
		t.Fatal("WithTrace(true) changed default results or trace")
	}
	if !bytes.Equal(defaultPayload, falsePayload) || len(falseTrace) != 0 {
		t.Fatal("WithTrace(false) changed fault results or retained trace")
	}
}
