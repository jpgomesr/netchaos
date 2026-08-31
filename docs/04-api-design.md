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

// Added by M7-5 (issue #53, candidate 1), post-v0.1.0. Deterministic, not
// drawn -- see the determinism contract's Fault composition and draw
// discipline section.
func WithBandwidth(bytesPerSecond int) Option

// Added by M7-8 (issue #53, candidate 3), post-v0.1.0. Draws unconditionally
// past the partition gate, like WithLatency and WithPacketLoss -- see the
// determinism contract's Fault composition and draw discipline section.
func WithDuplication(rate float64) Option

func (n *Network) Dial(network, addr string) (net.Conn, error)
func (n *Network) DialContext(ctx context.Context, network, addr string) (net.Conn, error)
func (n *Network) Listen(network, addr string) (net.Listener, error)

// Added by M7-2 (issue #36), post-v0.1.0.
func (n *Network) DialerFor(name string) func(network, addr string) (net.Conn, error)

func (n *Network) Partition(peerA, peerB string)
func (n *Network) Heal(peerA, peerB string)

// Added by M7-4 (issue #50), post-v0.1.0. Same live semantics as
// Partition/Heal — see the determinism contract's Runtime fault mutation.
func (n *Network) SetLatency(min, max time.Duration)
func (n *Network) SetPacketLoss(rate float64)

func WithPeerName(ctx context.Context, name string) context.Context

var ErrUnsupportedNetwork = errors.New("netchaos: unsupported network")
var ErrConnectionRefused = errors.New("netchaos: connection refused")
var ErrAddressInUse = errors.New("netchaos: address already in use")
var ErrBacklogFull = errors.New("netchaos: accept backlog full")
```

The four error sentinels are matched with `errors.Is`; see [Error and no-op behaviour](#error-and-no-op-behaviour) below and each sentinel's own godoc for which call returns it and why.

**The surface above is what `v0.1.0` shipped. Four additions are accepted for `v0.2.0` and are not in it yet** — see [06 — Scope & Roadmap § Accepted for v0.2.0](06-scope-and-roadmap.md#accepted-for-v02) for the reasoning behind each, and note that all four are input to [M5-2](tasks/m5-hardening-and-ergonomics.md#m5-2--api-ergonomics-review-before-v100)'s review of this surface rather than exempt from it:

- `WithPipeBound` and `WithListenerBacklog` ([M6-17](tasks/m6-review-findings.md#m6-17--decide-whether-the-pipe-bound-and-listener-backlog-become-configurable)) — see [Functional options](#functional-options).
- `SetLatency` and `SetPacketLoss` ([M6-13](tasks/m6-review-findings.md#m6-13--decide-on-runtime-mutation-of-latency-and-loss)) — **shipped** in [M7-4](tasks/m7-v0.2.0-implementation.md#m7-4--setlatency-and-setpacketloss), after [M7-3](tasks/m7-v0.2.0-implementation.md#m7-3--widen-the-determinism-contract-for-runtime-fault-mutation) widened the contract ahead of the code, as that decision required. See [Runtime fault mutation](#runtime-fault-mutation).
- A fault-trace accessor ([M6-14](tasks/m6-review-findings.md#m6-14--decide-whether-to-export-fault-observability)), closing the deferral `M2-1` recorded. The full trace was chosen over counters, so the `faultEvent` shape becomes public API — a compatibility surface at `v1.0.0`, on a format [M3-3](tasks/m3-synctest-and-reproducibility.md) deliberately made high-friction to change.
- Options for the new fault kinds accepted by [M6-11](tasks/m6-review-findings.md#m6-11--decide-which-new-fault-kinds-if-any-enter-v02) (mid-stream reset, duplication, corruption). Bandwidth throttling and packet duplication, the first two of the four, **shipped** in [M7-5](tasks/m7-v0.2.0-implementation.md#m7-5--fault-kind-bandwidth-throttling) and [M7-8](tasks/m7-v0.2.0-implementation.md#m7-8--fault-kind-packet-duplication) as `WithBandwidth` and `WithDuplication`.

Address strings also change shape: [M6-10](tasks/m6-review-findings.md#m6-10--decide-whether-addresses-should-have-a-hostport-shape) accepted a synthesized port, so `net.SplitHostPort` succeeds against a netchaos address. That must land before `v1.0.0` — adding port structure afterwards breaks every address string a test prints.

No exported identifier returns an `error` from `NewNetwork` or from any `Option` — see [Error and no-op behaviour](#error-and-no-op-behaviour) below for why, and for what happens instead on misuse.

**`WithPeerName` is an addition made during [M2-4](tasks/m2-determinism-and-faults.md#m2-4--network-partition-static-and-dynamic), not a change of an earlier decision.** M0-5 froze the surface above it without an exported way for a dialer to declare its own peer identity — `Dial`'s signature has no room for one, and the identity is what `Partition`/`Heal` need to target a specific connection's dialing side. Without it, every dialer gets a synthesized, unpartitionable `ephemeral-N` identity (see `DialContext`'s godoc), which would make the root README's own `client` ↔ `server-a` partition example impossible to implement. `WithPeerName(ctx, name)` closes that gap: a caller passes `n.DialContext(netchaos.WithPeerName(ctx, "client"), "tcp", "server-a")` to make `"client"` targetable by `n.Partition("client", "server-a")`.

What it did *not* close is the same gap for `Dial` — the context it writes to has nowhere to live in a `net.Dial`-shaped signature. `DialerFor` ([M7-2](tasks/m7-v0.2.0-implementation.md#m7-2--networkdialerfor-make-a-drop-in-dialer-partition-targetable), issue [#36](https://github.com/jpgomesr/netchaos/issues/36)) is the answer to that, and the two are complements rather than alternatives: `WithPeerName` for a dial that has a context anyway, `DialerFor` for code that wants a plain dial function to hand to a client constructor.

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

// WithDuplication admits a delivered Write unit a second time, with the
// given probability, in [0.0, 1.0]. The decision is drawn from the
// Network's seeded RNG (its own stream, independent of loss and latency),
// so it is reproducible for a given seed. The duplicate reuses the same
// release timing WithLatency/WithBandwidth computed for the first copy.
func WithDuplication(rate float64) Option
```

