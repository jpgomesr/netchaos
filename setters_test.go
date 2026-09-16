package netchaos

import (
	"io"
	"math"
	"math/bits"
	"reflect"
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
// names the offending call and value -- the setter's own name (issue #76),
// not the Option constructor it shares validation logic with.
func TestSettersPanicOnInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		call    func(*Network)
		wantMsg string
	}{
		{"loss above 1", func(n *Network) { n.SetPacketLoss(1.5) }, "SetPacketLoss"},
		{"loss below 0", func(n *Network) { n.SetPacketLoss(-0.1) }, "SetPacketLoss"},
		{"latency min above max", func(n *Network) { n.SetLatency(2*time.Second, time.Second) }, "SetLatency"},
		{"negative latency", func(n *Network) { n.SetLatency(-time.Second, time.Second) }, "SetLatency"},
		{"duplication above 1", func(n *Network) { n.SetDuplication(1.5) }, "SetDuplication"},
		{"duplication below 0", func(n *Network) { n.SetDuplication(-0.1) }, "SetDuplication"},
		{"duplication NaN", func(n *Network) { n.SetDuplication(math.NaN()) }, "SetDuplication"},
		{"corruption above 1", func(n *Network) { n.SetCorruption(1.5) }, "SetCorruption"},
		{"corruption below 0", func(n *Network) { n.SetCorruption(-0.1) }, "SetCorruption"},
		{"corruption NaN", func(n *Network) { n.SetCorruption(math.NaN()) }, "SetCorruption"},
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
			n.SetDuplication(float64(i%2) * 0.5)
			n.SetCorruption(float64(i%2) * 0.5)
		}
	}()

	wg.Wait()
	close(done)
	reader.Wait()
}

// TestSetDuplicationAppliesToLiveConn is the duplication half of #85 (M9-4):
// SetDuplication reaches a connection that already exists, the same live
// semantics SetLatency/SetPacketLoss/Partition/Heal already have.
func TestSetDuplicationAppliesToLiveConn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork()
		client, server := dialPair(t, n)

		if _, err := client.Write([]byte("ab")); err != nil {
			t.Fatal(err)
		}

		n.SetDuplication(1.0)
		if _, err := client.Write([]byte("cd")); err != nil {
			t.Fatal(err)
		}

		n.SetDuplication(0.0)
		if _, err := client.Write([]byte("ef")); err != nil {
			t.Fatal(err)
		}

		synctest.Wait()
		buf := make([]byte, 8)
		if _, err := io.ReadFull(server, buf); err != nil {
			t.Fatal(err)
		}
		if got, want := string(buf), "abcdcdef"; got != want {
			t.Fatalf("read %q, want %q (the middle write must be admitted twice by the live SetDuplication(1.0))", got, want)
		}
	})
}

// TestSetCorruptionAppliesToLiveConn is the corruption half of #85 (M9-4):
// SetCorruption reaches a connection that already exists.
func TestSetCorruptionAppliesToLiveConn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork()
		client, server := dialPair(t, n)

		payload := []byte("healthy!")
		if _, err := client.Write(payload); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		buf := make([]byte, len(payload))
		if _, err := io.ReadFull(server, buf); err != nil {
			t.Fatal(err)
		}
		if string(buf) != string(payload) {
			t.Fatalf("read %q before SetCorruption, want %q unmodified", buf, payload)
		}

		n.SetCorruption(1.0)
		corrupted := []byte("corrupted")
		if _, err := client.Write(corrupted); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		got := make([]byte, len(corrupted))
		if _, err := io.ReadFull(server, got); err != nil {
			t.Fatal(err)
		}
		diffBits := 0
		for i := range got {
			diffBits += bits.OnesCount8(got[i] ^ corrupted[i])
		}
		if diffBits != 1 {
			t.Fatalf("delivered payload differs from written in %d bits, want exactly 1 "+
				"(SetCorruption(1.0) flips a single bit)", diffBits)
		}

		n.SetCorruption(0.0)
		healthyAgain := []byte("healthy2")
		if _, err := client.Write(healthyAgain); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		final := make([]byte, len(healthyAgain))
		if _, err := io.ReadFull(server, final); err != nil {
			t.Fatal(err)
		}
		if string(final) != string(healthyAgain) {
			t.Fatalf("read %q after SetCorruption(0.0), want %q unmodified", final, healthyAgain)
		}
	})
}

// TestDuplicationCorruptionSettersDeterministic is #85's determinism
// requirement: two Networks with the same seed and the same call order
// (dial, write, SetDuplication, SetCorruption, more writes) produce
// identical traces.
func TestDuplicationCorruptionSettersDeterministic(t *testing.T) {
	run := func(t *testing.T) []FaultEvent {
		n := NewNetwork(WithSeed(11))
		client, _ := dialNamedPair(t, n)

		for i := 0; i < 10; i++ {
			if i == 5 {
				n.SetDuplication(0.5)
				n.SetCorruption(0.5)
			}
			if _, err := client.Write([]byte{byte(i)}); err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()
		return n.Trace()
	}

	var a, b []FaultEvent
	synctest.Test(t, func(t *testing.T) { a = run(t) })
	synctest.Test(t, func(t *testing.T) { b = run(t) })

	if !reflect.DeepEqual(a, b) {
		t.Fatalf("traces differ across identical runs:\na = %+v\nb = %+v", a, b)
	}
}
