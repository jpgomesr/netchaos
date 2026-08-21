package netchaos

// M3-3: the reproducibility harness. Turns the determinism contract
// (docs/04's #determinism-contract) into an enforced, regression-tested
// property: the same seed and the same scenario must produce identical
// fault traces across runs, across real concurrency, and (via checked-in
// golden files) across machines and Go versions.
//
// No new exported API. traceRecorder and faultEvent (trace.go) are
// deliberately unexported -- M2-1 explicitly deferred exporting an
// accessor, and docs/04's banner says M3 adds no API surface. captureTrace
// below reaches them the same way every existing in-package test does: a
// net.Conn from Dial/Accept is always a *conn in this package, and
// *conn.writePipe.trace is always non-nil (newConnPairWithSeed allocates
// one for both pipes of every pair, conn.go).
//
// canonicalTrace is a concatenation ordered by (ordinal, side, seq), never
// a wall-clock merge and never a global sort across directions. Per-
// direction ordering is exactly what would catch a shared-RNG regression:
// one direction's draws shifting because another direction's traffic stole
// them. A global sort across directions would hide that by interleaving
// events from unrelated streams. All five faultEvent fields are kept for
// the same reason -- dropping drawn when dropped is true would hide a
// regression in the M2-5 draw discipline (a dropped unit still draws a
// latency it never uses; see faults.go).
//
// Golden traces are generated AND asserted from inside a synctest bubble.
// faultEvent.effective (faults.go's pending-queue clamp) depends on the
// real elapsed time between two writes whenever clamping fires -- zero and
// deterministic inside a bubble (the clock only advances when everything
// is idle), wall-clock noise outside one. A golden file generated outside
// a bubble would be flaky by construction.
//
// captureTrace's direction-coverage rule: a *conn's write direction is
// c.writePipe at c.side; its readPipe is the peer's write direction with
// the side flipped. A caller that hands captureTrace only one end of every
// pair silently captures half the directions in play. Scenarios below
// therefore always return every conn end they created.
//
// The concurrent-determinism test uses exactly one goroutine per direction,
// each with a fixed payload sequence. This is why it holds: each
// direction's writes are serialized by that pipe's own mutex, and each
// direction has its own RNG stream keyed by (seed, ordinal, side, kind), so
// the draw sequence *within* a direction is fixed regardless of how
// directions interleave with each other. Two writers sharing one direction
// would still have their draws serialized by the mutex, but the
// payload-to-event mapping (and, with differing payload sizes, the
// buffered-byte accounting) becomes nondeterministic -- so the set of
// directions and each one's write count and payload sequence must be fixed
// by the scenario, not decided by scheduling.
//
// Deviation from a literal build/run split: a scenario here is a single
// closure (fn) that dials (sequentially -- docs/04's contract does not
// cover concurrent, unordered connection *establishment*), drives traffic,
// and returns every conn end for capture. Nothing in these scenarios
// benefits from separating "build" from "run" as two closures, and a split
// would force an awkward choice about how "run" reaches the *Network a
// dynamic Partition/Heal call needs -- the composed-basic scenario toggles
// a partition mid-run, which a fn closure captures naturally.

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// Regenerating golden traces:
//
//	go test -run TestGoldenTraces -update .
//
// A golden diff is a CONTRACT-CHANGE SIGNAL, not a flake. docs/04's
// determinism contract says the fault sequence is stable for a given seed
// and call order, so a changed sequence is a breaking change to that
// contract. Regenerate only when the change is intended, and say so in the
// PR that regenerates it.
var updateGolden = flag.Bool("update", false, "regenerate testdata/traces/*.golden")

// traceLine is one faultEvent tagged with the connection direction that
// produced it.
type traceLine struct {
	ordinal uint64
	side    connSide
	faultEvent
}

// canonicalTrace is every direction's recorded events concatenated in
// (ordinal, side, seq) order. See the file comment for why this is
// deliberately not a wall-clock merge.
type canonicalTrace []traceLine

