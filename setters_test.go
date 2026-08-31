package netchaos

import (
	"io"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// TestSetLatencyAppliesToLiveConn is the asymmetry #50 was filed for.
// Partition and Heal change behaviour on connections that already exist;
// latency and loss were fixed at construction, so "healthy, then degraded,
// then healthy" meant building a second Network — new connections, and a
// reset of every ordinal.
//
// M7-3 answered the question this raises before the code existed: a mid-run
// change applies to already-established connections, matching Partition.
func TestSetLatencyAppliesToLiveConn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork()
		client, server := dialPair(t, n)

		// Healthy: the write is readable without time passing.
		if _, err := client.Write([]byte("fast")); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		buf := make([]byte, 4)
		if _, err := io.ReadFull(server, buf); err != nil {
			t.Fatalf("read before SetLatency: %v", err)
		}

		// Degraded, on the connection that already exists.
		n.SetLatency(50*time.Millisecond, 50*time.Millisecond)

		start := time.Now()
		if _, err := client.Write([]byte("slow")); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(server, buf); err != nil {
			t.Fatalf("read after SetLatency: %v", err)
		}
		if elapsed := time.Since(start); elapsed != 50*time.Millisecond {
			t.Fatalf("delivery took %v after SetLatency, want exactly 50ms "+
				"(a setter must reach connections established before it, per the determinism contract)", elapsed)
		}

		// Healthy again, same connection.
		n.SetLatency(0, 0)
		start = time.Now()
		if _, err := client.Write([]byte("fast")); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(server, buf); err != nil {
			t.Fatalf("read after healing latency: %v", err)
		}
		if elapsed := time.Since(start); elapsed != 0 {
			t.Fatalf("delivery took %v after SetLatency(0, 0), want 0", elapsed)
		}
	})
}

// TestSetPacketLossAppliesToLiveConn is the loss half of the same property.
// Rate 1.0 drops everything, so the degraded write is a silent gap the
// reader never sees, while the writes on either side of it arrive.
func TestSetPacketLossAppliesToLiveConn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork()
		client, server := dialPair(t, n)

		if _, err := client.Write([]byte("aa")); err != nil {
			t.Fatal(err)
		}

		n.SetPacketLoss(1.0)
		if _, err := client.Write([]byte("XX")); err != nil {
			t.Fatal(err)
		}

		n.SetPacketLoss(0.0)
		if _, err := client.Write([]byte("bb")); err != nil {
			t.Fatal(err)
		}

		synctest.Wait()
		buf := make([]byte, 4)
		if _, err := io.ReadFull(server, buf); err != nil {
			t.Fatal(err)
		}
		if got, want := string(buf), "aabb"; got != want {
			t.Fatalf("read %q, want %q (the middle write must be dropped by the live SetPacketLoss(1.0))", got, want)
		}
	})
}

// TestSettersPanicOnInvalidValues holds the setters to the same
// panic-on-invalid convention as the options they mirror: invalid values are
// programmer errors in test code, not runtime conditions, and the message
// names the offending call and value.
func TestSettersPanicOnInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		call    func(*Network)
		wantMsg string
	}{
		{"loss above 1", func(n *Network) { n.SetPacketLoss(1.5) }, "WithPacketLoss"},
		{"loss below 0", func(n *Network) { n.SetPacketLoss(-0.1) }, "WithPacketLoss"},
		{"latency min above max", func(n *Network) { n.SetLatency(2*time.Second, time.Second) }, "WithLatency"},
		{"negative latency", func(n *Network) { n.SetLatency(-time.Second, time.Second) }, "WithLatency"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("no panic, want one naming the offending value")
				}
				msg, ok := r.(string)
				if !ok || !strings.Contains(msg, tt.wantMsg) {
					t.Fatalf("panic = %v, want a message mentioning %q", r, tt.wantMsg)
				}
			}()
			tt.call(NewNetwork())
		})
	}
}

// TestSettersRaceWithLiveIO is the -race test for the consequence #50
// accepted explicitly: the per-unit read path was lock-free, and making the
// configuration mutable adds synchronization to it. The determinism contract
// deliberately does NOT promise which unit first sees a new value here — only
// that this is race-free — so nothing about the delivered bytes is asserted.
func TestSettersRaceWithLiveIO(t *testing.T) {
	n := NewNetwork()
	client, server := dialPair(t, n)

	// The reader is drained separately from the two goroutines under test:
	// it only stops once they are done, so waiting on it in the same group
	// would deadlock — nothing would ever close done.
	done := make(chan struct{})
	var reader sync.WaitGroup
	reader.Add(1)
	go func() {
		defer reader.Done()
		buf := make([]byte, 64)
		for {
			select {
			case <-done:
				return
			default:
			}
			_ = server.SetReadDeadline(time.Now().Add(5 * time.Millisecond))
			_, _ = server.Read(buf)
		}
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_, _ = client.Write([]byte("payload"))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			n.SetPacketLoss(float64(i%2) * 0.5)
			n.SetLatency(time.Duration(i%3)*time.Millisecond, time.Duration(i%3)*time.Millisecond)
		}
	}()

	wg.Wait()
	close(done)
	reader.Wait()
}
