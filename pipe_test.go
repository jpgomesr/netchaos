package netchaos

import (
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// TestZeroLengthWrite pins what a Write of no bytes does, which nothing
// asserted before: whether an empty write is a fault unit at all was
// answered only by whichever line of code happened to run first.
//
// It is a fault unit. The write is admitted and draws from every configured
// fault's stream exactly like any other unit — that is the draw discipline
// the determinism contract fixes (docs/04, M2-5), and treating an empty
// write as a no-op that consumes no draws would be a breaking change to it,
// not a cleanup. So the draw consumption is asserted, deliberately, as
// intended behaviour.
//
// What it must NOT do is surface at the reader. An empty payload carries
// nothing to read, and delivering one as a (0, nil) Read is a wakeup no real
// net.Conn produces — io.Reader discourages exactly that return for a
// non-empty buffer.
func TestZeroLengthWrite(t *testing.T) {
	n := NewNetwork(WithSeed(7), WithPacketLoss(0.5), WithLatency(time.Millisecond, 2*time.Millisecond))
	client, server := dialPair(t, n)

	for _, payload := range [][]byte{nil, {}} {
		got, err := client.Write(payload)
		if got != 0 || err != nil {
			t.Fatalf("Write(%#v) = (%d, %v), want (0, nil)", payload, got, err)
		}
	}

	// Both empty writes were fault units: one recorded decision each.
	if events := client.(*conn).writePipe.trace.snapshot(); len(events) != 2 {
		t.Errorf("recorded fault events after two empty writes = %d, want 2 "+
			"(an empty write is a unit and must consume its draws, per the draw discipline)", len(events))
	}

	// The reader must see nothing at all, not a zero-length read.
	if err := server.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	got, err := server.Read(make([]byte, 8))
	if got != 0 {
		t.Fatalf("Read after empty writes = %d bytes, want 0", got)
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("Read after empty writes = %v, want os.ErrDeadlineExceeded "+
			"(an empty write must not surface as a spurious (0, nil) read)", err)
	}
}

// dialPair stands up one connected pair on n, failing the test rather than
// making every caller repeat the accept goroutine.
func dialPair(t *testing.T, n *Network) (client, server net.Conn) {
	t.Helper()

	l, err := n.Listen("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := l.Accept()
		if err == nil {
			accepted <- c
		}
	}()

	client, err = n.Dial("tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	server = <-accepted
	t.Cleanup(func() { _ = server.Close() })
	return client, server
}

func TestPipeRoundTrip(t *testing.T) {
	p := newPipe(defaultPipeBound)
	want := []byte("hello world")
	n, err := p.write(want)
	if err != nil || n != len(want) {
		t.Fatalf("write = (%d, %v), want (%d, nil)", n, err, len(want))
	}
	got := make([]byte, len(want))
	n, err = p.read(got)
	if err != nil || n != len(want) {
		t.Fatalf("read = (%d, %v), want (%d, nil)", n, err, len(want))
	}
	if string(got) != string(want) {
		t.Fatalf("read data = %q, want %q", got, want)
	}
}

func TestPipePartialRead(t *testing.T) {
	p := newPipe(defaultPipeBound)
	if _, err := p.write([]byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	var got []byte
	for len(got) < 10 {
		n, err := p.read(buf)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		got = append(got, buf[:n]...)
	}
	if string(got) != "0123456789" {
		t.Fatalf("got %q, want %q", got, "0123456789")
	}
}

func TestPipeCoalescedReads(t *testing.T) {
	p := newPipe(defaultPipeBound)
	if _, err := p.write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.write([]byte("def")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 6)
	n, err := p.read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if n != 6 || string(buf) != "abcdef" {
		t.Fatalf("read = (%d, %q), want (6, \"abcdef\")", n, buf)
	}
}

func TestPipeBlocksWhenEmpty(t *testing.T) {
	p := newPipe(defaultPipeBound)
	started := make(chan struct{})
	type readResult struct {
		n   int
		err error
	}
	result := make(chan readResult, 1)
	go func() {
		close(started)
		buf := make([]byte, 5)
		n, err := p.read(buf)
		result <- readResult{n, err}
	}()
	<-started
	time.Sleep(20 * time.Millisecond)
	select {
	case r := <-result:
		t.Fatalf("read returned early: (%d, %v)", r.n, r.err)
	default:
	}
	if _, err := p.write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-result:
		if r.err != nil || r.n != 2 {
			t.Fatalf("read = (%d, %v), want (2, nil)", r.n, r.err)
		}
	case <-time.After(time.Second):
		t.Fatal("read did not unblock after write")
	}
}

func TestPipeCloseUnblocksReader(t *testing.T) {
	p := newPipe(defaultPipeBound)
	started := make(chan struct{})
	type readResult struct {
		n   int
		err error
	}
	result := make(chan readResult, 1)
	go func() {
		close(started)
		buf := make([]byte, 5)
		n, err := p.read(buf)
		result <- readResult{n, err}
	}()
	<-started
	time.Sleep(20 * time.Millisecond)
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-result:
		if !errors.Is(r.err, io.EOF) || r.n != 0 {
			t.Fatalf("read = (%d, %v), want (0, io.EOF)", r.n, r.err)
		}
	case <-time.After(time.Second):
		t.Fatal("read did not unblock after close")
	}
}

func TestPipeEOFAfterClose(t *testing.T) {
	p := newPipe(defaultPipeBound)
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	n, err := p.read(buf)
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("read = (%d, %v), want (0, io.EOF)", n, err)
	}
}