// scenario is a reproducible, repeatable unit of work: fn dials whatever
// connections it needs (sequentially) and drives traffic over them, then
// returns every conn end it created so the caller can capture a trace.
type scenario struct {
	name string
	fn   func(t *testing.T, seed int64) []net.Conn
}

// runScenario runs sc under seed and captures the resulting trace. Must be
// called from inside a synctest bubble; see the file comment on why golden
// generation and assertion both require one.
func runScenario(t *testing.T, sc scenario, seed int64) canonicalTrace {
	t.Helper()
	conns := sc.fn(t, seed)
	return captureTrace(conns)
}

// captureTrace collects the write-direction trace of every conn in conns.
// Callers must pass every conn end whose direction should be part of the
// trace; captureTrace reads only writePipe, so each direction is captured
// exactly once with no side-flipping (see the file comment).
func captureTrace(conns []net.Conn) canonicalTrace {
	type direction struct {
		ordinal uint64
		side    connSide
		events  []faultEvent
	}
	dirs := make([]direction, 0, len(conns))
	for _, nc := range conns {
		c := nc.(*conn)
		dirs = append(dirs, direction{
			ordinal: c.ordinal,
			side:    c.side,
			events:  c.writePipe.trace.snapshot(),
		})
	}
	// Sort by (ordinal, side); a direction's own events are already in seq
	// order from traceRecorder.record's append-only accumulation.
	for i := 1; i < len(dirs); i++ {
		for j := i; j > 0; j-- {
			a, b := dirs[j-1], dirs[j]
			less := a.ordinal < b.ordinal || (a.ordinal == b.ordinal && a.side < b.side)
			if less {
				break
			}
			dirs[j-1], dirs[j] = dirs[j], dirs[j-1]
		}
	}

	var out canonicalTrace
	for _, d := range dirs {
		for _, e := range d.events {
			out = append(out, traceLine{ordinal: d.ordinal, side: d.side, faultEvent: e})
		}
	}
	return out
}

func sideName(s connSide) string {
	switch s {
	case sideDialer:
		return "dialer"
	case sideAcceptor:
		return "acceptor"
	default:
		return "unknown"
	}
}

