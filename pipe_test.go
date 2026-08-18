package netchaos

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

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

	_, _, ch := p.tryWrite([]byte("x"))
	if ch == nil {
		t.Fatal("tryWrite at full bound did not report blocking")
	}

	buf := make([]byte, 4)
	if _, err := p.read(buf); err != nil {
		t.Fatalf("read: %v", err)
	}

	n, err, ch = p.tryWrite([]byte("x"))
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
	_, _, ch := p.tryWrite([]byte("0123456789"))
	if ch == nil {
		t.Fatal("oversized tryWrite while pipe non-empty did not report blocking")
	}

	buf := make([]byte, 2)
	if _, err := p.read(buf); err != nil {
		t.Fatal(err)
	}

	// Now empty: the oversized write must be admitted as one atomic unit.
	n, err, ch := p.tryWrite([]byte("0123456789"))
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