func TestPipeWriteAfterClose(t *testing.T) {
	p := newPipe(defaultPipeBound)
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
	n, err := p.write([]byte("x"))
	if n != 0 || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write = (%d, %v), want (0, io.ErrClosedPipe)", n, err)
	}
}

func TestPipeDoubleClose(t *testing.T) {
	p := newPipe(defaultPipeBound)
	if err := p.close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := p.close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestPipeBackPressure(t *testing.T) {
	p := newPipe(8)
	n, err := p.write([]byte("12345678"))
	if err != nil || n != 8 {
		t.Fatalf("fill write = (%d, %v), want (8, nil)", n, err)
	}

	_, ch, _ := p.tryWrite([]byte("x"))
	if ch == nil {
		t.Fatal("tryWrite at full bound did not report blocking")
	}

	buf := make([]byte, 4)
	if _, err := p.read(buf); err != nil {
		t.Fatalf("read: %v", err)
	}

	n, ch, err = p.tryWrite([]byte("x"))
	if ch != nil {
		t.Fatal("tryWrite after drain still reports blocking")
	}
	if err != nil || n != 1 {
		t.Fatalf("tryWrite after drain = (%d, %v), want (1, nil)", n, err)
	}
}

func TestPipeOversizedWriteWaitsForEmpty(t *testing.T) {
	p := newPipe(4)
	if _, err := p.write([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	// Oversized relative to bound (4), but pipe is not yet empty: must block.
	_, ch, _ := p.tryWrite([]byte("0123456789"))
	if ch == nil {
		t.Fatal("oversized tryWrite while pipe non-empty did not report blocking")
	}

	buf := make([]byte, 2)
	if _, err := p.read(buf); err != nil {
		t.Fatal(err)
	}

	// Now empty: the oversized write must be admitted as one atomic unit.
	n, ch, err := p.tryWrite([]byte("0123456789"))
	if ch != nil {
		t.Fatal("oversized tryWrite on empty pipe still reports blocking")
	}
	if err != nil || n != 10 {
		t.Fatalf("oversized tryWrite on empty pipe = (%d, %v), want (10, nil)", n, err)
	}
}

func TestPipeDurablyBlockingInBubble(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newPipe(defaultPipeBound)
		type readResult struct {
			n   int
			err error
		}
		result := make(chan readResult, 1)
		go func() {
			buf := make([]byte, 5)
			n, err := p.read(buf)
			result <- readResult{n, err}
		}()

		synctest.Wait()

		if _, err := p.write([]byte("hi")); err != nil {
			t.Fatal(err)
		}
		r := <-result
		if r.err != nil || r.n != 2 {
			t.Fatalf("read = (%d, %v), want (2, nil)", r.n, r.err)
		}
	})
}

func TestPipeConcurrent(t *testing.T) {
	const writers = 4
	const perWriter = 200
	p := newPipe(1024)

	var wg sync.WaitGroup
	var totalWritten int64
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				data := []byte{byte(id), byte(j)}
				n, err := p.write(data)
				if err != nil || n != len(data) {
					t.Errorf("write: n=%d err=%v", n, err)
					return
				}
				atomic.AddInt64(&totalWritten, int64(n))
			}
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		_ = p.close()
		close(done)
	}()

	var totalRead int64
	const readers = 4
	var rwg sync.WaitGroup
	for i := 0; i < readers; i++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			buf := make([]byte, 16)
			for {
				n, err := p.read(buf)
				atomic.AddInt64(&totalRead, int64(n))
				if errors.Is(err, io.EOF) {
					return
				}
				if err != nil {
					t.Errorf("read: %v", err)
					return
				}
			}
		}()
	}

	<-done
	rwg.Wait()

	if got, want := atomic.LoadInt64(&totalRead), atomic.LoadInt64(&totalWritten); got != want {
		t.Fatalf("totalRead = %d, want totalWritten %d", got, want)
	}
}
