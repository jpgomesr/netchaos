package netchaos

import (
	"errors"
	"io"
	"os"
	"testing"
	"testing/synctest"
	"time"
)

func TestCloseDeliversInFlightThenEOF(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  []Option
		delay time.Duration
	}{
		{"latency", []Option{WithLatency(10*time.Millisecond, 10*time.Millisecond)}, 10 * time.Millisecond},
		{"bandwidth", []Option{WithBandwidth(400)}, 10 * time.Millisecond},
		{"both", []Option{WithBandwidth(400), WithLatency(10*time.Millisecond, 10*time.Millisecond)}, 20 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client, server := dialNamedPair(t, NewNetwork(tc.opts...))
				if _, err := client.Write([]byte("done")); err != nil {
					t.Fatal(err)
				}
				if err := client.Close(); err != nil {
					t.Fatal(err)
				}
				start := time.Now()
				got, err := io.ReadAll(server)
				if err != nil || string(got) != "done" || time.Since(start) != tc.delay {
					t.Fatalf("ReadAll = (%q, %v) at %v, want done at %v", got, err, time.Since(start), tc.delay)
				}
			})
		})
	}
}

func TestCloseEOFTimingAfterMultipleWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, server := dialNamedPair(t, NewNetwork(WithBandwidth(100)))
		for _, b := range []byte{'a', 'b'} {
			if _, err := client.Write([]byte{b}); err != nil {
				t.Fatal(err)
			}
		}
		_ = client.Close()
		start := time.Now()
		for i, want := range []byte{'a', 'b'} {
			var got [1]byte
			if _, err := io.ReadFull(server, got[:]); err != nil || got[0] != want {
				t.Fatalf("read %d = (%q, %v), want %q", i, got, err, want)
			}
			if elapsed := time.Since(start); elapsed != time.Duration(i+1)*10*time.Millisecond {
				t.Fatalf("read %d at %v, want %v", i, elapsed, time.Duration(i+1)*10*time.Millisecond)
			}
		}
		var buf [1]byte
		if _, err := server.Read(buf[:]); !errors.Is(err, io.EOF) || time.Since(start) != 20*time.Millisecond {
			t.Fatalf("EOF = %v at %v, want EOF at 20ms", err, time.Since(start))
		}
	})
}

func TestCloseKeepsDeliveryTimerUntilReaderGetsData(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, server := dialNamedPair(t, NewNetwork(WithLatency(time.Hour, time.Hour)))
		if _, err := client.Write([]byte("last")); err != nil {
			t.Fatal(err)
		}
		p := client.(*conn).writePipe
		_ = client.Close()
		p.mu.Lock()
		pending, armed := len(p.pending), p.timer != nil
		p.mu.Unlock()
		if pending != 1 || !armed {
			t.Fatalf("after Close: pending=%d timer armed=%v, want 1 and true", pending, armed)
		}
		start := time.Now()
		got, err := io.ReadAll(server)
		if err != nil || string(got) != "last" || time.Since(start) != time.Hour {
			t.Fatalf("ReadAll = (%q, %v) at %v, want last after 1h", got, err, time.Since(start))
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if len(p.pending) != 0 || p.timer != nil {
			t.Fatal("delivery timer remained after the final pending unit")
		}
	})
}

func TestClosingReaderAbortsPendingDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, server := dialNamedPair(t, NewNetwork(WithLatency(time.Hour, time.Hour)))
		if _, err := client.Write([]byte("unread")); err != nil {
			t.Fatal(err)
		}
		p := client.(*conn).writePipe
		_ = server.Close()
		p.mu.Lock()
		defer p.mu.Unlock()
		if len(p.pending) != 0 || len(p.readable) != 0 || p.timer != nil || p.bufBytes != 0 {
			t.Fatal("closing the reader retained queued data or its timer")
		}
	})
}

func TestCloseFINWaitsForHeal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork(WithLatency(10*time.Millisecond, 10*time.Millisecond))
		client, server := dialNamedPair(t, n)
		if _, err := client.Write([]byte("in flight")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
		n.Partition("client", "server")
		_ = client.Close()
		start := time.Now()
		buf := make([]byte, len("in flight"))
		if _, err := io.ReadFull(server, buf); err != nil || string(buf) != "in flight" || time.Since(start) != 5*time.Millisecond {
			t.Fatalf("in-flight read = (%q, %v) at %v, want delivery 5ms after partition", buf, err, time.Since(start))
		}
		if err := server.SetReadDeadline(time.Now().Add(5 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		if _, err := server.Read(buf); !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("Read while FIN partitioned = %v, want deadline", err)
		}
		n.Heal("client", "server")
		if err := server.SetReadDeadline(time.Time{}); err != nil {
			t.Fatal(err)
		}
		if _, err := server.Read(buf); !errors.Is(err, io.EOF) {
			t.Fatalf("Read after Heal = %v, want EOF", err)
		}
	})
}

func TestObservedEOFIsNotRetractedByPartition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork()
		client, server := dialNamedPair(t, n)
		_ = client.Close()
		var buf [1]byte
		if _, err := server.Read(buf[:]); !errors.Is(err, io.EOF) {
			t.Fatalf("first read = %v, want EOF", err)
		}
		n.Partition("client", "server")
		if err := server.SetReadDeadline(time.Now().Add(time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		if _, err := server.Read(buf[:]); !errors.Is(err, io.EOF) {
			t.Fatalf("read after Partition = %v, want persistent EOF", err)
		}
	})
}

func TestResetAbortsClosedWritersPendingDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork(WithLatency(time.Hour, time.Hour))
		client, _ := dialNamedPair(t, n)
		if _, err := client.Write([]byte("discard on reset")); err != nil {
			t.Fatal(err)
		}
		_ = client.Close()
		n.Reset("client", "server")
		p := client.(*conn).writePipe
		p.mu.Lock()
		pending, timer := len(p.pending), p.timer
		p.mu.Unlock()
		if pending != 0 || timer != nil {
			t.Fatalf("reset left %d pending units and timer %v", pending, timer)
		}
	})
}
