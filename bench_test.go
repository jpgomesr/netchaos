package netchaos

import (
	"testing"
	"time"
)

// Benchmarks exist to establish a baseline, not to defend a number. The repo
// had none, so there was no way to tell whether a later change — M6-8's timer
// guard, or any new fault kind — cost anything. Their first results are
// recorded in M6-7's PR as that baseline.
//
// Each builds its conn pair with newConnPairWithBound and installs the fault
// policy directly, rather than dialing through a Network. A dial would pull
// the accept goroutine and the listener hand-off into the measurement, which
// is not what any of these are trying to measure.

const benchPayload = 512

// BenchmarkRoundTrip measures the fault-free write/read path: the floor every
// other benchmark here is compared against.
func BenchmarkRoundTrip(b *testing.B) {
	client, server := newConnPairWithBound(&addr{network: "tcp", peer: "client"}, &addr{network: "tcp", peer: "server"}, 0, "tcp", defaultPipeBound)
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	payload := make([]byte, benchPayload)
	buf := make([]byte, benchPayload)

	b.ReportAllocs()
	b.SetBytes(benchPayload)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := client.Write(payload); err != nil {
			b.Fatal(err)
		}
		if _, err := server.Read(buf); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWriteUnderLoss measures the write path with packet loss
// configured. Rate 1.0 makes every unit take the drop branch, which is the
// branch worth measuring: it still draws, still records a trace event, and
// still has to release the unit's bufBytes accounting.
func BenchmarkWriteUnderLoss(b *testing.B) {
	client, server := newConnPairWithBound(&addr{network: "tcp", peer: "client"}, &addr{network: "tcp", peer: "server"}, 0, "tcp", defaultPipeBound)
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	installFaultPolicy(client.writePipe, faultPolicy{static: faultConfig{lossEnabled: true, lossRate: 1.0}})

	payload := make([]byte, benchPayload)

	b.ReportAllocs()
	b.SetBytes(benchPayload)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := client.Write(payload); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWriteUnderLatency is the one M6-8 is measured against: it is the
// path where armLatencyTimerLocked currently stops and recreates the timer on
// every admitted unit, so a per-write timer allocation shows up here or
// nowhere.
//
// Nothing reads, so the loop measures admission and timer handling rather
// than the reader's scheduling.
//
// The latency is an hour, so nothing is ever released mid-run and every write
// lands behind an existing pending head — precisely the steady state M6-8 is
// about. That means pending would otherwise grow for the whole run, so it is
// cleared every drainEvery iterations with the clock stopped. Without that a
// default -benchtime=1s run queues millions of units and measures the
// allocator instead. One write in drainEvery is therefore a genuine new head;
// at this ratio that is noise, and it keeps the benchmark honest about
// covering both branches.
func BenchmarkWriteUnderLatency(b *testing.B) {
	const (
		bound      = 1 << 24
		drainEvery = 4096
	)

	client, server := newConnPairWithBound(&addr{network: "tcp", peer: "client"}, &addr{network: "tcp", peer: "server"}, 0, "tcp", bound)
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	installFaultPolicy(client.writePipe, faultPolicy{static: faultConfig{
		latencyEnabled: true,
		latencyMin:     time.Hour,
		latencyMax:     time.Hour,
	}})

	payload := make([]byte, benchPayload)

	b.ReportAllocs()
	b.SetBytes(benchPayload)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := client.Write(payload); err != nil {
			b.Fatal(err)
		}
		if i%drainEvery == drainEvery-1 {
			b.StopTimer()
			p := client.writePipe
			p.mu.Lock()
			p.pending = p.pending[:0]
			p.bufBytes = 0
			p.mu.Unlock()
			b.StartTimer()
		}
	}
}