func sideFromName(s string) connSide {
	switch s {
	case "dialer":
		return sideDialer
	case "acceptor":
		return sideAcceptor
	default:
		return connSide(-1)
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// String renders ct in the golden file line format: integer nanoseconds,
// never time.Duration.String(), so formatting can never be a source of
// cross-version drift (see the file comment).
func (ct canonicalTrace) String() string {
	var b strings.Builder
	for _, l := range ct {
		fmt.Fprintf(&b, "ord=%d side=%-8s seq=%d part=%d drop=%d drawn=%d eff=%d\n",
			l.ordinal, sideName(l.side), l.seq, boolInt(l.partitioned), boolInt(l.dropped),
			int64(l.drawn), int64(l.effective))
	}
	return b.String()
}

func (ct canonicalTrace) equal(other canonicalTrace) bool {
	if len(ct) != len(other) {
		return false
	}
	for i := range ct {
		if ct[i] != other[i] {
			return false
		}
	}
	return true
}

// diff returns a human-readable description of the first differing line,
// or the length mismatch if all shared lines are equal.
func (ct canonicalTrace) diff(other canonicalTrace) string {
	n := len(ct)
	if len(other) < n {
		n = len(other)
	}
	for i := 0; i < n; i++ {
		if ct[i] != other[i] {
			return fmt.Sprintf("line %d differs:\n  got:  %+v\n  want: %+v", i, ct[i], other[i])
		}
	}
	if len(ct) != len(other) {
		return fmt.Sprintf("trace lengths differ: got %d, want %d", len(ct), len(other))
	}
	return "(no diff)"
}

func writeGolden(path, scenarioName string, seed int64, ct canonicalTrace) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# netchaos-trace v1\n")
	fmt.Fprintf(&b, "# scenario=%s seed=%d\n", scenarioName, seed)
	b.WriteString("# fields: ord side seq part drop drawn_ns eff_ns\n")
	b.WriteString(ct.String())
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func readGolden(path string) (canonicalTrace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ct canonicalTrace
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := make(map[string]string, 7)
		for _, f := range strings.Fields(line) {
			if k, v, ok := strings.Cut(f, "="); ok {
				m[k] = v
			}
		}
		ord, err := strconv.ParseUint(m["ord"], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing golden line %q: %w", line, err)
		}
		seq, err := strconv.ParseUint(m["seq"], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing golden line %q: %w", line, err)
		}
		drawn, err := strconv.ParseInt(m["drawn"], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing golden line %q: %w", line, err)
		}
		eff, err := strconv.ParseInt(m["eff"], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing golden line %q: %w", line, err)
		}
		ct = append(ct, traceLine{
			ordinal: ord,
			side:    sideFromName(m["side"]),
			faultEvent: faultEvent{
				seq:         seq,
				partitioned: m["part"] == "1",
				dropped:     m["drop"] == "1",
				drawn:       time.Duration(drawn),
				effective:   time.Duration(eff),
			},
		})
	}
	return ct, nil
}

// scenarioComposedBasic dials one named client/server pair with loss and
// latency configured, writes unitCount units on the client->server
// direction, and dynamically partitions the pair halfway through --
// exercising all three faults composed on one direction, in the fixed
// evaluation order (partition -> loss -> latency, faults.go).
func scenarioComposedBasic(unitCount int) scenario {
	return scenario{
		name: "composed-basic",
		fn: func(t *testing.T, seed int64) []net.Conn {
			n := NewNetwork(WithSeed(seed), WithPacketLoss(0.3), WithLatency(time.Millisecond, 20*time.Millisecond))
			client, server := dialNamedPair(t, n)

			for i := 0; i < unitCount; i++ {
				if i == unitCount/2 {
					n.Partition("client", "server")
				}
				if _, err := client.Write([]byte{byte(i)}); err != nil {
					t.Fatal(err)
				}
			}
			return []net.Conn{client, server}
		},
	}
}

// scenarioSensitivity dials one pair with loss and latency configured and
// writes unitCount units with no partition, for the "different seeds
// produce different traces" volume requirement -- unitCount is expected to
// be 50+ so a coincidental cross-seed match is impossible.
func scenarioSensitivity(unitCount int) scenario {
	return scenario{
		name: "sensitivity",
		fn: func(t *testing.T, seed int64) []net.Conn {
			n := NewNetwork(WithSeed(seed), WithPacketLoss(0.3), WithLatency(time.Millisecond, 20*time.Millisecond))
			client, server := dialNamedPair(t, n)

			for i := 0; i < unitCount; i++ {
				if _, err := client.Write([]byte{byte(i % 256)}); err != nil {
					t.Fatal(err)
				}
			}
			return []net.Conn{client, server}
		},
	}
}

// scenarioClamping dials one pair with a wide latency range and no loss,
// then writes unitCount units back to back with nothing draining the pipe
// between them. Every write's "now" (installFaultPolicy, faults.go) falls
// at the same virtual instant, so a later unit that draws a shorter delay
// than an earlier, still-pending one gets its releaseAt clamped up to
// match -- the property TestClampingScenarioExercisesClampPath verifies
// actually fires, rather than assuming a specific unit index does.
func scenarioClamping(unitCount int) scenario {
	return scenario{
		name: "clamping",
		fn: func(t *testing.T, seed int64) []net.Conn {
			n := NewNetwork(WithSeed(seed), WithLatency(time.Millisecond, 500*time.Millisecond))
			client, server := dialNamedPair(t, n)

			for i := 0; i < unitCount; i++ {
				if _, err := client.Write([]byte{byte(i % 256)}); err != nil {
					t.Fatal(err)
				}
			}
			return []net.Conn{client, server}
		},
	}
}

// scenarioConcurrentIO dials two independent named pairs sequentially, then
// drives writesPerDirection fixed-size units on all four directions
// concurrently -- one goroutine per direction, per the file comment's
// determinism argument. No packet loss: each reader goroutine below reads
// back exactly writesPerDirection units, one per write, so a dropped unit
// would leave its reader blocked forever waiting for data that never
// arrives. Latency alone is enough to exercise the concurrent draw path.
func scenarioConcurrentIO(writesPerDirection int) scenario {
	return scenario{
		name: "concurrent-io",
		fn: func(t *testing.T, seed int64) []net.Conn {
			n := NewNetwork(WithSeed(seed), WithLatency(time.Millisecond, 10*time.Millisecond))

			dial := func(peer, listenAddr string) (client, server net.Conn) {
				l, err := n.Listen("tcp", listenAddr)
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

				ctx := WithPeerName(context.Background(), peer)
				client, err = n.DialContext(ctx, "tcp", listenAddr)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = client.Close() })

				server = <-accepted
				t.Cleanup(func() { _ = server.Close() })
				return client, server
			}

			// Both dials complete sequentially before any concurrent I/O
			// starts -- the determinism contract does not cover racing,
			// unordered establishment (docs/04).
			client1, server1 := dial("client-1", "server-1")
			client2, server2 := dial("client-2", "server-2")

			writeSeq := func(wg *sync.WaitGroup, w net.Conn, tag byte) {
				defer wg.Done()
				for i := 0; i < writesPerDirection; i++ {
					if _, err := w.Write([]byte{tag, byte(i % 256)}); err != nil {
						t.Fatal(err)
					}
				}
			}
			readSeq := func(wg *sync.WaitGroup, r net.Conn) {
				defer wg.Done()
				buf := make([]byte, 2)
				for i := 0; i < writesPerDirection; i++ {
					if _, err := r.Read(buf); err != nil {
						t.Fatal(err)
					}
				}
			}

			var wg sync.WaitGroup
			wg.Add(8)
			go writeSeq(&wg, client1, 0x10)
			go readSeq(&wg, server1)
			go writeSeq(&wg, server1, 0x11)
			go readSeq(&wg, client1)
			go writeSeq(&wg, client2, 0x20)
			go readSeq(&wg, server2)
			go writeSeq(&wg, server2, 0x21)
			go readSeq(&wg, client2)
			wg.Wait()

			return []net.Conn{client1, server1, client2, server2}
		},
	}
}

