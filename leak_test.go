package netchaos

import (
	"net"
	"runtime"
	"testing"
	"testing/synctest"
	"time"
)

// TestNoGoroutineLeaks drives a full dial/accept/write/read/close cycle,
// including a deadline (so deadline.go's time.AfterFunc-backed goroutine is
// actually exercised), and confirms goroutine count returns to its baseline
// afterward. netchaos spawns no goroutine of its own except a deadline
// timer's callback, which self-terminates once it fires or is stopped (see
// Network's godoc) — this test is the audit that backs that claim.
func TestNoGoroutineLeaks(t *testing.T) {
	before := runtime.NumGoroutine()

	n := NewNetwork()
	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := l.Accept()
		if err == nil {
			accepted <- c
		}
	}()

	client, err := n.Dial("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted

	if err := client.SetDeadline(time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Read(make([]byte, 2)); err != nil {
		t.Fatal(err)
	}

	_ = client.Close()
	_ = server.Close()
	_ = l.Close()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if runtime.NumGoroutine() <= before {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine count = %d, want <= %d (baseline) after a full dial-write-close cycle", runtime.NumGoroutine(), before)
		}
		runtime.Gosched()
		time.Sleep(10 * time.Millisecond)
	}
}

// TestBubbleCleanExit runs a full dial/accept/write/read/close scenario
// inside synctest.Test. synctest.Test itself fails the test if any bubble
// goroutine is still running (and not durably blocked) when the function
// passed to it returns, so simply completing this test is the assertion.
func TestBubbleCleanExit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNetwork()
		l, err := n.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}

		accepted := make(chan net.Conn, 1)
		go func() {
			c, err := l.Accept()
			if err == nil {
				accepted <- c
			}
		}()

		client, err := n.Dial("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		server := <-accepted

		if _, err := client.Write([]byte("hi")); err != nil {
			t.Fatal(err)
		}
		if _, err := server.Read(make([]byte, 2)); err != nil {
			t.Fatal(err)
		}

		_ = client.Close()
		_ = server.Close()
		_ = l.Close()
	})
}
