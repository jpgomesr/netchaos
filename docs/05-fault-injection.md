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

**Configuration:** `WithPacketLoss(rate float64)` takes a probability in `[0.0, 1.0]`. For each write (or, depending on how granular the v1 model ends up, each simulated packet within a write), the `Network`'s seeded RNG decides whether it's delivered or dropped.

**Seeded and reproducible:** Because the loss decision is drawn from the seeded RNG, the exact sequence of "this write succeeds / this write is dropped" is identical across runs for a given seed — this is what lets a flaky-seeming failure be pinned down and replayed deterministically.

**What it's for:** Testing retry logic — does your code detect a dropped write (via timeout or connection-level signal) and retry appropriately, does it eventually give up and return the right error after exhausting retries, does at-least-once vs. at-most-once semantics hold up.

## Partition

**What it does:** Drops **all** traffic between two named simulated peers, unlike packet loss which drops probabilistically. A partition is binary and (per the [proposed API](04-api-design.md#dynamic-partition-control)) persists until explicitly healed.

**Static vs. dynamic:** `WithPartition(peerA, peerB)` establishes a partition at `Network` construction time, present for the whole test. `Network.Partition` / `Network.Heal` allow inducing and resolving a partition mid-test — useful for scenarios like "the connection was healthy, then the network split, then it recovered."

**What it's for:** Testing circuit breakers and failover logic — does your code detect a fully-unreachable peer and open its circuit breaker, does it correctly fail over to another peer, does it recover once the partition heals (`Heal`) without requiring a process restart.

## Reordering (open question)

The root README's introductory description lists reordering alongside latency, packet loss, and partition as a fault netchaos aims to inject:

> "simulated `net.Conn` and `net.Listener` implementations with deterministic fault injection: latency, packet loss, partitions, and reordering"

However, the v1 scope checklist in the same README only lists:

- [ ] Latency injection (fixed and ranged)
- [ ] Packet loss (probabilistic, seeded/deterministic)
- [ ] Network partition (drop all traffic between two simulated peers)

Reordering does **not** appear as a checklist item. This is flagged here rather than silently resolved one way or the other — see [06 — Scope & Roadmap](06-scope-and-roadmap.md#reordering-in-or-out-of-v1) for the same flag in the scope document. Two ways this could resolve:

1. **Reordering is in v1**, and the checklist in the README is simply incomplete — it should be added as a fourth checklist item before implementation starts.
2. **Reordering is out of v1**, and the introductory description should be trimmed to match the checklist (latency, packet loss, partition only), with reordering revisited post-v1 alongside the other deferred items in [06 — Scope & Roadmap](06-scope-and-roadmap.md).

If reordering does end up in scope, the mechanic would conceptually be: instead of delivering queued writes to the other peer strictly in the order they were written, the fault-injection layer would hold a small window of pending writes and deliver them in a seeded-random permutation — meaningful primarily for protocols/clients that assume in-order delivery over what they believe is a reliable stream, which is a stronger assumption than TCP itself actually guarantees is preserved end-to-end at the application level in all real-world conditions (see e.g. TCP segments arriving out of order at the receiver's stack before reassembly).

Next: [06 — Scope & Roadmap](06-scope-and-roadmap.md) covers why v1 is bounded the way it is, and what's explicitly deferred.
