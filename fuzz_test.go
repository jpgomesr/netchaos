package netchaos

import (
	"bytes"
	"errors"
	"io"
	"math"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
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

		if n, _, err := p.tryRead(make([]byte, 8)); n != 0 || !errors.Is(err, io.EOF) {
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
// after every operation, whatever the interleaving, on a pipe with no fault
// policy installed.
func checkPipeAccounting(t *testing.T, p *pipe) {
	t.Helper()

	p.mu.Lock()
	defer p.mu.Unlock()

	nonEmpty := checkBufBytesLocked(t, p)

	// bufBytes may exceed the bound only through the oversized-write rule,
	// which admits one payload into a pipe holding no bytes. Nothing can join
	// it afterwards — admitting anything else requires bufBytes+len <= bound —
	// so more than one non-empty payload over the bound would mean the
	// admission check let something through that it should not have.
	//
	// This fuzz target drives p.tryWrite/tryRead directly against a raw pipe
	// (passThroughDeliver, no fault policy installed), so it can never reach
	// installFaultPolicy's other way bufBytes can legitimately exceed the
	// bound with two non-empty payloads: WithDuplication (M7-8) admitting a
	// second copy of an already-admitted unit. That path is covered by
	// duplicate_test.go's TestDuplicationChargesBothCopies, not here.
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

// checkBufBytesLocked asserts the invariant that holds for every pipe,
// faults or not: bufBytes is exactly the sum of the payloads still buffered
// (readable plus pending), and never negative. It returns how many of those
// payloads are non-empty. p.mu must be held.
func checkBufBytesLocked(t *testing.T, p *pipe) (nonEmpty int) {
	t.Helper()

	sum := 0
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
	return nonEmpty
}

// FuzzSplitAddr checks the one function that takes an address string apart
// (splitAddr) and the two built on it (peerName, errAddr) against
// net.SplitHostPort, for arbitrary input: no panic, a no-colon string is a
// plain peer name, a successful split agrees with the standard library and
// keeps the port in range, and every failure is a *net.AddrError naming
// the input.
func FuzzSplitAddr(f *testing.F) {
	for _, s := range []string{"", "server", "server:8080", ":0", ":8080", "[::1]:80", "[::1", "a:b:c", "h:99999", "h:-1", "h:00", "h:+5"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		host, port, explicit, err := splitAddr(s)

		if !strings.Contains(s, ":") {
			if err != nil || host != s || port != 0 || explicit {
				t.Fatalf("splitAddr(%q) = (%q, %d, %v, %v), want the input back as a plain peer name", s, host, port, explicit, err)
			}
		} else if err == nil {
			h, p, splitErr := net.SplitHostPort(s)
			if splitErr != nil || h != host {
				t.Fatalf("splitAddr(%q) host = %q, net.SplitHostPort = (%q, %v)", s, host, h, splitErr)
			}
			if port < 0 || port > maxPort {
				t.Fatalf("splitAddr(%q) port = %d, outside [0, %d]", s, port, maxPort)
			}
			if wantExplicit := p != "" && p != "0"; explicit != wantExplicit {
				t.Fatalf("splitAddr(%q) explicit = %v, want %v (port string %q)", s, explicit, wantExplicit, p)
			}
		} else {
			var addrErr *net.AddrError
			if !errors.As(err, &addrErr) || addrErr.Addr != s {
				t.Fatalf("splitAddr(%q) error = %v (%T), want a *net.AddrError with Addr %q", s, err, err, s)
			}
		}

		if got := peerName(s); err == nil && got != host || err != nil && got != s {
			t.Fatalf("peerName(%q) = %q, want %q", s, got, map[bool]string{true: host, false: s}[err == nil])
		}

		a := errAddr("tcp", s)
		if (a == nil) != (s == "") {
			t.Fatalf("errAddr(%q) = %v, want nil exactly when the address is empty", s, a)
		}
	})
}

// FuzzSerializationDelay checks serializationDelay's 128-bit arithmetic
// against a math/big reference for arbitrary sizes and rates:
// floor(size * 1e9 / bytesPerSecond) nanoseconds, clamped to the largest
// time.Duration. The overflow cases #80 found are in the seed corpus.
func FuzzSerializationDelay(f *testing.F) {
	f.Add(uint64(10<<30), uint64(1<<20))                 // 10 GiB at 1 MiB/s
	f.Add(uint64(9_300_000_000), uint64(10_000_000_000)) // 9.3 GB at 10 GB/s (#80)
	f.Add(uint64(1<<40), uint64(1))                      // clamps: quotient past 64 bits
	f.Add(uint64(10_000_000_000), uint64(1))             // clamps: past MaxInt64, within 64 bits
	f.Add(uint64(0), uint64(1))
	f.Add(uint64(1), uint64(math.MaxInt64))
	f.Fuzz(func(t *testing.T, rawSize, rawRate uint64) {
		size := int(rawSize % (uint64(math.MaxInt) + 1))
		rate := int(rawRate%uint64(math.MaxInt)) + 1 // bytesPerSecond > 0, as validated

		q := new(big.Int).Mul(big.NewInt(int64(size)), big.NewInt(int64(time.Second)))
		q.Quo(q, big.NewInt(int64(rate)))
		want := time.Duration(math.MaxInt64)
		if q.IsInt64() {
			want = time.Duration(q.Int64())
		}
		if got := serializationDelay(size, rate); got != want {
			t.Fatalf("serializationDelay(%d, %d) = %d, want %d", size, rate, got, want)
		}
	})
}

// FuzzStreamIntegrity drives a Network connection with only timing faults
// (latency and bandwidth) and arbitrary write chunking, and checks that
// the reader receives exactly the bytes written, in order: timing faults
// may delay delivery but must never drop, duplicate, reorder or alter a
// byte. Runs inside a synctest bubble, so any hang is a reported deadlock.
func FuzzStreamIntegrity(f *testing.F) {
	f.Add(int64(1), []byte("hello, world"), []byte{3})
	f.Add(int64(7), bytes.Repeat([]byte{0xAB}, 300), []byte{0, 31, 5, 200})
	f.Add(int64(-42), []byte{}, []byte{})
	f.Add(int64(1<<40), bytes.Repeat([]byte("netchaos"), 64), []byte{63, 1})
	f.Fuzz(func(t *testing.T, seed int64, data []byte, chunks []byte) {
		if len(data) > 4096 {
			data = data[:4096]
		}
		u := uint64(seed)
		maxLatency := time.Duration(u%20) * time.Millisecond
		rate := 1000 + int((u>>8)%1_000_000)

		synctest.Test(t, func(t *testing.T) {
			n := NewNetwork(WithSeed(seed), WithPipeBound(64), WithLatency(0, maxLatency), WithBandwidth(rate))
			client, server := dialNamedPair(t, n)

			got := make(chan []byte, 1)
			go func() {
				buf := make([]byte, len(data))
				_, _ = io.ReadFull(server, buf)
				got <- buf
			}()

			rest, i := data, 0
			for len(rest) > 0 {
				size := 1
				if len(chunks) > 0 {
					size = int(chunks[i%len(chunks)])%32 + 1
				}
				i++
				size = min(size, len(rest))
				if _, err := client.Write(rest[:size]); err != nil {
					t.Fatalf("Write: %v", err)
				}
				rest = rest[size:]
			}

			// Read everything before closing: Close discards bytes still in
			// flight, which is not what this target is about.
			if b := <-got; !bytes.Equal(b, data) {
				t.Fatalf("received %d bytes that differ from the %d written (latency <= %v, %d B/s)", len(b), len(data), maxLatency, rate)
			}
		})
	})
}

// FuzzEvaluatorAccounting toggles the live fault settings and a partition
// mid-stream while writing, and checks after every step that the writer's
// pipe accounting still balances (checkEvaluatorAccounting). At the end, with
// every fault off and the partition healed, a marker write must still get
// through -- no combination of drops, duplicates, partitions and latency
// may leave the writer wedged on phantom back-pressure -- and the dialer
// side of Trace() must hold exactly one event per Write.
//
// Each op byte selects an action by op%6: write (op/6)%32+1 bytes; set loss
// to 0, 0.5 or 1; set duplication to 0 or 0.5; set latency to [0, k ms];
// toggle the partition; or let 1ms of virtual time pass.
func FuzzEvaluatorAccounting(f *testing.F) {
	f.Add(int64(1), []byte{0, 6, 12})
	f.Add(int64(2), []byte{1 + 6, 0, 0, 4, 0, 0, 4, 5, 0}) // loss 0.5, writes across a partition
	f.Add(int64(3), []byte{2 + 6, 3 + 6*9, 0, 0, 0, 5, 5, 0})
	f.Add(int64(4), []byte{1 + 12, 0, 0, 0, 0, 1, 0, 0}) // loss 1 then back to 0
	f.Add(int64(5), bytes.Repeat([]byte{0, 3, 5, 2, 4}, 20))
	f.Fuzz(func(t *testing.T, seed int64, ops []byte) {
		if len(ops) > 512 {
			ops = ops[:512]
		}
		synctest.Test(t, func(t *testing.T) {
			n := NewNetwork(WithSeed(seed), WithPipeBound(64))
			client, server := dialNamedPair(t, n)
			wp := client.(*conn).writePipe

			var mu sync.Mutex
			var received bytes.Buffer
			done := make(chan struct{})
			go func() {
				defer close(done)
				buf := make([]byte, 128)
				for {
					k, err := server.Read(buf)
					mu.Lock()
					received.Write(buf[:k])
					mu.Unlock()
					if err != nil {
						return
					}
				}
			}()

			writes, partitioned := 0, false
			for _, op := range ops {
				switch op % 6 {
				case 0:
					if _, err := client.Write(make([]byte, int(op/6)%fuzzEvalMaxWrite+1)); err != nil {
						t.Fatalf("Write: %v", err)
					}
					writes++
				case 1:
					n.SetPacketLoss([]float64{0, 0.5, 1}[int(op/6)%3])
				case 2:
					n.SetDuplication([]float64{0, 0.5}[int(op/6)%2])
				case 3:
					n.SetLatency(0, time.Duration(op/6)*time.Millisecond)
				case 4:
					if partitioned {
						n.Heal("client", "server")
					} else {
						n.Partition("client", "server")
					}
					partitioned = !partitioned
				case 5:
					time.Sleep(time.Millisecond)
				}
				checkEvaluatorAccounting(t, wp, fuzzEvalMaxWrite)
			}

			n.Heal("client", "server")
			n.SetPacketLoss(0)
			n.SetDuplication(0)
			n.SetLatency(0, 0)
			marker := []byte("<<end-of-fuzz>>")
			if _, err := client.Write(marker); err != nil {
				t.Fatalf("marker Write: %v", err)
			}
			writes++
			time.Sleep(time.Second) // release anything still held back by latency
			synctest.Wait()
			checkEvaluatorAccounting(t, wp, len(marker))

			mu.Lock()
			ok := bytes.HasSuffix(received.Bytes(), marker)
			mu.Unlock()
			if !ok {
				t.Fatalf("the marker written after healing and clearing every fault never arrived; the writer is wedged")
			}

			dialerEvents := 0
			for _, ev := range n.Trace() {
				if ev.Side == SideDialer {
					dialerEvents++
				}
			}
			if dialerEvents != writes {
				t.Fatalf("Trace() has %d dialer events for %d writes, want one per Write", dialerEvents, writes)
			}

			_ = client.Close()
			<-done
		})
	})
}

// fuzzEvalMaxWrite is the largest Write FuzzEvaluatorAccounting issues
// before its marker; it is below the pipe bound, so no write is oversized.
const fuzzEvalMaxWrite = 32

// checkEvaluatorAccounting is checkPipeAccounting for a pipe with the fault
// evaluator installed. bufBytes must still equal the buffered payloads, but
// the over-bound rule differs: WithDuplication charges the second copy of
// an admitted unit against the bound after admission (docs/05 § Packet
// duplication, "Accounting"), so a direction can legitimately sit past its
// bound -- by at most one more copy of a unit, since admission requires
// bufBytes+len <= bound and an over-bound pipe admits nothing further until
// it drains. That is what this checks: bufBytes <= bound + maxWrite.
func checkEvaluatorAccounting(t *testing.T, p *pipe, maxWrite int) {
	t.Helper()

	p.mu.Lock()
	defer p.mu.Unlock()

	checkBufBytesLocked(t, p)
	if limit := max(p.bound, maxWrite) + maxWrite; p.bufBytes > limit {
		t.Fatalf("bufBytes = %d exceeds bound %d plus one duplicated %d-byte unit (%d)", p.bufBytes, p.bound, maxWrite, limit)
	}
}
