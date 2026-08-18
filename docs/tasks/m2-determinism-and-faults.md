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

**Status:** done
**Decision:** implemented as a single live `time.AfterFunc`, armed for the head of a per-pipe `pending` FIFO whose entries are release-ordered by construction (`releaseAt = max(previous releaseAt, now+drawn)`) — not one timer per unit, which would let a later write's shorter draw jump the queue. `uniformDuration` (M2-1) always draws, even for `WithLatency(d, d)`, so the draw index tracks the unit index regardless of whether a given delay happened to be fixed. Reads deadlined shorter than the latency get `os.ErrDeadlineExceeded`; the data is not discarded and arrives on a later read with a longer deadline. Closing a conn discards any still-pending (undelivered) units rather than delivering them — they model bytes in flight on the wire, not bytes already in the peer's receive buffer, so this doesn't touch M1's "already-buffered data still drains" guarantee. Confirmed the M0-2 fault-scoping decision (global) rather than re-deciding it.
**Roadmap item:** *Latency injection (fixed and ranged)* ([06](../06-scope-and-roadmap.md))
**Depends on:** M1-1, M1-2, M1-3, M2-1
**Blocks:** M2-5, M2-6, M3-2

**Objective**
Delay delivery of a write from one simulated peer to the other by a duration drawn from the seeded RNG, so tests can exercise timeout, deadline and backoff logic without real wall-clock cost.