func TestSameSeedSameTrace(t *testing.T) {
	sc := scenarioComposedBasic(20)
	const seed = 42

	var a, b canonicalTrace
	synctest.Test(t, func(t *testing.T) { a = runScenario(t, sc, seed) })
	synctest.Test(t, func(t *testing.T) { b = runScenario(t, sc, seed) })

	if !a.equal(b) {
		t.Fatalf("same seed produced different traces:\n%s", a.diff(b))
	}
}

func TestSameSeedSameTraceUnderConcurrency(t *testing.T) {
	sc := scenarioConcurrentIO(20)
	const seed = 77
	const repeats = 10

	traces := make([]canonicalTrace, repeats)
	for i := 0; i < repeats; i++ {
		i := i
		synctest.Test(t, func(t *testing.T) {
			traces[i] = runScenario(t, sc, seed)
		})
	}

	for i := 1; i < repeats; i++ {
		if !traces[0].equal(traces[i]) {
			t.Fatalf("run %d trace differs from run 0:\n%s", i, traces[0].diff(traces[i]))
		}
	}
}

func TestDifferentSeedDifferentTrace(t *testing.T) {
	sc := scenarioSensitivity(60) // 50+ units so a coincidental match is impossible

	var a, b canonicalTrace
	synctest.Test(t, func(t *testing.T) { a = runScenario(t, sc, 1) })
	synctest.Test(t, func(t *testing.T) { b = runScenario(t, sc, 2) })

	if a.equal(b) {
		t.Fatal("different seeds produced identical traces")
	}

	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	diffCount := 0
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			diffCount++
		}
	}
	const minDiffFloor = 10
	if diffCount < minDiffFloor {
		t.Fatalf("only %d of %d shared lines differ between seeds, want at least %d: too close to a coincidental match", diffCount, n, minDiffFloor)
	}
}

