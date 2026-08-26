// Package netchaos provides deterministic, in-process simulated net.Conn
// and net.Listener implementations for Go tests. A Network, created with
// NewNetwork, lets peers Dial and Listen against each other subject to a
// configurable fault policy: latency, packet loss, and network partition.
// It is meant to be imported directly into a test — no external process,
// proxy, or daemon — so a test can prove its retry logic, timeout handling,
// or circuit breaker reacts correctly to a bad network.
//
// See the package-level Example for a minimal dial/listen round trip, and
// the other Example functions for latency, packet loss, partition, and
// seeding.
//
// # Determinism
//
// A Network's fault sequence is reproducible from a seed (WithSeed): the
// same seed, with the same order of Dial/Listen/Partition/Heal calls,
// always produces the same sequence of injected faults on every connection.
// Each connection derives its own RNG stream from the seed, its
// establishment order, and its direction, so one connection's fault
// sequence never depends on how the Go scheduler interleaved it with
// another's — concurrent reads and writes on already-established
// connections are fully covered by the guarantee.
//
// The guarantee has a real limit: it is about the order Network methods are
// called in, not about wall-clock concurrency of establishment itself. If a
// test races two goroutines to Dial concurrently, which one gets which
// connection ordinal — and therefore which RNG stream — is decided by the
// scheduler, not the seed. Fix the dial order (e.g. dial sequentially
// before starting concurrent I/O) if a test needs to reproduce exactly.
//
// # Using netchaos with testing/synctest
//
// netchaos composes with testing/synctest so that injected latency costs no
// real wall-clock time. Doing so imposes constraints that otherwise surface
// as a confusing panic:
//
//   - A Network must be constructed inside the synctest.Test bubble that
//     will use it. synctest panics if a channel or timer created inside a
//     bubble is later operated on from outside it, and every conn,
//     listener, and deadline timer a Network creates is reachable from
//     goroutines running inside that bubble.
//   - Connections and listeners must not be shared across bubbles.
//   - A closure passed to synctest.Test should take the bubble's own
//     *testing.T rather than closing over an outer one. Cleanup registered
//     against the outer t (via t.Cleanup) runs only after the whole test
//     function returns, by which point the bubble is gone.
//
// netchaos also works correctly outside a synctest bubble — but then
// latency consumes real wall-clock time, so don't assume virtual time
// comes for free without one.
//
// # Fault composition
//
// When more than one fault is configured on the same connection direction,
// they are evaluated in one fixed order: partition, then packet loss, then
// latency. A partitioned unit is discarded before any random draw happens,
// so partition never perturbs the loss or latency sequence. Every other
// configured fault draws from its stream unconditionally, even if an
// earlier fault in the order already dropped the unit — a unit dropped by
// packet loss still draws (and discards) a latency duration. This keeps
// each fault's draw index locked to the unit index, which is what makes a
// fault trace diffable across runs.
//
// # Reproducing a failure
//
// A test that fails under fault injection can be reproduced by re-running
// it with the seed that produced the failure, passed via WithSeed — the
// same seed and the same call order always regenerate the same fault
// sequence.
package netchaos
