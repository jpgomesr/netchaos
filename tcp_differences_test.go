package netchaos

import (
	"errors"
	"io"
	"os"
	"testing"
	"testing/synctest"
	"time"
)

func TestDialDoesNotPayConfiguredLatency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		network := NewNetwork(WithLatency(time.Second, time.Second))
		listener, err := network.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = listener.Close() }()
		start := time.Now()
		client, err := network.Dial("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = client.Close() }()
		if elapsed := time.Since(start); elapsed != 0 {
			t.Fatalf("Dial took %v, want no configured latency on establishment", elapsed)
		}
		server, err := listener.Accept()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = server.Close() }()
	})
}

// A zero-length read on an open, empty connection waits like net.Pipe,
// unlike a real TCP connection's immediate (0, nil) result.
func TestZeroLengthReadWaitsForDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, _ := dialPair(t, NewNetwork())
		if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		n, err := client.Read(nil)
		if n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("Read(nil) = (%d, %v), want (0, deadline exceeded)", n, err)
		}
		if elapsed := time.Since(start); elapsed != time.Second {
			t.Fatalf("Read(nil) waited %v, want one virtual second", elapsed)
		}
	})
}

// The default pipe bound limits bytes in flight under latency, even when
// the reader continuously drains every delivered byte.
func TestDefaultPipeBoundActsAsInFlightWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		network := NewNetwork(WithLatency(100*time.Millisecond, 100*time.Millisecond))
		client, server := dialPair(t, network)
		const total = 1 << 20
		written := make(chan error, 1)
		go func() {
			chunk := make([]byte, 1024)
			for sent := 0; sent < total; sent += len(chunk) {
				if _, err := client.Write(chunk); err != nil {
					written <- err
					return
				}
			}
			written <- nil
		}()
		start := time.Now()
		if _, err := io.CopyN(io.Discard, server, total); err != nil {
			t.Fatal(err)
		}
		if err := <-written; err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed < time.Second {
			t.Fatalf("1 MiB transfer took %v, want sustained back-pressure beyond one latency interval", elapsed)
		}
	})
}

func TestDroppedAndDuplicatedUnitsDoNotAddLinkTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dropped := NewNetwork(WithPacketLoss(1), WithBandwidth(1))
		client, _ := dialPair(t, dropped)
		if _, err := client.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
		events := dropped.Trace()
		if len(events) != 1 || !events[0].Dropped || events[0].Serialization != 0 {
			t.Fatalf("dropped unit used link time: %+v", events)
		}

		duplicated := NewNetwork(WithDuplication(1), WithBandwidth(1))
		writer, reader := dialPair(t, duplicated)
		if _, err := writer.Write([]byte("y")); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		data := make([]byte, 2)
		if _, err := io.ReadFull(reader, data); err != nil {
			t.Fatal(err)
		}
		if string(data) != "yy" || time.Since(start) != time.Second {
			t.Fatalf("duplicate = %q after %v, want two copies after one second", data, time.Since(start))
		}
	})
}