// TestComposedBasicExercisesAllThreeFaults confirms scenarioComposedBasic
// actually records a partitioned, a dropped, and a delivered event, so the
// golden trace built from it below provably exercises all three faults
// composed rather than degenerating to one or two of them for this seed.
func TestComposedBasicExercisesAllThreeFaults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		trace := runScenario(t, scenarioComposedBasic(20), 42)

		var sawPartitioned, sawDropped, sawDelivered bool
		for _, l := range trace {
			switch {
			case l.partitioned:
				sawPartitioned = true
			case l.dropped:
				sawDropped = true
			default:
				sawDelivered = true
			}
		}
		if !sawPartitioned {
			t.Error("composed-basic trace never recorded a partitioned event")
		}
		if !sawDropped {
			t.Error("composed-basic trace never recorded a dropped event")
		}
		if !sawDelivered {
			t.Error("composed-basic trace never recorded a delivered event")
		}
	})
}

// TestClampingScenarioExercisesClampPath confirms scenarioClamping actually
// produces at least one unit whose effective delay was clamped up from its
// own drawn duration (faults.go's pending-queue clamp), so the golden trace
// built from it below is provably exercising that path rather than
// happening to avoid it for this seed.
func TestClampingScenarioExercisesClampPath(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		trace := runScenario(t, scenarioClamping(30), 7)

		clamped := 0
		for _, l := range trace {
			if !l.dropped && !l.partitioned && l.effective > l.drawn {
				clamped++
			}
		}
		if clamped == 0 {
			t.Fatal("clamping scenario recorded no clamped unit (effective > drawn); it does not exercise the pending-queue clamp for this seed")
		}
	})
}

func TestGoldenTraces(t *testing.T) {
	cases := []struct {
		sc   scenario
		seed int64
	}{
		{scenarioComposedBasic(20), 42},
		{scenarioClamping(30), 7},
	}

	for _, c := range cases {
		c := c
		t.Run(c.sc.name, func(t *testing.T) {
			var trace canonicalTrace
			synctest.Test(t, func(t *testing.T) {
				trace = runScenario(t, c.sc, c.seed)
			})

			path := filepath.Join("testdata", "traces", fmt.Sprintf("%s-seed%d.golden", c.sc.name, c.seed))

			if *updateGolden {
				if err := writeGolden(path, c.sc.name, c.seed, trace); err != nil {
					t.Fatal(err)
				}
				return
			}

			want, err := readGolden(path)
			if err != nil {
				t.Fatalf("reading golden file %s: %v (run with -update to generate it)", path, err)
			}
			if !trace.equal(want) {
				t.Fatalf("trace does not match golden file %s:\n%s", path, trace.diff(want))
			}
		})
	}
}

// TestReplayFromReportedSeed demonstrates the end-user workflow M3-3 exists
// to serve: a scenario run under a given seed reproduces byte-identically
// when re-run with that same seed. M1-5 shipped a fixed default seed with
// no accessor, by design (NewNetwork's godoc) -- there is nothing to
// "report" from a failing run and no recovery step to demonstrate.
// WithSeed's argument IS the reporting mechanism: whatever value a test
// passed is exactly the value a re-run replays.
func TestReplayFromReportedSeed(t *testing.T) {
	const reportedSeed = 12345
	sc := scenarioComposedBasic(20)

	var first, replay canonicalTrace
	synctest.Test(t, func(t *testing.T) { first = runScenario(t, sc, reportedSeed) })
	synctest.Test(t, func(t *testing.T) { replay = runScenario(t, sc, reportedSeed) })

	if !first.equal(replay) {
		t.Fatalf("replay with the reported seed produced a different trace:\n%s", first.diff(replay))
	}
}
