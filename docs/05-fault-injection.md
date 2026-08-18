# 05 — Fault Injection

> **Status: implemented.** Latency, packet loss, and partition (below) are implemented as of M2-2/M2-3/M2-4. See [04 — API Design](04-api-design.md) for the configuration surface.
>
> **Known gap until M2-5:** configuring `WithLatency` and `WithPacketLoss` on the same `Network` today does not compose — whichever fault `Network.DialContext` wires up last silently replaces the other's delivery hook. Partition is unaffected by this gap: it always wraps whatever combination of latency/loss ended up installed, so partition composes correctly with either (or both, once M2-5 fixes their pairing) today. Composing latency and loss into one evaluator, in the documented `partition → loss → latency` order, is M2-5's job.

netchaos's v1 scope (per the root README's checklist) covers three fault categories: latency, packet loss, and partition. Each connection direction draws from its own seeded stream, derived from the `Network`'s master seed (see [04 — API Design § Determinism contract](04-api-design.md#determinism-contract) and [03 — Architecture](03-architecture.md#fault-injection-layer)), which is what makes an entire test run reproducible from a single seed value without one connection's fault sequence depending on how the scheduler interleaved it with another.

## Latency

**What it does:** Delays delivery of a write from one simulated peer to the other by some duration, instead of delivering it immediately.

**Fixed vs. ranged:** `WithLatency(min, max time.Duration)` supports both — passing equal `min` and `max` applies a fixed delay to every write; passing a range draws a duration uniformly from `[min, max]` per write, using the connection direction's own seeded stream. The draw happens even when `min == max`, so the draw sequence's length always tracks the number of writes, fixed delays included.

**Ordering:** Latency delays delivery; it never reorders. A connection direction releases writes in the order they were made, even when a later write happens to draw a shorter delay than an earlier one still in flight.

**Interaction with virtual time:** Implemented with a single live `time.AfterFunc` per connection direction (not one timer per write), a timer primitive `testing/synctest` already knows how to virtualize, so a test doesn't spend real wall-clock time waiting out the configured latency. See [03 — Architecture](03-architecture.md#composing-with-testingsynctest).

**Interaction with read deadlines:** A read deadlined shorter than the latency returns `os.ErrDeadlineExceeded`, not the delayed data. The data is not discarded — a subsequent read with a longer deadline still receives it once its delay elapses.

**Interaction with `Close`:** A write still held back by latency when its connection is closed is discarded, not delivered — it models bytes still in flight on the wire, not bytes already in the peer's receive buffer, so already-buffered (non-delayed) data still drains normally on close.

**What it's for:** Testing timeout handling and deadline logic — does your code correctly time out a request that's taking too long, does it retry with appropriate backoff, does a context deadline propagate correctly through a slow simulated call.

## Packet loss

**What it does:** Probabilistically drops a write instead of delivering it to the other simulated peer.

**Configuration:** `WithPacketLoss(rate float64)` takes a probability in `[0.0, 1.0]`. The unit of loss is the `Write` call — for each `Write`, the `Network`'s seeded RNG decides whether it's delivered or dropped as a whole. This couples loss behaviour to how the caller happens to chunk its writes (one 64 KiB write and sixty-four one-KiB writes see different loss behaviour at the same configured rate); that's an accepted v1 trade-off in exchange for a much simpler delivery queue, not an oversight — see [04 — API Design](04-api-design.md#fault-unit-and-drop-semantics).

**What a drop looks like to the reader:** A dropped write is a **silent gap**, not a visible error. The write is discarded, the peer's `Read` never observes those bytes, and — per `io.Writer`'s contract, which forbids returning a short count without a non-nil error — the call that issued the write still reports `n = len(p), nil`: full, successful delivery from the writer's point of view. This mirrors real packet loss, which is invisible to the sender at the socket layer.

**Seeded and reproducible:** Because the loss decision is drawn from the seeded RNG, the exact sequence of "this write succeeds / this write is dropped" is identical across runs for a given seed — this is what lets a flaky-seeming failure be pinned down and replayed deterministically. The Bernoulli trial is drawn even at rate `0.0` or `1.0`, so the draw sequence's length always tracks the number of writes.

**Relationship to partition:** rate `1.0` is behaviourally similar to partitioning the same peer pair — both drop every unit — but the two are distinct in intent and in draw consumption: loss at rate `1.0` still draws from the seeded stream on every unit, while partition (M2-4) consumes no draws at all.

**What it's for:** Testing retry logic — does your code detect a dropped write (via timeout or connection-level signal) and retry appropriately, does it eventually give up and return the right error after exhausting retries, does at-least-once vs. at-most-once semantics hold up.

## Partition

**What it does:** Drops **all** traffic between two named simulated peers, unlike packet loss which drops probabilistically. A partition is binary and (per the [API](04-api-design.md#dynamic-partition-control)) persists until explicitly healed. Unlike latency and packet loss, partition consumes no random draws — see [04 — API Design § Determinism contract](04-api-design.md#determinism-contract) — so partitioning one pair can never perturb another connection's fault sequence.

**Static vs. dynamic:** `WithPartition(peerA, peerB)` establishes a partition at `Network` construction time, present for the whole test. `Network.Partition` / `Network.Heal` allow inducing and resolving a partition mid-test — useful for scenarios like "the connection was healthy, then the network split, then it recovered." Pairs are unordered: naming either peer first identifies the same partition.

**Effect on dialing:** a `Dial`/`DialContext` call whose caller named itself via [`WithPeerName`](04-api-design.md#frozen-v1-surface) blocks — not fails fast — while its target peer is partitioned from it, returning once `Heal` clears the partition or the dial's context is done. This is the realistic choice: a partition drops the SYN, so a real dial hangs the same way, rather than surfacing a connection-refused-style error the way dialing an address nobody is listening on does. A dialer that never calls `WithPeerName` gets a synthesized identity no `Partition` call could ever target in advance, so it never blocks here.

**Effect on established connections:** once a partition is in effect, writes into it are accepted and silently discarded — the same silent-gap model as packet loss (see above) — and reads block until their deadline. `Heal` restores traffic on the existing connection with no re-dial required. Data written while partitioned is discarded, not queued for delivery once healed; this matches what a real partition looks like to a sender, whose kernel accepts into the socket buffer and discovers nothing until a timeout.

**What it's for:** Testing circuit breakers and failover logic — does your code detect a fully-unreachable peer and open its circuit breaker, does it correctly fail over to another peer, does it recover once the partition heals (`Heal`) without requiring a process restart.

## Reordering (deferred, not in v1)

Reordering is **not** in v1 scope. It was flagged as an open question — the README's introductory prose used to list it alongside latency, packet loss, and partition while the v1 checklist never included it — and has been resolved out, decided as part of [M0-1](tasks/m0-decisions-and-foundations.md#m0-1--resolve-whether-reordering-is-in-v1). See [06 — Scope & Roadmap](06-scope-and-roadmap.md#explicitly-out-of-scope-for-v1) for where it now lives on the deferred list, including the mechanic it would use if picked up post-v1.

Next: [06 — Scope & Roadmap](06-scope-and-roadmap.md) covers why v1 is bounded the way it is, and what's explicitly deferred.
