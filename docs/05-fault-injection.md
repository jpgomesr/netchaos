# 05 — Fault Injection

> **Status: implemented.** Latency, packet loss, bandwidth throttling, and partition (below) are implemented, tested, and compose correctly together. See [04 — API Design](04-api-design.md) for the configuration surface.

netchaos's v1 scope (per the root README's checklist) covers three fault categories: latency, packet loss, and partition; [M7-5](tasks/m7-v0.2.0-implementation.md#m7-5--fault-kind-bandwidth-throttling) added bandwidth throttling as the first `v0.2.0` addition. Latency, packet loss, and bandwidth's dependent state each draw from or track a per-connection-direction stream or clock (see [04 — API Design § Determinism contract](04-api-design.md#determinism-contract) and [03 — Architecture](03-architecture.md#fault-injection-layer)), which is what makes an entire test run reproducible from a single seed value without one connection's fault sequence depending on how the scheduler interleaved it with another.

**Composition (M2-5, extended by M7-5):** when more than one fault is configured on the same connection direction, a single evaluator decides the outcome for each unit, in a fixed order — **partition, then packet loss, then bandwidth, then latency** — rather than hooks that happen to run in whatever order they were installed. Partition short-circuits before any draw happens, so it never perturbs the loss/latency streams. Packet loss is the next gate: a dropped unit never reaches the link, so bandwidth's stage costs it no simulated transmission time. A unit that isn't partitioned draws from every *configured, drawing* fault's stream regardless of what an earlier fault in the order decided — a unit packet loss drops still draws (and discards) a latency duration. Bandwidth is not part of that: it computes a deterministic delay from a unit's size and the configured rate, drawing nothing, so it cannot perturb the loss/latency sequence regardless of whether it is configured. This keeps each drawing fault's draw index locked to the unit index, which is what makes two runs' traces diffable position-for-position. See the [determinism contract](04-api-design.md#determinism-contract) for the exact, permanent rule.

## Latency

**What it does:** Delays delivery of a write from one simulated peer to the other by some duration, instead of delivering it immediately.

**Fixed vs. ranged:** `WithLatency(min, max time.Duration)` supports both — passing equal `min` and `max` applies a fixed delay to every write; passing a range draws a duration uniformly from `[min, max]` per write, using the connection direction's own seeded stream. A duration is produced even when `min == max`, so exactly one is produced per write, fixed delays included, and the sequence of durations always tracks the number of writes. (One duration is not necessarily one raw 64-bit draw: the unbiased bounded draw underneath resamples when a value lands in its biased zone. That is invisible to the sequence of durations, and to determinism, since each stream is scoped to a single connection direction and fault kind.)

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

## Bandwidth throttling

**What it does:** Delays delivery in proportion to a write's size, modelling a link that can only carry so many bytes per second, rather than a flat per-write delay.

**Configuration:** `WithBandwidth(bytesPerSecond int)` sets a rate; `bytesPerSecond` must be positive. Applied **per connection direction**, not shared across a connection's two directions or across connections — a full-duplex conn is throttled to the configured rate each way.

**Model:** a serialization clock, not a flat per-unit delay. Each connection direction tracks the instant its link finishes transmitting everything admitted so far; a unit's transmission starts no earlier than that instant, so back-to-back writes on a slow link queue behind each other — the shape of delay that produces *sustained* back-pressure once the throttle is slower than the reader, rather than each write drawing an independent delay. Latency, if also configured, is added on top as propagation delay: the two compose additively, so `WithLatency` is never silently inert because a throttle is configured.

**Deterministic, not drawn:** unlike latency and packet loss, the throttle's delay is a function of a unit's size and the configured rate — there is nothing random about it. It has no `faultKind` byte and no derived stream, so enabling it can never perturb the loss or latency draw sequence on the same direction. See the [determinism contract](04-api-design.md#determinism-contract) for the full statement of why a non-drawing fault is safe to add without shifting anything else.

**Interaction with the pipe bound:** a throttle slower than the reader is what makes the pipe's buffer bound observable as sustained back-pressure — see [M6-17](tasks/m6-review-findings.md#m6-17--decide-whether-the-pipe-bound-and-listener-backlog-become-configurable), accepted alongside this fault for exactly that reason.

**What it's for:** Testing behaviour under a slow link — does a client's timeout budget account for a large payload taking real time to transfer, does streaming logic keep up with (or correctly apply back-pressure against) a constrained connection.

## Partition

**What it does:** Drops **all** traffic between two named simulated peers, unlike packet loss which drops probabilistically. A partition is binary and (per the [API](04-api-design.md#dynamic-partition-control)) persists until explicitly healed. Unlike latency and packet loss, partition consumes no random draws — see [04 — API Design § Determinism contract](04-api-design.md#determinism-contract) — so partitioning one pair can never perturb another connection's fault sequence.

**Static vs. dynamic:** `WithPartition(peerA, peerB)` establishes a partition at `Network` construction time, present for the whole test. `Network.Partition` / `Network.Heal` allow inducing and resolving a partition mid-test — useful for scenarios like "the connection was healthy, then the network split, then it recovered." Pairs are unordered: naming either peer first identifies the same partition.

**Effect on dialing:** a dial whose caller named itself — via [`WithPeerName`](04-api-design.md#frozen-v1-surface) or [`DialerFor`](04-api-design.md#frozen-v1-surface) — blocks rather than failing fast while its target peer is partitioned from it, returning once `Heal` clears the partition or, if the dial has a context, once that context is done. This is the realistic choice: a partition drops the SYN, so a real dial hangs the same way, rather than surfacing a connection-refused-style error the way dialing an address nobody is listening on does. A dialer that never names itself gets a synthesized identity no `Partition` call could ever target in advance, so it never blocks here.

**Effect on established connections:** once a partition is in effect, writes into it are accepted and silently discarded — the same silent-gap model as packet loss (see above) — and reads block until their deadline. `Heal` restores traffic on the existing connection with no re-dial required. Data written while partitioned is discarded, not queued for delivery once healed; this matches what a real partition looks like to a sender, whose kernel accepts into the socket buffer and discovers nothing until a timeout.

**What it's for:** Testing circuit breakers and failover logic — does your code detect a fully-unreachable peer and open its circuit breaker, does it correctly fail over to another peer, does it recover once the partition heals (`Heal`) without requiring a process restart.

## Accepted for v0.2.0, not yet implemented

[M6-11](tasks/m6-review-findings.md#m6-11--decide-which-new-fault-kinds-if-any-enter-v02) accepted four further fault kinds into `v0.2.0` scope. Bandwidth throttling **shipped** in [M7-5](tasks/m7-v0.2.0-implementation.md#m7-5--fault-kind-bandwidth-throttling) — see the Bandwidth throttling section above. The remaining three are not implemented; each gets its own task. Recorded here so this document stays the place a reader looks for what netchaos can fault, and [06 — Scope & Roadmap § Accepted for v0.2.0](06-scope-and-roadmap.md#accepted-for-v02) carries the reasoning.

- **Mid-stream connection reset** — an established conn failing abruptly. This is a genuine gap rather than an enhancement: `Partition` is the nearest thing today and is deliberately a silent black hole (writes accepted and discarded, reads blocking to their deadline), not an `ECONNRESET`.
- **Packet duplication** and **data corruption / bit flips** — accepted on the [M0-3](tasks/m0-decisions-and-foundations.md#m0-3--decide-fault-granularity-per-write-vs-per-simulated-packet) precedent. Real TCP hides both from the application, as it also hides packet *loss* by retransmitting; netchaos chose the silent-gap model for loss anyway, precisely so a test can exercise what the application sees when the transport does not behave. The same reasoning extends here, and was applied explicitly rather than by analogy.

A new kind that draws gets its own `faultKind` byte and therefore its own derived stream, so **adding one cannot perturb any existing test's latency or loss sequence** — new kinds are additive rather than a golden-trace break. Bandwidth is the counterexample worth naming: it draws nothing at all, so it needed neither a byte nor a stream, and the same non-perturbation guarantee holds for the stronger reason that there is no draw to place anywhere in the sequence. Each remaining kind that does draw must respect the M2-5 draw discipline above: draw unconditionally on every unit past the partition gate, like the existing two.

## Reordering (deferred, not in v1)

Reordering is **not** in v1 scope. It was flagged as an open question — the README's introductory prose used to list it alongside latency, packet loss, and partition while the v1 checklist never included it — and has been resolved out, decided as part of [M0-1](tasks/m0-decisions-and-foundations.md#m0-1--resolve-whether-reordering-is-in-v1). See [06 — Scope & Roadmap](06-scope-and-roadmap.md#explicitly-out-of-scope-for-v1) for where it now lives on the deferred list, including the mechanic it would use if picked up post-v1.

Next: [06 — Scope & Roadmap](06-scope-and-roadmap.md) covers why v1 is bounded the way it is, and what's explicitly deferred.
