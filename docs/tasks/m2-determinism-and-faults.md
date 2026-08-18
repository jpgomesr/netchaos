# M2 — Determinism & the three faults

> See the [task index](README.md) for the milestone map and conventions.

**Covers four v1 checklist items** ([06 — Scope & Roadmap](../06-scope-and-roadmap.md)):

- Latency injection (fixed and ranged) → **M2-2**
- Packet loss (probabilistic, seeded/deterministic) → **M2-3**
- Network partition (drop all traffic between two simulated peers) → **M2-4**
- Seeded randomness for reproducible failure scenarios → **M2-1**

**Ordering that matters:** seeded randomness is listed fifth in the checklist but must be built **first** in this milestone. Latency and packet loss both draw from it; building them against an ad-hoc RNG and retrofitting determinism afterwards means rewriting both, plus their entire test suites.

Everything here plugs into the delivery hook left as a pass-through in [M1-1](m1-core-transport.md). No task in this milestone should need to change the transport's byte-stream semantics — if one does, that is a signal the M1 hook was placed wrong.

---

### M2-1 — Seeded RNG core with per-connection derived streams

**Status:** done
**Decision:** derivation is `sha256(domain-tag || masterSeed || ordinal || side || kind) → rand.NewChaCha8` seed, using only `stream.next() uint64` (the ChaCha8 byte stream, which is covered by Go's compatibility promise) as the primitive underneath `bernoulli` and `uniformDuration`. `math/rand/v2`'s convenience methods (`Float64`, `IntN`, `N`, ...) are deliberately not used directly, since their exact output for a given generator state is not part of the compatibility promise — only the raw `Uint64` stream is. `uniformDuration` always draws, even when `min == max`, so a fault kind's draw index tracks the unit index one-for-one regardless of whether a given draw happened to be fixed (this is the draw-discipline groundwork M2-5 builds on). The fault trace is always recorded, not opt-in, and stays unexported — no accessor is in the frozen v1 surface, so exporting one is deferred to a later milestone. A golden-vector test (`TestDeriveStreamGoldenVector`) pins concrete draw values for a fixed tuple, since none of the other tests would catch a derivation or generator change that preserved reproducibility within a run but altered it across versions.
**Roadmap item:** *Seeded randomness for reproducible failure scenarios* ([06](../06-scope-and-roadmap.md))
**Depends on:** M0-4 (the derivation model is decided there), M1-5, M1-7 (connection ordinals)
**Blocks:** M2-2, M2-3, M3-3

**Objective**
Implement the determinism substrate so the contract in [04 — API Design](../04-api-design.md#determinism-contract) — "for a fixed seed, a fixed sequence of `Network` method calls ... produces an identical sequence of injected faults across runs and across machines" — is actually true, including when the test under it uses several goroutines.

**Why this is the milestone's highest-risk task**
A single shared `rand.Rand` behind a mutex is race-free but *not* deterministic: with a client goroutine writing while a server goroutine writes back, the order in which they reach the RNG is chosen by the Go scheduler, so the same seed yields different fault sequences run to run. The mutex fixes the data race and leaves the determinism bug. The resolution decided in M0-4 — expected to be per-connection (and possibly per-direction) streams derived from the master seed — makes each connection's draw sequence depend only on its own I/O.

**Scope**
- A derivation function `(masterSeed, connectionOrdinal, direction) → *rand.Rand` (or `*rand.ChaCha8`/`rand/v2` equivalent), implementing M0-4's decision.
- Attach a stream to each connection direction at creation, using the ordinal assigned deterministically in M1-7.
- Draw helpers the fault tasks call: a uniform `time.Duration` in `[min, max]` for M2-2, and a Bernoulli trial for M2-3. Faults must never touch a global RNG (`math/rand`'s top-level functions) — add a lint or test guard.
- A **fault trace**: an ordered, per-connection record of the fault decisions made (unit index → delayed by *d* / dropped / delivered). This is the artifact M3-3 compares across runs, and the thing that turns "the test failed differently" into a diffable answer. Decide whether it is always recorded or opt-in.
- Cross-machine stability: the chosen RNG and derivation must produce identical output on every platform and architecture, since the contract says "across machines". Do not use map iteration order, pointer values, or anything `int`-width-dependent in the derivation.
- Out of scope: any fault behaviour.

**Files**
- `rand.go` (new) — derivation, stream type, draw helpers
- `trace.go` (new) — fault trace recording
- `conn.go` — attach a stream per direction
- `rand_test.go`, `trace_test.go` (new)

**Acceptance criteria**
- [x] The same master seed and the same connection ordinal always produce the same draw sequence.
- [x] Different ordinals from the same master seed produce different, uncorrelated sequences.
- [x] A connection's draw sequence is unaffected by concurrent I/O on other connections — asserted by a test running N connections concurrently and comparing each connection's trace against its single-connection baseline.
- [x] No fault path reads a global/unseeded RNG (guarded by a test or lint rule).
- [x] Derived values are identical across GOOS/GOARCH — no platform-dependent constructs in the derivation path.
- [x] The uniform-duration helper covers `[min, max]` inclusive and handles `min == max` without dividing by zero.
- [x] The fault trace records decisions in per-connection order and is comparable between runs.
- [x] `-race` clean (verified on CI; this repo's Windows dev box has no C toolchain, so `-race` cannot run locally — a pre-existing limitation noted in `AGENTS.md`).

**Tests**
- `TestSeedReproducible`, `TestOrdinalsIndependent`, `TestDirectionAndKindIndependent`
- `TestConcurrencyDoesNotPerturbStreams` — the headline test: baseline each connection alone, then run all concurrently, assert traces match
- `TestNoGlobalRandUsage`
- `TestUniformDurationBounds`, `TestUniformDurationFixed`, `TestUniformDurationFixedConsumesADraw`, `TestBernoulliBoundaries`
- `TestDeriveStreamGoldenVector` — pins concrete draw values for a fixed tuple
- `TestConnPairAttachesPerDirectionStreams`, `TestDialAttachesNetworkSeed` — attachment at connection creation, wired through `Network.seed`
- `TestTraceRecordsInOrder`, `TestTraceComparable`, `TestTraceSnapshotIsACopy`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M2-2 — Latency injection (fixed and ranged)

**Status:** todo
**Roadmap item:** *Latency injection (fixed and ranged)* ([06](../06-scope-and-roadmap.md))
**Depends on:** M1-1, M1-2, M1-3, M2-1
**Blocks:** M2-5, M2-6, M3-2

**Objective**
Delay delivery of a write from one simulated peer to the other by a duration drawn from the seeded RNG, so tests can exercise timeout, deadline and backoff logic without real wall-clock cost.

**Scope**
- `WithLatency(min, max time.Duration) Option`, per [04 — API Design](../04-api-design.md#functional-options).
- Equal `min` and `max` = a fixed delay on every unit; `min < max` = a duration drawn uniformly from `[min, max]` per unit, from the connection's M2-1 stream ([05 — Fault Injection](../05-fault-injection.md#latency)).
- The delayed unit follows M0-3 (per write vs. per simulated packet).
- Delay is implemented with standard `time` primitives — a timer on the delivery path — so `testing/synctest` virtualizes it. **Do not introduce a clock abstraction:** [03 — Architecture](../03-architecture.md#composing-with-testingsynctest) rules this out explicitly, and any custom clock would defeat the whole synctest integration.
- Latency delays delivery; it must not reorder. Pending units on one direction are released in write order even when a later unit draws a shorter delay. (If M0-1 put reordering in v1, that is a separate, explicitly-configured fault — it must not fall out of latency as an accident.)
- Per M0-2, apply globally or per peer pair as decided.
- Out of scope: interaction with loss (M2-5), the synctest test suite (M3-2).

**Files**
- `latency.go` (new) — option, config field, delay draw, delivery timer
- `conn.go` / `pipe.go` — hook into the M1-1 delivery point
- `latency_test.go` (new)

**Acceptance criteria**
- [ ] `WithLatency(d, d)` delays every unit by exactly `d` of virtual time.
- [ ] `WithLatency(min, max)` produces durations within `[min, max]` inclusive.
- [ ] The same seed and call sequence produce an identical delay sequence across runs.
- [ ] Units are delivered in write order regardless of drawn delays.
- [ ] A read blocked waiting on a delayed write is **durably blocking** inside a `synctest` bubble — the bubble advances virtual time instead of deadlock-panicking.
- [ ] A read deadline shorter than the latency returns `os.ErrDeadlineExceeded` (M1-3), not the delayed data; the data still arrives on a subsequent read with a longer deadline, or is discarded — whichever, it is documented.
- [ ] Closing a conn with delayed writes in flight behaves per the M1-2 close decision, and that behaviour is tested here where it becomes observable.
- [ ] Zero latency (the default, no option) adds no timer and no measurable overhead.
- [ ] `-race` clean.

**Tests**
- `TestLatencyFixed` / `TestLatencyRanged` — inside `synctest.Test`, asserting `time.Since(start)` equals the expected virtual duration
- `TestLatencyDeterministic` — same seed twice, compare recorded delay traces
- `TestLatencyPreservesOrder` — N writes, assert read order matches write order
- `TestLatencyVsReadDeadline` — deadline shorter than the delay
- `TestLatencyCloseInFlight`
- `TestNoLatencyByDefault`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M2-3 — Packet loss (probabilistic, seeded)

**Status:** todo
**Roadmap item:** *Packet loss (probabilistic, seeded/deterministic)* ([06](../06-scope-and-roadmap.md))
**Depends on:** M1-1, M1-2, M2-1, M0-3
**Blocks:** M2-5, M2-6

**Objective**
Probabilistically drop a unit instead of delivering it, using the seeded RNG so the exact sequence of delivered-vs-dropped is identical for a given seed — which is what lets a flaky-looking failure be pinned down and replayed ([05 — Fault Injection](../05-fault-injection.md#packet-loss)).

**Scope**
- `WithPacketLoss(rate float64) Option`, rate in `[0.0, 1.0]`, per [04](../04-api-design.md#functional-options).
- A Bernoulli trial per unit from the connection's M2-1 stream; the unit follows M0-3.
- **Define what a drop means to a byte-stream reader.** This is the substantive design content of the task, not a detail. `net.Conn` is a stream: if a write is silently dropped, the reader sees the surrounding bytes concatenated with no indication that anything is missing — the stream is silently corrupted, which real TCP never does. Real TCP retransmits, so a lossy link manifests as *latency and eventual timeout*, not missing bytes. Three coherent models to choose between, and the choice must be recorded in [05](../05-fault-injection.md#packet-loss):
  1. **Silent gap** — the dropped bytes simply never arrive. Simplest, but hands the code under test a corrupted stream, which tests a scenario TCP does not produce.
  2. **Stall** — the dropped unit is never delivered and never retransmitted, so the reader blocks until its deadline fires. This is what [05](../05-fault-injection.md#packet-loss) implies when it says retry logic detects loss "via timeout or connection-level signal", and it matches how a lossy link actually looks to an application.
  3. **Connection error** — the drop surfaces as a connection-level failure to the reader.
  Model 2 is the one consistent with the stated purpose (testing retry and timeout logic); models 1 and 3 are viable but change what the fault teaches.
- Rate `0.0` drops nothing and adds no overhead; rate `1.0` drops everything (equivalent in effect to a one-directional partition — note the relationship in godoc).
- Per M0-2, apply globally or per peer pair as decided.
- Out of scope: rate validation (M2-6), composition with latency (M2-5).

**Files**
- `loss.go` (new)
- `conn.go` / `pipe.go` — hook into the delivery point
- `loss_test.go` (new)

**Acceptance criteria**
- [ ] The drop model (1/2/3 above) is chosen, implemented, and documented in [05](../05-fault-injection.md#packet-loss) and in godoc.
- [ ] The same seed produces a byte-identical delivered/dropped sequence across runs.
- [ ] Over a large sample, the observed drop rate converges on the configured rate within a stated tolerance.
- [ ] Rate `0.0` delivers everything; rate `1.0` delivers nothing.
- [ ] Under the chosen model, a client with a retry loop and a read deadline completes successfully at a moderate loss rate — the scenario [05](../05-fault-injection.md#packet-loss) names as the point of the feature.
- [ ] The drop decisions appear in the M2-1 fault trace.
- [ ] Loss on one connection does not perturb another connection's draw sequence.
- [ ] `-race` clean.

**Tests**
- `TestLossDeterministic` — same seed twice, identical drop sequence
- `TestLossRateConverges` — large N, assert within tolerance
- `TestLossZeroRate`, `TestLossFullRate`
- `TestLossDropModel` — asserts the chosen model's observable behaviour (e.g. for model 2: the reader blocks and hits its deadline)
- `TestRetrySucceedsUnderLoss` — the end-to-end scenario
- `TestLossIsolatedPerConnection`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M2-4 — Network partition (static and dynamic)

**Status:** todo
**Roadmap item:** *Network partition (drop all traffic between two simulated peers)* ([06](../06-scope-and-roadmap.md))
**Depends on:** M1-4, M1-7, M1-8 — deliberately *not* the seeded-RNG task, since a partition is binary and must consume no random draws
**Blocks:** M2-5, M2-6

**Objective**
Drop **all** traffic between two named peers — binary, not probabilistic — with both a construction-time form and mid-test control, per [05 — Fault Injection](../05-fault-injection.md#partition) and [04 — API Design](../04-api-design.md#dynamic-partition-control).

**Scope**
- `WithPartition(peerA, peerB string) Option` — a partition present from `Network` construction for the whole test.
- `Network.Partition(peerA, peerB string)` and `Network.Heal(peerA, peerB string)` for mid-test control.
- Partition state keyed by unordered peer pair — `Partition("a","b")` and `Partition("b","a")` are the same partition. Peer resolution uses the single M1-4 address→peer function.
- **Decide what a partition does to connection establishment**, not just to data. If `client` and `server-a` are partitioned, does `Dial` fail immediately, block until its context/deadline expires, or succeed and then deliver nothing? The realistic answer depends on the scenario being modelled; whichever is chosen must be documented, because circuit-breaker tests key off exactly this.
- **Decide what happens to already-established connections** when a partition appears mid-test: data stops flowing (writes accepted and discarded, or writes blocked?), and reads block until their deadline. Note that "writes silently accepted then discarded" is what a real partition looks like to a sender — the kernel accepts into the socket buffer and the application learns nothing until a timeout.
- **Decide what `Heal` does to traffic sent while partitioned** — discarded (the realistic choice, matching packet loss) or buffered and delivered on heal (a different, also-useful simulation). [05](../05-fault-injection.md#partition) says a partition "drops" traffic, which points at discard.
- `Heal` on a pair with no partition, and `Partition` on an unknown peer: behaviour per the M0-5 error decision.
- Partition applies to both directions. If a one-directional partition is wanted, that is post-v1 — do not add it here.
- Concurrency: `Partition`/`Heal` are called from a test goroutine while I/O runs in others; the state read on the delivery path must be safe and must not use a blocking primitive that breaks bubble idleness ([M1](m1-core-transport.md) constraint 2).

**Files**
- `partition.go` (new) — state, option, `Partition`, `Heal`
- `netchaos.go` — topology state
- `conn.go` / `pipe.go` — consult partition state on the delivery path
- `partition_test.go` (new)

**Acceptance criteria**
- [ ] `WithPartition` prevents traffic between the pair from `Network` construction onward.
- [ ] `Partition` mid-test stops traffic on an already-established connection.
- [ ] `Heal` restores traffic on that same connection without requiring a re-dial — the recovery case [05](../05-fault-injection.md#partition) names.
- [ ] Pair keys are order-independent.
- [ ] Traffic between *unpartitioned* peers is unaffected while another pair is partitioned.
- [ ] The dial-under-partition behaviour is implemented as decided and documented.
- [ ] The in-flight-data-on-heal behaviour is implemented as decided and documented.
- [ ] Partition consumes **no** RNG draws — it is deterministic by nature, and drawing from the stream would perturb latency/loss sequences on the same connection.
- [ ] `Heal` on an unpartitioned pair behaves per M0-5, without panicking.
- [ ] The full partition → heal → recover cycle runs inside `synctest.Test` with virtual time.
- [ ] `-race` clean with `Partition`/`Heal` called concurrently with I/O.

**Tests**
- `TestStaticPartition`, `TestDynamicPartitionThenHeal`
- `TestPartitionPairOrderIndependent`, `TestPartitionIsolatedToPair`
- `TestDialUnderPartition`
- `TestPartitionConsumesNoRandomness` — compare traces with and without a partition on an unrelated pair
- `TestCircuitBreakerScenario` — partition, observe failure, heal, observe recovery, inside `synctest.Test`
- `TestPartitionRace`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M2-5 — Fault composition rules

**Status:** todo
**Roadmap item:** the fault-injection layer as a whole ([03 — Architecture](../03-architecture.md#fault-injection-layer))
**Depends on:** M2-2, M2-3, M2-4
**Blocks:** M3-1, M3-3

**Objective**
Define and implement what happens when latency, packet loss and partition are configured together. The README's own example configures loss and latency simultaneously, so this is the default case, not an exotic one — and the order faults are applied in changes both the observable behaviour and the RNG draw sequence, so it must be fixed rather than emergent.

**Scope**
- Fix the application order and document it. The natural ordering, matching how a real path behaves: **partition first** (a partitioned link drops everything, so no further evaluation), **then loss** (a dropped unit is never delivered, so its latency is irrelevant), **then latency** (applies to units that survive).
- Fix the RNG-draw discipline, which is the subtle part: if a unit is dropped by loss, is a latency duration still drawn for it? Both answers are defensible, but they produce **different fault sequences for the same seed**, so the rule must be explicit and permanent. Drawing unconditionally makes the draw sequence independent of loss outcomes and therefore easier to reason about; drawing only when needed is cheaper. Pick one, write it into [04](../04-api-design.md#determinism-contract), and treat a later change as a breaking change to the determinism contract.
- Same question for partition: a partitioned link must consume **no** draws (per M2-4), so that partitioning an unrelated pair cannot shift another connection's sequence.
- Document the interaction between loss rate `1.0` and partition — behaviourally similar, different in intent and in draw consumption.
- Out of scope: new fault types.

**Files**
- `faults.go` (new) — the composed policy evaluation, called from the delivery hook
- `conn.go` / `pipe.go` — call the single composed evaluator instead of three separate hooks
- `faults_test.go` (new)

**Acceptance criteria**
- [ ] There is exactly **one** place where fault policy is evaluated per unit — not three independent hooks that happen to run in an order.
- [ ] The application order (partition → loss → latency) is implemented and documented in [05](../05-fault-injection.md) or [03](../03-architecture.md#fault-injection-layer).
- [ ] The draw discipline for dropped units is decided, implemented, and written into the determinism contract in [04](../04-api-design.md#determinism-contract).
- [ ] With all three configured, behaviour matches the documented order.
- [ ] Partitioning an unrelated pair does not change any other connection's fault trace.
- [ ] The same seed with all three faults configured reproduces an identical trace across runs.
- [ ] `-race` clean.

**Tests**
- `TestFaultOrderPartitionWinsOverLoss`, `TestFaultOrderLossWinsOverLatency`
- `TestDrawDisciplineStable` — assert the documented rule directly against the trace
- `TestAllFaultsDeterministic` — all three configured, same seed twice
- `TestUnrelatedPartitionDoesNotPerturb`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M2-6 — Option validation

**Status:** todo
**Roadmap item:** supports all four M2 checklist items
**Depends on:** M2-2, M2-3, M2-4, M0-5
**Blocks:** M4-1

**Objective**
Reject invalid configuration loudly. A silently clamped or ignored option produces a test that passes for the wrong reason — the worst possible failure mode for a testing library, since it manufactures false confidence in the code under test.

**Scope**
- Validate: packet-loss rate outside `[0.0, 1.0]` (including `NaN`); latency `min > max`; negative durations; empty or unknown peer names in `WithPartition`; an unsupported `network` string.
- Implement the reporting mechanism decided in M0-5. `NewNetwork` currently returns only `*Network` ([04](../04-api-design.md#network)), so an invalid option has nowhere to go — the realistic choices are to panic (defensible in a *test-only* library, where a misconfigured test should fail immediately and loudly), or to change the signature to return an error, or to defer the error to the first `Dial`/`Listen`. Panicking is the option most consistent with test-only usage and with `NewNetwork`'s current signature; whichever is chosen must be applied consistently across every option.
- Whatever the mechanism, the message must name the offending option and the offending value.
- Out of scope: runtime I/O errors (M1-8).

**Files**
- `options.go` — validation
- `errors.go` — any new sentinels
- `options_test.go` — validation table

**Acceptance criteria**
- [ ] Every invalid input listed above is rejected, table-driven.
- [ ] `NaN` and infinite loss rates are rejected, not silently compared.
- [ ] The failure mechanism is uniform across options and matches M0-5.
- [ ] Every message names the option and the value.
- [ ] Valid boundary values are accepted: rate exactly `0.0` and `1.0`, `min == max` latency, zero latency.
- [ ] Godoc on each option states its valid range and what happens outside it.

**Tests**
- `TestOptionValidation` — table over invalid inputs, asserting the failure mechanism
- `TestOptionBoundaryValuesAccepted`
- `TestValidationMessagesNameTheOption`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`
