# 04 — API Design

> **API implemented and shipped.** [M0-5](tasks/m0-decisions-and-foundations.md#m0-5--freeze-the-v1-api-surface) froze the v1 API surface below; every exported identifier it lists is built, tested, and documented via godoc, plus one addition made during implementation (`WithPeerName`, noted where it appears below). The surface may still change before a `v1.0.0` (see [07 — Contributing](07-contributing.md)), but is not expected to for `v0.1.0`.

This is a concrete elaboration of the code sample from the root [`README.md`](../README.md), scoped to the v1 fault types in [06 — Scope & Roadmap](06-scope-and-roadmap.md).

## Frozen v1 surface

The v1 fault set is exactly **three** categories — latency, packet loss, partition. Reordering was considered and decided out of v1 by [M0-1](tasks/m0-decisions-and-foundations.md#m0-1--resolve-whether-reordering-is-in-v1); see [06 — Scope & Roadmap](06-scope-and-roadmap.md#explicitly-out-of-scope-for-v1) for where it now lives on the deferred list. No exported identifier below relates to reordering.

Every exported identifier v1 ships, with its final signature:

```go
type Network struct{ /* unexported */ }

func NewNetwork(opts ...Option) *Network

type Option func(*networkConfig)

func WithSeed(seed int64) Option
func WithLatency(min, max time.Duration) Option
func WithPacketLoss(rate float64) Option
func WithPartition(peerA, peerB string) Option

func (n *Network) Dial(network, addr string) (net.Conn, error)
func (n *Network) DialContext(ctx context.Context, network, addr string) (net.Conn, error)
func (n *Network) Listen(network, addr string) (net.Listener, error)

func (n *Network) Partition(peerA, peerB string)
func (n *Network) Heal(peerA, peerB string)

func WithPeerName(ctx context.Context, name string) context.Context

var ErrUnsupportedNetwork = errors.New("netchaos: unsupported network")
var ErrConnectionRefused = errors.New("netchaos: connection refused")
var ErrAddressInUse = errors.New("netchaos: address already in use")
var ErrBacklogFull = errors.New("netchaos: accept backlog full")
```

The four error sentinels are matched with `errors.Is`; see [Error and no-op behaviour](#error-and-no-op-behaviour) below and each sentinel's own godoc for which call returns it and why.

**The surface above is what `v0.1.0` shipped. Four additions are accepted for `v0.2.0` and are not in it yet** — see [06 — Scope & Roadmap § Accepted for v0.2.0](06-scope-and-roadmap.md#accepted-for-v02) for the reasoning behind each, and note that all four are input to [M5-2](tasks/m5-hardening-and-ergonomics.md#m5-2--api-ergonomics-review-before-v100)'s review of this surface rather than exempt from it:

- `WithPipeBound` and `WithListenerBacklog` ([M6-17](tasks/m6-review-findings.md#m6-17--decide-whether-the-pipe-bound-and-listener-backlog-become-configurable)) — see [Functional options](#functional-options).
- `SetLatency` and `SetPacketLoss` ([M6-13](tasks/m6-review-findings.md#m6-13--decide-on-runtime-mutation-of-latency-and-loss)) — gated on the [determinism contract](#determinism-contract) being widened first.
- A fault-trace accessor ([M6-14](tasks/m6-review-findings.md#m6-14--decide-whether-to-export-fault-observability)), closing the deferral `M2-1` recorded. The full trace was chosen over counters, so the `faultEvent` shape becomes public API — a compatibility surface at `v1.0.0`, on a format [M3-3](tasks/m3-synctest-and-reproducibility.md) deliberately made high-friction to change.
- Options for the new fault kinds accepted by [M6-11](tasks/m6-review-findings.md#m6-11--decide-which-new-fault-kinds-if-any-enter-v02) (throttling, mid-stream reset, duplication, corruption).

Address strings also change shape: [M6-10](tasks/m6-review-findings.md#m6-10--decide-whether-addresses-should-have-a-hostport-shape) accepted a synthesized port, so `net.SplitHostPort` succeeds against a netchaos address. That must land before `v1.0.0` — adding port structure afterwards breaks every address string a test prints.

No exported identifier returns an `error` from `NewNetwork` or from any `Option` — see [Error and no-op behaviour](#error-and-no-op-behaviour) below for why, and for what happens instead on misuse.

**`WithPeerName` is an addition made during [M2-4](tasks/m2-determinism-and-faults.md#m2-4--network-partition-static-and-dynamic), not a change of an earlier decision.** M0-5 froze the surface above it without an exported way for a dialer to declare its own peer identity — `Dial`'s signature has no room for one, and the identity is what `Partition`/`Heal` need to target a specific connection's dialing side. Without it, every dialer gets a synthesized, unpartitionable `ephemeral:N` identity (see `DialContext`'s godoc), which would make the root README's own `client` ↔ `server-a` partition example impossible to implement. `WithPeerName(ctx, name)` closes that gap: a caller passes `n.DialContext(netchaos.WithPeerName(ctx, "client"), "tcp", "server-a")` to make `"client"` targetable by `n.Partition("client", "server-a")`.

## Package shape

```go
package netchaos // import "github.com/jpgomesr/netchaos"
```

A single flat package for v1 — no subpackages. If the API grows enough to warrant it (e.g., separating fault types from the core network simulation), that's a post-v1 concern.

## `Network`

The central type. Owns the seeded RNG, the configured fault policy, and simulated topology state.

```go
// Network is a simulated, in-process network with deterministic fault
// injection. A Network is created per test scenario via NewNetwork.
type Network struct {
    // unexported fields: seeded RNG, fault policy, topology state
}

// NewNetwork constructs a Network configured by the given Options.
func NewNetwork(opts ...Option) *Network
```

## Functional options

Consistent with the idiomatic Go functional-options pattern, and with the shape already implied by the README's example (`netchaos.WithPacketLoss(0.3)`, etc.):

```go
// Option configures a Network at construction time.
type Option func(*networkConfig)

// WithSeed makes fault injection deterministic and reproducible: the same
// seed, with the same order of Dial/Listen/Partition/Heal calls, always
// produces the same sequence of injected faults on each connection. Seeds a
// per-connection stream derivation, not a single shared random source — see
// the determinism contract for the exact guarantee and its limits.
func WithSeed(seed int64) Option

// WithLatency delays delivery of writes by a duration drawn uniformly from
// [min, max]. Passing an equal min and max applies fixed latency.
func WithLatency(min, max time.Duration) Option

// WithPacketLoss drops whole Write calls with the given probability, in
// [0.0, 1.0]. A dropped write is silent: it reports success to its caller
// (n == len(p), nil error) and never arrives at the peer. The decision is
// drawn from the Network's seeded RNG, so it is reproducible for a given
// seed.
func WithPacketLoss(rate float64) Option

// WithPartition marks all traffic between the named peers as dropped,
// starting immediately when the Network is constructed. Partitions
// configured this way are static for the lifetime of the Network; see
// Network.Partition / Network.Heal for dynamic control during a test.
func WithPartition(peerA, peerB string) Option
```

### Fault scoping: global vs. per-peer-pair

Decided by [M0-2](tasks/m0-decisions-and-foundations.md#m0-2--decide-fault-scoping-global-vs-per-peer-pair): `WithLatency` and `WithPacketLoss` apply **globally**, to every simulated connection the `Network` handles. `WithPartition` (and its dynamic counterparts, `Network.Partition`/`Network.Heal`) are **pair-scoped** — they name the two peers affected. This is a deliberate asymmetry, not an oversight: partition is inherently a relationship between two peers, while latency and loss model conditions of the network as a whole for v1. Per-pair latency/loss scoping is a plausible post-v1 refinement once real usage patterns are clearer (see [06 — Scope & Roadmap](06-scope-and-roadmap.md#explicitly-out-of-scope-for-v1)). Faults are also symmetric by construction — `DialContext` installs the *same* `faultPolicy` on both directions and `newPairKey` sorts its arguments, so a pair is inherently undirected — and [M6-12](tasks/m6-review-findings.md#m6-12--decide-on-per-direction-asymmetric-faults) merged that question into the same gate, on the grounds that per-pair and per-direction scoping are two axes of one configuration model.

### Fault unit and drop semantics

Decided by [M0-3](tasks/m0-decisions-and-foundations.md#m0-3--decide-fault-granularity-per-write-vs-per-simulated-packet): the unit of fault application is the **`Write` call**, not a simulated packet. Each `Write` is delayed as a whole or dropped as a whole; there is no segmentation layer in v1. This couples fault behaviour to how the caller happens to chunk its writes — accepted as a v1 trade-off in exchange for a much simpler delivery queue, revisitable post-v1 if real usage shows it matters.

A dropped write is a **silent gap**: the write is discarded, the peer's `Read` never observes those bytes, and the call that issued the write still reports full success. Concretely, given `io.Writer`'s contract — a non-nil error is required whenever `n < len(p)` — a silent drop cannot report a short count or an error, so it **must** return `n = len(p), nil`. This is not a stylistic choice; it is the only return value consistent with the drop being silent, and it mirrors what a real socket does when a packet is lost downstream — the sender's kernel doesn't know either.

## Dialing and listening

```go
// Dial creates a simulated connection from the calling peer to addr within
// this Network, subject to the Network's fault policy. It has the same
// signature shape as net.Dial so it can be used as a drop-in for code that
// accepts a dial function. Dial(network, addr) is equivalent to
// DialContext(context.Background(), network, addr).
func (n *Network) Dial(network, addr string) (net.Conn, error)

// DialContext is Dial with a context: the dial is aborted, returning
// ctx.Err(), if ctx is cancelled before the simulated connection is
// established. Ships in v1 (decided by M0-5) because http.Transport's
// DialContext and grpc.WithContextDialer both expect this shape — a
// library claiming drop-in transport compatibility needs it from the
// start rather than adding it as a breaking follow-up.
func (n *Network) DialContext(ctx context.Context, network, addr string) (net.Conn, error)

// Listen registers a simulated listener at addr within this Network.
// Connections dialed to addr from elsewhere in the Network are delivered
// to this listener's Accept, subject to the Network's fault policy.
func (n *Network) Listen(network, addr string) (net.Listener, error)
```

The intent is for `Network.Dial` (and `Network.DialContext`) to be usable anywhere calling code accepts a `func(network, addr string) (net.Conn, error)` (or its context-aware equivalent) — e.g., as the dial function passed into an HTTP transport, a gRPC dialer, or a hand-rolled client constructor. This is the "swap a `net.Dial` call for a factory" adoption path described in [03 — Architecture](03-architecture.md#design-goals-driving-the-architecture).

## Dynamic partition control

For scenarios that need to partition and heal a link *during* a test (not just at construction time):

```go
// Partition drops all subsequent traffic between the named peers until
// Heal is called for the same pair.
func (n *Network) Partition(peerA, peerB string)

// Heal removes a previously established partition between the named peers.
func (n *Network) Heal(peerA, peerB string)
```

Pairs are unordered: `Partition("a", "b")` and `Partition("b", "a")` name the same pair, and either heals the other.

**Effect on connection establishment (decided in [M2-4](tasks/m2-determinism-and-faults.md#m2-4--network-partition-static-and-dynamic)):** `Dial`/`DialContext` **blocks** for the duration of the partition, returning `ctx.Err()` only once the context is done — a partition drops the SYN, so a real dial into a partitioned peer hangs the same way rather than failing fast. `Dial` (which uses `context.Background()`) therefore hangs forever against a partitioned peer; give it a context with a deadline if that's not the intended behaviour. Only a dialer that named itself via `WithPeerName` can be blocked this way — an unnamed dialer's synthesized `ephemeral:N` identity can never appear in a `Partition` call made before the dial completes, so it never blocks on this check regardless of any partition's state. The wait happens **before** the connection's ordinal is assigned, so a dial that never establishes does not burn an ordinal; see the [determinism contract](#determinism-contract)'s note on this.

**Effect on already-established connections:** writes into a partitioned pair are accepted and silently discarded (the same silent-gap model as packet loss — see [Fault unit and drop semantics](#fault-unit-and-drop-semantics)); reads block until their deadline. `Heal` restores traffic without requiring a re-dial. Data written while partitioned is **discarded**, not buffered for delivery on `Heal` — matching what a real partition looks like to a sender (the kernel doesn't retain it either).

## Error and no-op behaviour

Decided by [M0-5](tasks/m0-decisions-and-foundations.md#m0-5--freeze-the-v1-api-surface), since none of these had options written down before:

- **`Partition(peerA, peerB)` on peers that have never `Dial`ed or `Listen`ed** — no-op, no error. Peers are identified by address string; partitioning before either side has connected is legitimate test setup (e.g. "start partitioned"), and erroring here would force tests to order setup calls for no benefit.
- **`Heal(peerA, peerB)` with no partition currently in effect for that pair** — silent no-op. Idempotent healing is what makes it safe to call from `defer` or test cleanup without tracking partition state separately.
- **`Dial`/`DialContext` to an address nothing has `Listen`ed on** — returns an error, shaped like `*net.OpError` with a connection-refused-style underlying error, so code under test takes the same path it would against a real closed port. This is the point of being a `net.Conn`-compatible drop-in.
- **Invalid option values** (e.g. `WithPacketLoss` outside `[0.0, 1.0]`, or `WithLatency` with `min > max`) — **`NewNetwork` panics.** `NewNetwork` returns no error and `Option` returns no error; both signatures are fixed above specifically so the README's and this doc's single-expression construction examples work as written. Invalid option values are programmer errors in test code, not runtime conditions a test needs to handle — a panic at construction time is the immediate, unambiguous signal, consistent with how the standard library treats similar construction-time misuse (e.g. `regexp.MustCompile`). Changing this to an `error` return later would be a breaking signature change to both `Option` and `NewNetwork`; it is being decided now, not deferred, because reversing it after v1 tags is expensive. (Implementation is M2-6's concern — this only fixes the *behaviour*, not the validation code.)

## Full usage sketch

Restating the README's example with the API surface above made explicit:

```go
package myservice_test

import (
    "testing"
    "testing/synctest"
    "time"

    "github.com/jpgomesr/netchaos"
)

func TestRetryOnPacketLoss(t *testing.T) {
    synctest.Test(t, func(t *testing.T) {
        network := netchaos.NewNetwork(
            netchaos.WithPacketLoss(0.3),
            netchaos.WithLatency(50*time.Millisecond, 150*time.Millisecond),
            netchaos.WithSeed(42), // deterministic, reproducible failures
        )

        client := myservice.NewClient(network.Dial)

        got, err := client.FetchWithRetry("resource-id")
        if err != nil {
            t.Fatalf("expected retry to succeed despite packet loss, got: %v", err)
        }
        if got != "resource-id" {
            t.Fatalf("got %q, want %q", got, "resource-id")
        }
    })
}
```

`myservice.NewClient`/`FetchWithRetry` stand in for your own client and its retry policy; the only netchaos-specific line is handing your client `network.Dial`, a `func(network, addr string) (net.Conn, error)`. A fully self-contained, compiled version of this scenario lives as `TestReadmeUsageSnippet` in `example_test.go`, alongside a runnable `Example` per headline feature (`ExampleWithLatency`, `ExampleWithPacketLoss`, `ExampleNetwork_Partition`, `ExampleWithSeed`).

## Determinism contract

Decided by [M0-4](tasks/m0-decisions-and-foundations.md#m0-4--design-the-determinism-under-concurrency-model), because the naive approach — one shared `rand.Rand` behind a mutex — is deterministic only when a test's I/O is strictly sequential. The moment two goroutines do simulated I/O concurrently (a client writing while a server reads, two clients against one server), the *order in which goroutines reach a shared RNG* is decided by the Go scheduler, not by the seed — so the same seed and the same calls can produce a different fault sequence between runs. A mutex there makes the RNG race-free, not deterministic.

**The model: per-connection derived streams.** `WithSeed` seeds a derivation function, not a shared `rand.Rand`. Each simulated connection gets its own RNG stream, derived from `(masterSeed, connectionOrdinal, direction, faultKind)`:

- `connectionOrdinal` is assigned in the order `Dial`/`Listen` establish the connection — the same ordering the contract already fixes. A `Dial` blocked on a partition (see [Dynamic partition control](#dynamic-partition-control)) has not yet established, so it has not yet consumed an ordinal; the ordinal is assigned only once the wait clears and the listener lookup succeeds, so a dial that never establishes never burns one.
- `direction` separates the two directions of a full-duplex connection, so peer A's writes and peer B's writes draw from independent streams.
- `faultKind` separates latency draws from packet-loss draws on the same connection and direction, so that adding `WithLatency` to an existing test does not shift that test's packet-loss sequence, and vice versa.

Because a connection's draw sequence depends only on its own ordinal, direction, and fault kind — never on what any other connection did, or on how the scheduler interleaved them — the fault sequence on that connection is reproducible independent of concurrent activity elsewhere in the `Network`. Partition consumes no random draws at all (see [05 — Fault Injection](05-fault-injection.md#partition)), so it doesn't perturb any stream.

**The guarantee, precisely:** for a fixed seed and a fixed *order* in which `Dial`, `Listen`, `Partition`, and `Heal` are called, each resulting connection produces an identical sequence of injected faults across runs and across machines. This is the property that lets a failing test be reproduced reliably from a seed value alone — analogous to how `go test -run` plus a fixed input reproduces a deterministic unit test failure.

**A widening already accepted, not yet implemented:** [M6-13](tasks/m6-review-findings.md#m6-13--decide-on-runtime-mutation-of-latency-and-loss) accepted `SetLatency`/`SetPacketLoss` into `v0.2.0` scope, which adds a new input to the guarantee above. The wording is deliberately narrow today — it fixes the order of `Dial`, `Listen`, `Partition` and `Heal` and nothing else — so **any setter joins that list, and this contract must be rewritten to say so before the setters are implemented, not after.** That ordering is the point: the contract is the library's core promise, and widening it is the substantive part of that change rather than a consequence of it. The same task also notes the implementation cost this implies: `latencyEnabled`, `latencyMin`, `latencyMax`, `lossEnabled` and `lossRate` are written once in `NewNetwork` and read **lock-free** on the per-unit path today.

**The limit, stated rather than implied:** the guarantee is about the *order of `Network` method calls*, not about wall-clock concurrency of the I/O itself. If a test races two goroutines to call `Dial` concurrently, which one gets which `connectionOrdinal` is nondeterministic, and the guarantee does not apply to that race — the fix is for the test to fix the dial order (e.g. by dialing sequentially before starting concurrent I/O), not for netchaos to guess an ordering. Concurrent I/O on **already-established** connections is fully covered; concurrent, unordered *establishment* of connections is not.

**Fault composition and draw discipline, permanent as of [M2-5](tasks/m2-determinism-and-faults.md#m2-5--fault-composition-rules):** when more than one fault is configured on the same connection direction, they are evaluated in exactly one fixed order — **partition, then packet loss, then latency** — by a single evaluator, not three independent hooks that happen to run in some order. Partition short-circuits before any draw: a partitioned unit is discarded with **zero** stream consumption, since partition must stay deterministic by nature and a draw there would perturb every later unit's loss/latency sequence on that direction. A unit that clears the partition gate draws from **every configured fault's stream unconditionally** — packet loss's Bernoulli trial and latency's duration are both drawn even when an earlier fault in the order already decided to drop the unit (e.g. a unit loss drops still draws a latency duration it will never use). This keeps each configured fault's draw index equal to the unit index on that direction, independent of what any other fault decided, which is what makes a fault trace diffable unit-for-unit across runs. Changing this discipline later — including which faults draw unconditionally versus lazily — is a breaking change to this contract, not an implementation detail free to vary.

Next: [05 — Fault Injection](05-fault-injection.md) details the mechanics behind each option above.