### Fault scoping: global vs. per-peer-pair

Decided by [M0-2](tasks/m0-decisions-and-foundations.md#m0-2--decide-fault-scoping-global-vs-per-peer-pair): `WithLatency` and `WithPacketLoss` apply **globally**, to every simulated connection the `Network` handles. `WithDuplication` ([M7-8](tasks/m7-v0.2.0-implementation.md#m7-8--fault-kind-packet-duplication)) follows the same rule — a global rate, evaluated independently per connection direction via its own draw stream, exactly like loss and latency. `WithBandwidth` ([M7-5](tasks/m7-v0.2.0-implementation.md#m7-5--fault-kind-bandwidth-throttling)) also follows the same global rule, applied **per connection direction** rather than shared across a connection's two directions or across connections — a full-duplex conn is throttled to the configured rate each way, mirroring how latency, loss, and duplication already evaluate independently per direction. `WithPartition` (and its dynamic counterparts, `Network.Partition`/`Network.Heal`) are **pair-scoped** — they name the two peers affected. This is a deliberate asymmetry, not an oversight: partition is inherently a relationship between two peers, while latency, loss, bandwidth, and duplication model conditions of the network as a whole for v1/v0.2.0. Per-pair latency/loss scoping is a plausible post-v1 refinement once real usage patterns are clearer (see [06 — Scope & Roadmap](06-scope-and-roadmap.md#explicitly-out-of-scope-for-v1)). Faults are also symmetric by construction — `DialContext` installs the *same* `faultPolicy` on both directions and `newPairKey` sorts its arguments, so a pair is inherently undirected — and [M6-12](tasks/m6-review-findings.md#m6-12--decide-on-per-direction-asymmetric-faults) merged that question into the same gate, on the grounds that per-pair and per-direction scoping are two axes of one configuration model.

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

### Address shape

Decided by [M6-10](tasks/m6-review-findings.md#m6-10--decide-whether-addresses-should-have-a-hostport-shape) and implemented in [M7-1](tasks/m7-v0.2.0-implementation.md#m7-1--addresses-gain-a-synthesized-hostport-shape). **A netchaos address has a host and a port, and the two halves do different jobs:**

- **The host half is the identity.** It is what `Dial` and `Listen` resolve against, what `Partition` and `Heal` name, and the only half the internal `peerName` returns.
- **The port half is presentation.** It exists so a netchaos address is shaped like a real one — `net.SplitHostPort(conn.RemoteAddr().String())` succeeds, and code under test that logs, labels a metric, or allow-lists on the host half takes the same path it takes against the real stack. **Nothing in netchaos resolves, matches, or partitions on a port.**

Before this, the address *was* the peer name verbatim, so `RemoteAddr().String()` returned `"server"` and `SplitHostPort` failed against netchaos where it worked against `net`. That cost was paid by code the adopter did not want to change, which is the specific friction [01 — Vision](01-vision.md) says netchaos exists to remove.

What the split buys, concretely, is that addresses could gain structure without invalidating anything already written:

| Written | Peer identity | `Addr().String()` |
|---|---|---|
| `Listen("tcp", "server")` | `server` | `server:8000` (synthesized) |
| `Listen("tcp", "server:0")` | `server` | `server:8001` (synthesized — the `:0` form) |
| `Listen("tcp", "server:8080")` | `server` | `server:8080` (honoured) |
| `Dial("tcp", "server:8080")` | dials peer `server` | local `ephemeral-0:32768` |

So `Partition("server")` reaches a connection dialed to `"server:8080"`, and every `Partition` call written before addresses had ports keeps working. Two listeners whose addresses name the same host collide regardless of port — one peer, one listener — and a malformed address is rejected with a `*net.AddrError`, which is what `net.Listen` and `net.Dial` produce for the same input.

**Ports are synthesized in `Listen`/`Dial` order**, which the [determinism contract](#determinism-contract) already fixes, so nothing about the contract widens here. It does inherit the contract's stated limit unchanged, and that is worth naming because it is newly *visible*: two goroutines racing to `Listen` get their ports in whichever order the scheduler picks, and unlike a connection ordinal, a port shows up in `RemoteAddr().String()` and therefore in test failure output. The fix is the same one the contract already prescribes — establish connections in a fixed order before starting concurrent I/O.

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

**Effect on connection establishment (decided in [M2-4](tasks/m2-determinism-and-faults.md#m2-4--network-partition-static-and-dynamic)):** only a dialer that named itself — via `WithPeerName` or `DialerFor` — is subject to this check at all. For such a dialer, `DialContext` **blocks** for the duration of the partition, returning `ctx.Err()` only once the context is done — a partition drops the SYN, so a real dial into a partitioned peer hangs the same way rather than failing fast. Give it a context with a deadline if that is not the intended behaviour.

An unnamed dialer's synthesized `ephemeral-N` identity can never appear in a `Partition` call made before the dial completes, so it never blocks here regardless of any partition's state. **A bare `Dial` call is always in that second category:** `WithPeerName` records the identity on a `context.Context`, and `Dial`'s `net.Dial`-shaped signature has no context parameter, so a `Dial` call can never carry a peer name and therefore never blocks on a partition.

**That is a property of `Dial` itself, not of `net.Dial`-shaped dialing** — a distinction `M6-9` did not have, since it recorded the caveat as *permanent* for anything with `Dial`'s shape. [M7-2](tasks/m7-v0.2.0-implementation.md#m7-2--networkdialerfor-make-a-drop-in-dialer-partition-targetable) (issue [#36](https://github.com/jpgomesr/netchaos/issues/36)) removed the cause: `DialerFor(name)` returns a function with exactly `Dial`'s signature that carries a name, so a drop-in dialer can be partition-targetable after all. Two ways to get there now, and one that still does not work:

```go
client := myservice.NewClient(n.DialerFor("client"))   // targetable, net.Dial-shaped
c, _ := n.DialContext(netchaos.WithPeerName(ctx, "client"), "tcp", "server") // targetable, bounded by ctx
c, _ := n.Dial("tcp", "server")                        // NOT targetable — ephemeral identity
```

The trade between the first two is the wait: a `DialerFor` dialer blocks for the duration of a partition with no context to bound it, while `DialContext` can be given a deadline. Reach for `DialContext` when the dial must fail rather than hang.

The wait happens **before** the connection's ordinal is assigned, so a dial that blocks and is then cancelled does not burn an ordinal; see the [determinism contract](#determinism-contract)'s note on this.

**Effect on already-established connections:** writes into a partitioned pair are accepted and silently discarded (the same silent-gap model as packet loss — see [Fault unit and drop semantics](#fault-unit-and-drop-semantics)); reads block until their deadline. `Heal` restores traffic without requiring a re-dial. Data written while partitioned is **discarded**, not buffered for delivery on `Heal` — matching what a real partition looks like to a sender (the kernel doesn't retain it either).

## Error and no-op behaviour

Decided by [M0-5](tasks/m0-decisions-and-foundations.md#m0-5--freeze-the-v1-api-surface), since none of these had options written down before:

- **`Partition(peerA, peerB)` on peers that have never `Dial`ed or `Listen`ed** — no-op, no error. Peers are identified by address string; partitioning before either side has connected is legitimate test setup (e.g. "start partitioned"), and erroring here would force tests to order setup calls for no benefit.
- **`Heal(peerA, peerB)` with no partition currently in effect for that pair** — silent no-op. Idempotent healing is what makes it safe to call from `defer` or test cleanup without tracking partition state separately.
- **`Dial`/`DialContext` to an address nothing has `Listen`ed on** — returns an error, shaped like `*net.OpError` with a connection-refused-style underlying error, so code under test takes the same path it would against a real closed port. This is the point of being a `net.Conn`-compatible drop-in.
- **Error shape, decided by [M6-2](tasks/m6-review-findings.md#m6-2--decide-a-single-error-wrapping-policy-across-listen-dial-and-dialcontext):** every error returned by `Listen`, `Dial` and `DialContext` is a `*net.OpError`, uniformly — including an unsupported network, an address already in use, and a context that was already done. Real `net.Listen`/`net.Dial` return `*net.OpError` for all of these, and code under test that type-asserts to it, or calls `Timeout()`/`Temporary()` on the result, must not take a different path against netchaos than against the standard library. Before `M6-2` the shape depended on which line produced the error, and that split was an artifact of which milestone wrote which line, not a rule.

  Every sentinel remains matchable with `errors.Is`, since `OpError` unwraps — that is the comparison style this document and `errors.go` specify. Direct `==` comparison against a sentinel no longer works; that is the cost the decision accepted, taken while `v0.1.0` has no external users.
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

- `connectionOrdinal` is assigned in the order `Dial`/`Listen` establish the connection — the same ordering the contract already fixes. A `Dial` blocked on a partition (see [Dynamic partition control](#dynamic-partition-control)) has not yet established, so it has not yet consumed an ordinal. A dial that never establishes never burns one, on any path: the ordinal is taken only after the partition wait clears, the listener lookup succeeds, *and* a slot in that listener's accept queue has been claimed — so a dial that fails with `ErrConnectionRefused` or `ErrBacklogFull` returns without shifting the ordinal, and therefore the RNG stream, of any connection established after it. (Claiming the slot before taking the ordinal is what makes this true rather than nearly true; see [M6-1](tasks/m6-review-findings.md#m6-1--reconcile-ordinal-assignment-with-the-determinism-contract), which found the code consuming an ordinal on a failed enqueue.)
- `direction` separates the two directions of a full-duplex connection, so peer A's writes and peer B's writes draw from independent streams.
- `faultKind` separates latency draws, packet-loss draws, and duplication draws ([M7-8](tasks/m7-v0.2.0-implementation.md#m7-8--fault-kind-packet-duplication)) from each other on the same connection and direction, so that adding `WithLatency` to an existing test does not shift that test's packet-loss or duplication sequence, and so on for every pair.

Because a connection's draw sequence depends only on its own ordinal, direction, and fault kind — never on what any other connection did, or on how the scheduler interleaved them — the fault sequence on that connection is reproducible independent of concurrent activity elsewhere in the `Network`. Partition consumes no random draws at all (see [05 — Fault Injection](05-fault-injection.md#partition)), so it doesn't perturb any stream.

**The guarantee, precisely:** for a fixed seed and a fixed *order* in which `Dial`, `Listen`, `Partition`, `Heal`, `SetLatency`, and `SetPacketLoss` are called, each resulting connection produces an identical sequence of injected faults across runs and across machines. This is the property that lets a failing test be reproduced reliably from a seed value alone — analogous to how `go test -run` plus a fixed input reproduces a deterministic unit test failure.

### Runtime fault mutation

Decided by [M6-13](tasks/m6-review-findings.md#m6-13--decide-on-runtime-mutation-of-latency-and-loss) and written here **before** `SetLatency`/`SetPacketLoss` exist, which was the substantive half of that decision: the contract is the library's core promise, and settling it after an implementation had already shipped would mean the code, not this document, had picked the answer. [M7-4](tasks/m7-v0.2.0-implementation.md#m7-4--setlatency-and-setpacketloss) implements against what follows.

**The setters are ordered calls, exactly like `Partition` and `Heal`.** They join the list in the guarantee above. A test that calls them in a fixed order relative to its other `Network` calls reproduces exactly; one that calls them from a goroutine racing other `Network` calls does not, for the same reason and with the same fix as a racing `Dial`.

**A change applies to already-established connections, not only to subsequent dials.** This is the whole motivation — "healthy, then degraded, then healthy" on a live connection, without rebuilding the `Network` and resetting every ordinal — and it is what makes the setters symmetric with `Partition`/`Heal` rather than a differently-shaped thing that happens to share a noun. A connection's fault configuration is therefore read per unit rather than captured at dial time.

**What changing the configuration does *not* do is perturb any stream.** Draw discipline is unchanged: every configured fault still draws unconditionally on every unit past the partition gate, so a unit's draw index still equals the unit index on its direction. Turning latency off with `SetLatency(0, 0)` is a change to the *value* a draw produces, not to whether the draw happens. Two consequences worth stating because they are easy to assume wrong:

- Disabling a fault mid-run does not "save" draws, and re-enabling it does not resume a stream from where it paused. There is one stream per `(ordinal, direction, kind)` and it advances one value per unit regardless.
- A fault that was never configured at construction has no stream consumption to begin with, so a setter that enables one mid-run **does** begin drawing from that kind's stream — which shifts nothing else, since kinds are independent by derivation.

**The limit this adds, stated rather than implied:** the guarantee fixes the order of the setter *relative to other `Network` calls*, not relative to in-flight I/O on another goroutine. If a test writes in a loop on one goroutine and calls `SetPacketLoss` from another, **which unit is the first to see the new rate is decided by the scheduler, not by the seed.** There is no per-unit boundary the contract can name there. The fix is the same one the contract already prescribes for concurrent establishment: sequence the setter against the I/O the test cares about — write, then set, then write — rather than expecting netchaos to guess where the boundary should fall.

**The limit, stated rather than implied:** the guarantee is about the *order of `Network` method calls*, not about wall-clock concurrency of the I/O itself. If a test races two goroutines to call `Dial` concurrently, which one gets which `connectionOrdinal` is nondeterministic, and the guarantee does not apply to that race — the fix is for the test to fix the dial order (e.g. by dialing sequentially before starting concurrent I/O), not for netchaos to guess an ordering. Concurrent I/O on **already-established** connections is fully covered; concurrent, unordered *establishment* of connections is not.

**Fault composition and draw discipline, permanent as of [M2-5](tasks/m2-determinism-and-faults.md#m2-5--fault-composition-rules):** when more than one fault is configured on the same connection direction, they are evaluated in exactly one fixed order — **partition, then packet loss, then bandwidth, then latency, then duplication** ([M7-8](tasks/m7-v0.2.0-implementation.md#m7-8--fault-kind-packet-duplication)) — by a single evaluator, not independent hooks that happen to run in some order. Partition short-circuits before any draw: a partitioned unit is discarded with **zero** stream consumption, since partition must stay deterministic by nature and a draw there would perturb every later unit's loss/latency/duplication sequence on that direction. Packet loss, evaluated next, is the same kind of gate for bandwidth and duplication both: a dropped unit never reaches the link (so it costs no simulated transmission time) and is never duplicated (so there is nothing for the second copy to attach to).

A unit that clears the partition gate draws from **every configured fault's stream unconditionally** — packet loss's Bernoulli trial, latency's duration, and duplication's Bernoulli trial are all drawn even when an earlier fault in the order already decided to drop the unit (e.g. a unit loss drops still draws a latency duration and a duplication decision it will never use). This keeps each drawing fault's draw index equal to the unit index on that direction, independent of what any other fault decided, which is what makes a fault trace diffable unit-for-unit across runs. Changing this discipline later — including which faults draw unconditionally versus lazily — is a breaking change to this contract, not an implementation detail free to vary.

**This discipline binds fault kinds that draw.** Bandwidth ([M7-5](tasks/m7-v0.2.0-implementation.md#m7-5--fault-kind-bandwidth-throttling)) is the one fault kind that does not: its delay is a deterministic function of a unit's size and the configured rate, with no `faultKind` byte and no derived stream. A kind with no stream has no draw index to misalign, so — the same way partition's zero-draw short-circuit cannot perturb loss or latency — enabling bandwidth cannot perturb any other configured fault's sequence either. Duplication is not an exception the way bandwidth is: it is a per-unit Bernoulli decision with its own stream (`kindDuplicate`), so it draws unconditionally like packet loss and latency, and the duplicate it admits reuses the release timing bandwidth/latency already computed for the original rather than drawing its own.

Next: [05 — Fault Injection](05-fault-injection.md) details the mechanics behind each option above.
