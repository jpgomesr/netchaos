package netchaos

import (
	"bytes"
	"io"
	"testing"
)

// fuzzPipeBound is small on purpose. The production bound is 64 KiB, but the
// rules worth fuzzing are about the *boundary* — admit, refuse, admit an
// oversized payload into an empty pipe — and a small bound reaches all three
// with byte-sized operations instead of megabyte ones.
const fuzzPipeBound = 64

// FuzzPipeAccounting drives a pipe through arbitrary interleavings of write
// and read sizes and checks the buffer accounting after every single step.
//
// The pipe's admission rules form a small state machine over sizes —
// bufBytes, the bound, the oversized-write rule (pipe.go: a write larger
// than the bound is admitted only into a completely empty pipe), and
// partial/coalesced reads — where the invariants are easy to state and a bad
// interleaving is genuinely hard to find by hand. That combination is what
// makes it the natural fuzz target rather than, say, the fault evaluator,
// whose behaviour is pinned by golden traces instead.
//
// Each input byte is one operation: b < 128 writes b bytes, b >= 128 reads
// b-128 bytes. Sizes therefore reach 127 against a bound of 64, so oversized
// writes are exercised rather than merely possible.
//
// tryWrite/tryRead are used rather than write/read because they are the
// non-blocking halves: a fuzzer that could block would deadlock on the first
// write into a full pipe instead of exploring anything.
func FuzzPipeAccounting(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{1, 129})               // write 1, read 1
	f.Add([]byte{0, 128})               // zero-length write, then a read
	f.Add([]byte{100, 227})             // oversized write into an empty pipe, then drain
	f.Add([]byte{60, 60, 60, 255})      // fill past the bound, then a large read
	f.Add([]byte{10, 10, 133, 10, 255}) // coalescing plus a partial read
	f.Add([]byte{127, 129, 129, 255})   // oversized, then repeated partial reads

	f.Fuzz(func(t *testing.T, ops []byte) {
		p := newPipe(fuzzPipeBound)

		var wrote, read bytes.Buffer
		var nextByte byte

		for _, op := range ops {
			if op < 128 {
				payload := make([]byte, int(op))
				for i := range payload {
					payload[i] = nextByte
					nextByte++
				}
				n, ch, err := p.tryWrite(payload)
				if err != nil {
					t.Fatalf("tryWrite(%d bytes) on an open pipe = %v, want nil", len(payload), err)
				}
				if ch == nil {
					// Admitted. io.Writer forbids a short count with no error.
					if n != len(payload) {
						t.Fatalf("tryWrite admitted %d of %d bytes without an error", n, len(payload))
					}
					wrote.Write(payload)
				} else if n != 0 {
					t.Fatalf("tryWrite reported blocking but still admitted %d bytes", n)
				}
			} else {
				buf := make([]byte, int(op-128))
				n, ch, err := p.tryRead(buf)
				if err != nil {
					t.Fatalf("tryRead on an open pipe = %v, want nil", err)
				}
				if n > len(buf) {
					t.Fatalf("tryRead returned %d bytes into a %d-byte buffer", n, len(buf))
				}
				if ch == nil {
					read.Write(buf[:n])
				} else if n != 0 {
					t.Fatalf("tryRead reported blocking but still returned %d bytes", n)
				}
			}
			checkPipeAccounting(t, p)
		}

		// Drain whatever is left, then close and confirm the close drains
		// rather than discarding: pipe.close only discards latency-pending
		// units, and this pipe has none.
		drainPipe(t, p, &read)
		if err := p.close(); err != nil {
			t.Fatalf("close = %v, want nil", err)
		}
		drainPipe(t, p, &read)
		checkPipeAccounting(t, p)

		if n, _, err := p.tryRead(make([]byte, 8)); n != 0 || err != io.EOF {
			t.Fatalf("tryRead on a closed, drained pipe = (%d, %v), want (0, io.EOF)", n, err)
		}

		// Every admitted byte comes back out, once, in order.
		if !bytes.Equal(wrote.Bytes(), read.Bytes()) {
			t.Fatalf("read back %d bytes, want the %d admitted, in order\n wrote: %v\n  read: %v",
				read.Len(), wrote.Len(), wrote.Bytes(), read.Bytes())
		}
	})
}

// drainPipe reads until the pipe reports it would block, or is closed and
// drained, appending everything it yields to got. The buffer is deliberately
// smaller than the largest payload a write can admit, so an oversized unit
// is drained across several partial reads rather than one convenient one.
func drainPipe(t *testing.T, p *pipe, got *bytes.Buffer) {
	t.Helper()

	buf := make([]byte, fuzzPipeBound)
	for i := 0; ; i++ {
		if i > 1<<16 {
			t.Fatal("drain did not terminate: tryRead kept yielding data")
		}
		n, ch, err := p.tryRead(buf)
		if err != nil || ch != nil || n == 0 {
			return
		}
		got.Write(buf[:n])
	}
}

// checkPipeAccounting asserts the two structural invariants that must hold
// after every operation, whatever the interleaving.
func checkPipeAccounting(t *testing.T, p *pipe) {
	t.Helper()

	p.mu.Lock()
	defer p.mu.Unlock()

	sum, nonEmpty := 0, 0
	for _, chunk := range p.readable {
		sum += len(chunk)
		if len(chunk) > 0 {
			nonEmpty++
		}
	}
	for _, u := range p.pending {
		sum += len(u.data)
		if len(u.data) > 0 {
			nonEmpty++
		}
	}
	if sum != p.bufBytes {
		t.Fatalf("bufBytes = %d, want %d (the sum of buffered payload lengths)", p.bufBytes, sum)
	}
	if p.bufBytes < 0 {
		t.Fatalf("bufBytes = %d, want >= 0", p.bufBytes)
	}

	// bufBytes may exceed the bound only through the oversized-write rule,
	// which admits one payload into a pipe holding no bytes. Nothing can join
	// it afterwards — admitting anything else requires bufBytes+len <= bound —
	// so more than one non-empty payload over the bound would mean the
	// admission check let something through that it should not have.
	//
	// Counted in non-empty payloads rather than payloads, and the distinction
	// is not pedantic: the fuzzer found it. A zero-length write is admitted
	// and queued while leaving bufBytes at 0, so the input {write 0, write 65}
	// legitimately reaches two queued payloads and 65 buffered bytes against a
	// bound of 64. The byte accounting is exactly right there — the empty
	// chunk contributes nothing and any read pops it silently — so this is the
	// rule holding, not breaking. That input is kept in the corpus.
	if p.bufBytes > p.bound && nonEmpty != 1 {
		t.Fatalf("bufBytes = %d exceeds bound %d with %d non-empty buffered payloads; "+
			"the oversized-write rule admits exactly one, into a pipe holding no bytes",
			p.bufBytes, p.bound, nonEmpty)
	}
}
