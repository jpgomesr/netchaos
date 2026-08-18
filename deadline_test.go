package netchaos

import (
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// --- deadline type unit tests ---

func TestDeadlineExpiredAfterPastSet(t *testing.T) {
	d := newDeadline()

	// A waiter that snapshotted the channel before the deadline was set must
	// still be woken by set(); that's the "unconditional bump" guarantee
	// SetReadDeadline(pastTime) relies on to interrupt an in-flight Read.
	waiting := d.channel()

	d.set(time.Now().Add(-time.Second))
	if !d.expired() {
		t.Fatal("expired() = false after a past deadline was set")
	}
	select {
	case <-waiting:
	default:
		t.Fatal("a channel snapshotted before set() was not woken by it")
	}
}

func TestDeadlineClearedByZeroTime(t *testing.T) {
	d := newDeadline()
	d.set(time.Now().Add(-time.Second))
	if !d.expired() {
		t.Fatal("expired() = false after a past deadline was set")
	}
	d.set(time.Time{})
	if d.expired() {
		t.Fatal("expired() = true after clearing with a zero time.Time")
	}
}

func TestDeadlineNoDeadlineNeverExpires(t *testing.T) {
	d := newDeadline()
	if d.expired() {
		t.Fatal("expired() = true with no deadline ever set")
	}
}

// --- conn-level deadline behaviour ---

func newTestConnPairWithBound(bound int) (client, server *conn) {
	return newConnPairWithBound(&addr{"tcp", "client"}, &addr{"tcp", "server"}, 0, "tcp", bound)
}

func assertDeadlineExceeded(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("err = %v, want errors.Is(os.ErrDeadlineExceeded)", err)
	}
	var nerr net.Error
	if !errors.As(err, &nerr) || !nerr.Timeout() {
		t.Fatalf("err = %v, want a net.Error with Timeout() == true", err)
	}
}

func TestReadDeadlineExceeded(t *testing.T) {
	client, server := newTestConnPair()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	if err := server.SetReadDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	n, err := server.Read(make([]byte, 4))
	if n != 0 {
		t.Fatalf("n = %d, want 0", n)
	}
	assertDeadlineExceeded(t, err)
}

func TestWriteDeadlineExceeded(t *testing.T) {
	client, server := newTestConnPairWithBound(4)
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	// Fill the bound so the next write has to block on back-pressure.
	if n, err := client.Write([]byte("abcd")); err != nil || n != 4 {
		t.Fatalf("fill write = (%d, %v), want (4, nil)", n, err)
	}
	if err := client.SetWriteDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	n, err := client.Write([]byte("e"))
	if n != 0 {
		t.Fatalf("n = %d, want 0", n)
	}
	assertDeadlineExceeded(t, err)
}

func TestDeadlineUnblocksInFlightRead(t *testing.T) {
	client, server := newTestConnPair()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		_, err := server.Read(make([]byte, 4))
		result <- err
	}()

	<-started
	time.Sleep(20 * time.Millisecond)
	if err := server.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-result:
		assertDeadlineExceeded(t, err)
	case <-time.After(time.Second):
		t.Fatal("Read did not unblock after a past deadline was set from another goroutine")
	}
}

func TestZeroTimeClearsDeadline(t *testing.T) {
	client, server := newTestConnPair()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	if err := server.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := server.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		_, err := server.Read(make([]byte, 4))
		result <- err
	}()

	time.Sleep(20 * time.Millisecond)
	if _, err := client.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Read after clearing deadline = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Read did not complete after deadline was cleared and data arrived")
	}
}

func TestSetDeadlineAffectsBothDirections(t *testing.T) {
	client, server := newTestConnPairWithBound(4)
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	if n, err := client.Write([]byte("abcd")); err != nil || n != 4 {
		t.Fatalf("fill write = (%d, %v), want (4, nil)", n, err)
	}
	if err := client.SetDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	if n, err := client.Write([]byte("e")); n != 0 {
		t.Fatalf("write n = %d, want 0", n)
	} else {
		assertDeadlineExceeded(t, err)
	}

	if _, err := client.Read(make([]byte, 4)); true {
		assertDeadlineExceeded(t, err)
	}
}

func TestPerDirectionDeadlineIsolated(t *testing.T) {
	client, server := newTestConnPairWithBound(4)
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	if err := client.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	// The write direction must be unaffected by a read-only deadline.
	if n, err := client.Write([]byte("ab")); err != nil || n != 2 {
		t.Fatalf("Write with only SetReadDeadline set = (%d, %v), want (2, nil)", n, err)
	}
}

func TestConnUsableAfterTimeout(t *testing.T) {
	client, server := newTestConnPair()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	if err := server.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Read(make([]byte, 4)); err == nil {
		t.Fatal("expected the first read to time out")
	}

	if err := server.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2)
	n, err := server.Read(buf)
	if err != nil || string(buf[:n]) != "ok" {
		t.Fatalf("read after fresh deadline = (%d, %q, %v), want (2, \"ok\", nil)", n, buf[:n], err)
	}
}

func TestDeadlineUsesVirtualTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, server := newTestConnPair()
		defer func() { _ = client.Close() }()
		defer func() { _ = server.Close() }()

		start := time.Now()
		if err := server.SetReadDeadline(start.Add(30 * time.Second)); err != nil {
			t.Fatal(err)
		}
		_, err := server.Read(make([]byte, 4))
		assertDeadlineExceeded(t, err)
		if elapsed := time.Since(start); elapsed != 30*time.Second {
			t.Fatalf("virtual elapsed = %v, want exactly 30s", elapsed)
		}
	})
}

func TestDeadlineRace(t *testing.T) {
	client, server := newTestConnPair()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4)
		for i := 0; i < 50; i++ {
			_, _ = server.Read(buf)
		}
	}()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = server.SetReadDeadline(time.Now().Add(time.Millisecond))
		}
	}()
	wg.Wait()
	<-done
}