**Scope**
- `WithLatency(min, max time.Duration) Option`, per [04 — API Design](../04-api-design.md#functional-options).
- Equal `min` and `max` = a fixed delay on every unit; `min < max` = a duration drawn uniformly from `[min, max]` per unit, from the connection's M2-1 stream ([05 — Fault Injection](../05-fault-injection.md#latency)).
- The delayed unit is the whole `Write` call (M0-3, already decided — not a simulated packet).
- Delay is implemented with standard `time` primitives — a timer on the delivery path — so `testing/synctest` virtualizes it. **Do not introduce a clock abstraction:** [03 — Architecture](../03-architecture.md#composing-with-testingsynctest) rules this out explicitly, and any custom clock would defeat the whole synctest integration.
- Latency delays delivery; it must not reorder. Pending units on one direction are released in write order even when a later unit draws a shorter delay. Reordering is out of v1 (M0-1, already decided).
- Applies globally (M0-2, already decided).
- Out of scope: interaction with loss (M2-5), the synctest test suite (M3-2).

**Files**
- `latency.go` (new) — option, config field, delay draw, delivery timer
- `conn.go` / `pipe.go` — hook into the M1-1 delivery point
- `latency_test.go` (new)

**Acceptance criteria**
- [x] `WithLatency(d, d)` delays every unit by exactly `d` of virtual time.
- [x] `WithLatency(min, max)` produces durations within `[min, max]` inclusive.
- [x] The same seed and call sequence produce an identical delay sequence across runs.
- [x] Units are delivered in write order regardless of drawn delays.
- [x] A read blocked waiting on a delayed write is **durably blocking** inside a `synctest` bubble — the bubble advances virtual time instead of deadlock-panicking.
- [x] A read deadline shorter than the latency returns `os.ErrDeadlineExceeded` (M1-3), not the delayed data; the data still arrives on a subsequent read with a longer deadline — documented above and in `latency.go`'s godoc.
- [x] Closing a conn with delayed writes in flight discards them rather than delivering them; tested (`TestLatencyCloseInFlight`).
- [x] Zero latency (the default, no option) adds no timer and no measurable overhead — `deliver` stays `passThroughDeliver` (`TestNoLatencyByDefault`).
- [x] `-race` clean (verified on CI; not runnable on this dev box, see M2-1).

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

**Status:** done
**Decision:** the drop model was **already settled by [M0-3](m0-decisions-and-foundations.md#m0-3--decide-fault-granularity-per-write-vs-per-simulated-packet) as model 1, silent gap** — this task's original scope text below presented it as a three-way open choice recommending model 2 (stall); that text predates M0's closure and was stale (per `docs/tasks/README.md`, M0's task file is authoritative on conflict). Implemented as: a dropped unit's `bufBytes` accounting is undone (it was admitted, so it must be un-accounted, or repeated drops permanently inflate `bufBytes` and wedge the writer — guarded by `TestLossDoesNotWedgeWriterOnRepeatedDrops`), the unit never reaches `readable`, and `conn.Write` already returns `n = len(p), nil` on admission regardless of what `deliver` does with the data afterward, so no `conn.go` change was needed for the writer side. `bernoulli` always draws, even at rate `0.0` or `1.0`, matching `uniformDuration`'s discipline. **Known gap, deferred to M2-5:** `installLoss` and `installLatency` both assign `p.deliver` directly, so configuring `WithLatency` and `WithPacketLoss` on the same `Network` today means whichever is installed last in `DialContext` wins outright — the other's `deliverFunc` is silently replaced, not composed. This is explicitly M2-5's job.
**Roadmap item:** *Packet loss (probabilistic, seeded/deterministic)* ([06](../06-scope-and-roadmap.md))
**Depends on:** M1-1, M1-2, M2-1, M0-3
**Blocks:** M2-5, M2-6

**Objective**
Probabilistically drop a unit instead of delivering it, using the seeded RNG so the exact sequence of delivered-vs-dropped is identical for a given seed — which is what lets a flaky-looking failure be pinned down and replayed ([05 — Fault Injection](../05-fault-injection.md#packet-loss)).

**Scope**
- `WithPacketLoss(rate float64) Option`, rate in `[0.0, 1.0]`, per [04](../04-api-design.md#functional-options).
- A Bernoulli trial per unit from the connection's M2-1 stream; the unit is the whole `Write` call (M0-3, already decided).
- The drop model is **silent gap** (M0-3, already decided): the dropped bytes simply never arrive, the reader sees the surrounding bytes concatenated with no indication anything is missing, and the write still reports full success. This is not re-litigated here; see [04](../04-api-design.md#fault-unit-and-drop-semantics) and [05](../05-fault-injection.md#packet-loss).
- Rate `0.0` drops nothing; rate `1.0` drops everything (equivalent in effect to a one-directional partition, but distinct in intent and in draw consumption — partition draws nothing, loss at rate `1.0` still draws — noted in godoc).
- Applies globally (M0-2, already decided).
- Out of scope: rate validation (M2-6), composition with latency (M2-5, and see the known gap noted above).

**Files**
- `loss.go` (new)
- `conn.go` / `pipe.go` — hook into the delivery point
- `loss_test.go` (new)

**Acceptance criteria**
- [x] The drop model (silent gap, per M0-3) is implemented and documented in [05](../05-fault-injection.md#packet-loss) and in godoc.
- [x] The same seed produces a byte-identical delivered/dropped sequence across runs.
- [x] Over a large sample, the observed drop rate converges on the configured rate within a stated tolerance (5 percentage points at N=5000).
- [x] Rate `0.0` delivers everything; rate `1.0` delivers nothing.
- [x] A client with a retry loop and a read deadline completes successfully at a moderate loss rate — the scenario [05](../05-fault-injection.md#packet-loss) names as the point of the feature.
- [x] The drop decisions appear in the M2-1 fault trace.
- [x] Loss on one connection does not perturb another connection's draw sequence.
- [x] `-race` clean (verified on CI; not runnable on this dev box, see M2-1).

**Tests**
- `TestLossDeterministic` — same seed twice, identical drop sequence
- `TestLossRateConverges` — large N, assert within tolerance
- `TestLossZeroRate`, `TestLossFullRate`
- `TestLossDropModelConcatenatesSurvivors` — asserts the silent-gap model directly: survivors arrive concatenated, no error, no gap marker
- `TestRetrySucceedsUnderLoss` — the end-to-end scenario
- `TestLossIsolatedPerConnection`
- `TestLossDoesNotWedgeWriterOnRepeatedDrops` — the `bufBytes` accounting regression
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M2-4 — Network partition (static and dynamic)

**Status:** done
**Decision:** **dial blocks** while the caller's declared peer name is partitioned from the target, returning `ctx.Err()` only once the context is done — a partition drops the SYN, so a real dial hangs the same way (documented in [04](../04-api-design.md#dynamic-partition-control) and [05](../05-fault-injection.md#partition)). This required an addition to the frozen v1 surface: **`WithPeerName(ctx, name) context.Context`**, an exported setter for the context key `DialContext` already read but nothing could write — without it, no dialer could ever be partition-targetable, which would make the README's own example unimplementable. An unnamed dialer's synthesized `ephemeral:N` identity is never nameable by a `Partition` call made before the dial completes, so it never blocks on this check. The ordinal is now assigned *after* the partition wait clears, so a dial that never establishes doesn't burn one — the [determinism contract](../04-api-design.md#determinism-contract) is updated to say so. Already-established connections: writes into a partitioned pair are accepted and silently discarded (reusing the M2-3 silent-gap drop path — `bufBytes` un-accounted, traced, no error to the writer); reads block until their deadline; `Heal` restores traffic with no re-dial; data written while partitioned is discarded on heal, not buffered, matching M0-3's drop-semantics precedent. `Heal`/`Partition` on an unpartitioned/unknown pair are silent no-ops per M0-5. Implemented as a **wrapping** decorator (`installPartition` wraps whatever `p.deliver` already was, rather than replacing it) specifically so partition always composes correctly with latency/loss regardless of the M2-3-noted gap between those two — partition sits outside both, matching the `partition → loss → latency` order M2-5 will make explicit for the whole stack.
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
- [x] `WithPartition` prevents traffic between the pair from `Network` construction onward.
- [x] `Partition` mid-test stops traffic on an already-established connection.
- [x] `Heal` restores traffic on that same connection without requiring a re-dial — the recovery case [05](../05-fault-injection.md#partition) names.
- [x] Pair keys are order-independent.
- [x] Traffic between *unpartitioned* peers is unaffected while another pair is partitioned.
- [x] The dial-under-partition behaviour is implemented as decided and documented.
- [x] The in-flight-data-on-heal behaviour is implemented as decided and documented.
- [x] Partition consumes **no** RNG draws — it is deterministic by nature, and drawing from the stream would perturb latency/loss sequences on the same connection.
- [x] `Heal` on an unpartitioned pair behaves per M0-5, without panicking.
- [x] The full partition → heal → recover cycle runs inside `synctest.Test` with virtual time.
- [x] `-race` clean with `Partition`/`Heal` called concurrently with I/O (verified on CI; not runnable on this dev box, see M2-1).

**Tests**
- `TestStaticPartition`, `TestDynamicPartitionThenHeal`
- `TestPartitionPairOrderIndependent`, `TestPartitionIsolatedToPair`
- `TestDialUnderPartition`, `TestDialUnblocksOnHeal`
- `TestHealUnpartitionedPairIsNoop`, `TestPartitionUnknownPeerIsNoop`
- `TestPartitionConsumesNoRandomness` — compare traces with and without a partition on an unrelated pair
- `TestCircuitBreakerScenario` — partition, observe failure, heal, observe recovery, inside `synctest.Test`
- `TestPartitionRace`
- `TestPartitionedWriteDiscardedSilently`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M2-5 — Fault composition rules

**Status:** done
**Decision:** the RNG-draw discipline is **unconditional**: a unit that clears the partition gate draws from every *configured* fault's stream regardless of what an earlier fault in the order already decided — a unit packet loss drops still draws (and discards) a latency duration. This is now the permanent rule in [04](../04-api-design.md#determinism-contract). Partition draws nothing, as M2-4 already required. Implemented as a single `faultPolicy` struct plus one `installFaultPolicy` function (`faults.go`) that replaces the three separate `install{Latency,Loss,Partition}` functions M2-2/M2-3/M2-4 had each been assigning to `pipe.deliver` directly (the last one installed had been silently winning — this is the bug M2-3's task entry flagged and deferred here). `faultPolicy.network` is nilable so pipe-level tests can exercise loss/latency composition without constructing a full `Network`.
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
- [x] There is exactly **one** place where fault policy is evaluated per unit — not three independent hooks that happen to run in an order.
- [x] The application order (partition → loss → latency) is implemented and documented in [05](../05-fault-injection.md) and [03](../03-architecture.md#fault-injection-layer).
- [x] The draw discipline for dropped units is decided, implemented, and written into the determinism contract in [04](../04-api-design.md#determinism-contract).
- [x] With all three configured, behaviour matches the documented order.
- [x] Partitioning an unrelated pair does not change any other connection's fault trace.
- [x] The same seed with all three faults configured reproduces an identical trace across runs.
- [x] `-race` clean (verified on CI; not runnable on this dev box, see M2-1).

**Tests**
- `TestFaultOrderPartitionWinsOverLoss`, `TestFaultOrderLossWinsOverLatency`
- `TestDrawDisciplineStable` — assert the documented rule directly against the trace
- `TestAllFaultsDeterministic` — all three configured, same seed twice
- `TestUnrelatedPartitionDoesNotPerturb`
- `TestSingleDeliveryHookPerUnit`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M2-6 — Option validation

**Status:** done
**Decision:** the reporting mechanism was **already settled by M0-5 as panic** — this task's scope text below predates that closure and still presents it as a three-way open choice; not re-litigated here. Validation runs **once**, in `NewNetwork` after every `Option` has been applied (`networkConfig.validate`, called from `options.go`), not inside each `Option`'s own closure — so an invalid intermediate value later overridden by a valid one of the same kind does not panic (`TestOptionValidationUsesFinalValue`). The one exception is `WithPartition`: its peer-name checks (non-empty, `peerA != peerB`) are also deferred to the same `validate()` pass rather than checked eagerly inside `WithPartition` itself, for mechanism uniformity, even though partition pairs accumulate in a list rather than being overridden. **Unknown peer names are deliberately not validated** — M0-5 makes "partition before either side has `Dial`ed/`Listen`ed" legitimate setup, and this task's own scope line listing "empty *or unknown*" peer names was stale on that point. An unsupported `network` string was already handled by `validateNetwork` (`addr.go`) since M1, as a returned error rather than a panic — this task didn't touch it, since it isn't an `Option`.
**Roadmap item:** supports all four M2 checklist items
**Depends on:** M2-2, M2-3, M2-4, M0-5
**Blocks:** M4-1

**Objective**
Reject invalid configuration loudly. A silently clamped or ignored option produces a test that passes for the wrong reason — the worst possible failure mode for a testing library, since it manufactures false confidence in the code under test.

**Scope**
- Validate: packet-loss rate outside `[0.0, 1.0]` (including `NaN`, `+Inf`, `-Inf`); latency `min > max`; negative durations; empty or identical peer names in `WithPartition`.
- The reporting mechanism is panic, decided by M0-5; this task only implements the validation checks themselves.
- The message must name the offending option and the offending value.
- Out of scope: runtime I/O errors (M1-8); unknown (as opposed to empty/identical) peer names in `WithPartition`, which are legitimate per M0-5; the `network` string, validated elsewhere since M1.

**Files**
- `options.go` — validation
- `errors.go` — any new sentinels
- `options_test.go` — validation table

**Acceptance criteria**
- [x] Every invalid input listed above is rejected, table-driven.
- [x] `NaN` and infinite loss rates are rejected, not silently compared.
- [x] The failure mechanism is uniform across options and matches M0-5.
- [x] Every message names the option and the value.
- [x] Valid boundary values are accepted: rate exactly `0.0` and `1.0`, `min == max` latency, zero latency.
- [x] Godoc on each option states its valid range and what happens outside it.

**Tests**
- `TestOptionValidation` — table over invalid inputs, asserting the failure mechanism
- `TestOptionValidationUsesFinalValue` — an invalid intermediate value overridden by a later valid one does not panic
- `TestOptionBoundaryValuesAccepted`
- `TestValidationMessagesNameTheOption`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`
