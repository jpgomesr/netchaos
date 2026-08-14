# 05 — Fault Injection

> **Status: design-stage.** Describes the intended mechanics for each fault type in netchaos's v1 scope. No implementation exists yet — see [04 — API Design](04-api-design.md) for the proposed configuration surface.

netchaos's v1 scope (per the root README's checklist) covers three fault categories: latency, packet loss, and partition. All three are driven by the same seeded random source owned by a `Network` (see [03 — Architecture](03-architecture.md#fault-injection-layer)), which is what makes an entire test run reproducible from a single seed value.

## Latency

**What it does:** Delays delivery of a write from one simulated peer to the other by some duration, instead of delivering it immediately.

**Fixed vs. ranged:** `WithLatency(min, max time.Duration)` supports both — passing equal `min` and `max` applies a fixed delay to every write; passing a range draws a duration uniformly from `[min, max]` per write, using the `Network`'s seeded RNG.

**Interaction with virtual time:** When a test runs inside `testing/synctest`, the delay should be implemented using timer primitives that `synctest` already knows how to virtualize, so the test doesn't spend real wall-clock time waiting out the configured latency. See [03 — Architecture](03-architecture.md#composing-with-testingsynctest).

**What it's for:** Testing timeout handling and deadline logic — does your code correctly time out a request that's taking too long, does it retry with appropriate backoff, does a context deadline propagate correctly through a slow simulated call.

## Packet loss

**What it does:** Probabilistically drops a write instead of delivering it to the other simulated peer.

**Configuration:** `WithPacketLoss(rate float64)` takes a probability in `[0.0, 1.0]`. The unit of loss is the `Write` call — for each `Write`, the `Network`'s seeded RNG decides whether it's delivered or dropped as a whole. This couples loss behaviour to how the caller happens to chunk its writes (one 64 KiB write and sixty-four one-KiB writes see different loss behaviour at the same configured rate); that's an accepted v1 trade-off in exchange for a much simpler delivery queue, not an oversight — see [04 — API Design](04-api-design.md#fault-unit-and-drop-semantics).

**What a drop looks like to the reader:** A dropped write is a **silent gap**, not a visible error. The write is discarded, the peer's `Read` never observes those bytes, and — per `io.Writer`'s contract, which forbids returning a short count without a non-nil error — the call that issued the write still reports `n = len(p), nil`: full, successful delivery from the writer's point of view. This mirrors real packet loss, which is invisible to the sender at the socket layer.

**Seeded and reproducible:** Because the loss decision is drawn from the seeded RNG, the exact sequence of "this write succeeds / this write is dropped" is identical across runs for a given seed — this is what lets a flaky-seeming failure be pinned down and replayed deterministically.

**What it's for:** Testing retry logic — does your code detect a dropped write (via timeout or connection-level signal) and retry appropriately, does it eventually give up and return the right error after exhausting retries, does at-least-once vs. at-most-once semantics hold up.

## Partition

**What it does:** Drops **all** traffic between two named simulated peers, unlike packet loss which drops probabilistically. A partition is binary and (per the [proposed API](04-api-design.md#dynamic-partition-control)) persists until explicitly healed.

**Static vs. dynamic:** `WithPartition(peerA, peerB)` establishes a partition at `Network` construction time, present for the whole test. `Network.Partition` / `Network.Heal` allow inducing and resolving a partition mid-test — useful for scenarios like "the connection was healthy, then the network split, then it recovered."

**What it's for:** Testing circuit breakers and failover logic — does your code detect a fully-unreachable peer and open its circuit breaker, does it correctly fail over to another peer, does it recover once the partition heals (`Heal`) without requiring a process restart.

## Reordering (deferred, not in v1)

Reordering is **not** in v1 scope. It was flagged as an open question — the README's introductory prose used to list it alongside latency, packet loss, and partition while the v1 checklist never included it — and has been resolved out, decided as part of [M0-1](tasks/m0-decisions-and-foundations.md#m0-1--resolve-whether-reordering-is-in-v1). See [06 — Scope & Roadmap](06-scope-and-roadmap.md#explicitly-out-of-scope-for-v1) for where it now lives on the deferred list, including the mechanic it would use if picked up post-v1.

Next: [06 — Scope & Roadmap](06-scope-and-roadmap.md) covers why v1 is bounded the way it is, and what's explicitly deferred.
