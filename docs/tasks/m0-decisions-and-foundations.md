# M0 — Decisions & foundations

> Gates every other milestone. See the [task index](README.md) for the milestone map and conventions.

**Covers:** no v1 checklist item directly — this milestone resolves the open design questions that [06 — Scope & Roadmap](../06-scope-and-roadmap.md#reordering-in-or-out-of-v1) says must be settled *before the v1 API is finalized*, and establishes the test baseline the later milestones build on.

**Why it exists:** [07 — Contributing](../07-contributing.md) states that landing implementation against an API still being debated "risks churn and wasted work". Three questions currently change the public surface depending on how they resolve. M0 closes them, then freezes the API.

Tasks M0-1 through M0-4 are **decision tasks**: they produce a written decision, not code, so they carry *Options considered / Decision required / Where it gets recorded* instead of acceptance criteria and tests.

---

### M0-1 — Resolve whether reordering is in v1

**Status:** todo
**Roadmap item:** open question flagged in [05 — Fault Injection](../05-fault-injection.md#reordering-open-question) and [06 — Scope & Roadmap](../06-scope-and-roadmap.md#reordering-in-or-out-of-v1)
**Depends on:** —
**Blocks:** M0-5, and the write-ordering guarantees in M1-1 and M2-2

**Objective**
Settle the single documented contradiction in the design: the root `README.md` intro prose lists reordering as a fault netchaos injects, while the v1 checklist in the same file (and in [06](../06-scope-and-roadmap.md)) does not. Until this resolves, the delivery path in M1 cannot commit to a write-ordering guarantee.

**Options considered** (both stated verbatim in [05](../05-fault-injection.md#reordering-open-question))
1. **Reordering is in v1** — the checklist is incomplete and gains a fourth item. Cost: the delivery queue in M1-1 must support holding a window of pending writes and releasing them in a seeded-random permutation, and every fault-composition rule in M2-5 grows a case.
2. **Reordering is out of v1** — the README's intro prose is trimmed to "latency, packet loss, partitions", and reordering joins the deferred list in [06](../06-scope-and-roadmap.md#explicitly-out-of-scope-for-v1). Cost: none to the implementation; the README loses a selling point until post-v1.

Weighing input, not a decision: option 2 is the one consistent with [06](../06-scope-and-roadmap.md)'s own operating principle — "ship a narrow, solid core first" — and with its instruction to "treat reordering as **not yet in scope**" until explicitly decided. Option 1 is not wrong, it is simply larger.

**Decision required**
In or out. This is the maintainer's call — `AGENTS.md` explicitly instructs agents not to resolve it unilaterally.

**Where the decision gets recorded**
- [06 — Scope & Roadmap](../06-scope-and-roadmap.md) — replace the "Reordering: in or out of v1?" section with the outcome.
- [05 — Fault Injection](../05-fault-injection.md#reordering-open-question) — replace the open-question section with either the full mechanic (option 1) or a pointer to the deferred list (option 2).
- Root `README.md` — either add the fourth checklist item (option 1) or trim the intro prose (option 2).
- [docs/README.md](../README.md) — remove the "Open question flagged in these docs" section.
- A `needs-discussion` issue is the natural venue if outside input is wanted first; `.claude/commands/issue.md` covers the flow.

---

### M0-2 — Decide fault scoping: global vs. per-peer-pair

**Status:** todo
**Roadmap item:** open design question in [04 — API Design](../04-api-design.md)
**Depends on:** —
**Blocks:** M0-5, M2-2, M2-3, M2-5

**Objective**
Decide whether the latency and packet-loss options configured on a `Network` apply to *every* simulated connection in it, or can be scoped to a specific peer pair (e.g. only the `client → server-b` link is lossy).

**Options considered**
1. **Global for v1** — `WithLatency`/`WithPacketLoss` apply to all connections. This is what the README's example implies and what [04](../04-api-design.md) calls the v1 shape; per-pair scoping becomes "a plausible post-v1 refinement once real usage patterns are clearer".
2. **Per-pair from the start** — options grow a peer-pair selector. More expressive, but it multiplies the configuration surface and every fault task's test matrix before there is any real usage to justify it.

Note that partition is *already* inherently per-pair (`WithPartition(peerA, peerB)`), so choosing option 1 means v1 ships with deliberately asymmetric scoping — global for latency/loss, pair-scoped for partition. That asymmetry should be stated in the docs rather than left to be discovered.

**Decision required**
Global or per-pair for latency and packet loss in v1.

**Where the decision gets recorded**
- [04 — API Design](../04-api-design.md) — replace the "Open design question" paragraph with the decided behaviour, and note the latency/loss vs. partition asymmetry if option 1 is chosen.
- Godoc on `WithLatency` / `WithPacketLoss` (written in M4-1).

---

### M0-3 — Decide fault granularity: per-`Write` vs. per-simulated-packet

**Status:** todo
**Roadmap item:** open question in [05 — Fault Injection](../05-fault-injection.md) ("depending on how granular the v1 model ends up being")
**Depends on:** —
**Blocks:** M0-5, M1-1, M2-2, M2-3

**Objective**
Decide the unit that latency and loss are applied to. This is not a cosmetic choice — it determines the shape of the delivery queue built in M1-1, so it must be settled before that task starts.

**Options considered**
1. **Per-`Write` call** — each `Write` is one unit: delayed as a whole, or dropped as a whole. Simple, and the delivery queue only needs to hold whole write payloads. Downside: fault behaviour becomes coupled to how the caller happens to chunk its writes, which a real TCP stack would not do — a client doing one 64 KiB write and a client doing 64 one-KiB writes see very different loss behaviour at the same configured rate.
2. **Per-simulated-packet** — writes are split into fixed-size simulated segments (an MSS-like constant), and faults apply per segment. Closer to real network behaviour and insensitive to caller chunking, at the cost of a segmentation layer in M1-1 and a size constant that must be justified.

The interaction to think through either way: `net.Conn` is a **byte stream**, so dropping part of a write silently truncates the stream from the reader's perspective — the reader cannot tell that bytes went missing. Whichever option is chosen, M2-3 must define what a "dropped" write means to a stream-oriented reader (silent gap, or connection-level error).

**Decision required**
The unit of fault application, plus — as part of the same decision — what a dropped unit looks like to the reader.

**Where the decision gets recorded**
- [05 — Fault Injection](../05-fault-injection.md) — replace the parenthetical hedges in the Packet loss and Latency sections with the decided unit.
- [03 — Architecture](../03-architecture.md#fault-injection-layer) — same hedge appears there.

---

### M0-4 — Design the determinism-under-concurrency model

**Status:** todo
**Roadmap item:** underpins "Seeded randomness for reproducible failure scenarios" ([06](../06-scope-and-roadmap.md))
**Depends on:** M0-3 (the fault unit is what an RNG draw corresponds to)
**Blocks:** M0-5, M2-1

**Objective**
Make the determinism contract in [04 — API Design](../04-api-design.md#determinism-contract) actually achievable. **This is the highest-risk item in the whole project**: if it is left implicit, the library's headline claim is false in exactly the multi-goroutine scenarios it exists to test.

**The problem**
[04](../04-api-design.md#determinism-contract) promises that for a fixed seed, a fixed sequence of `Network` calls "produces an identical sequence of injected faults across runs and across machines". But a realistic test has several goroutines doing I/O concurrently — a client writing while a server reads, two clients against one server. With a single shared `rand.Rand` behind a mutex, the *order in which goroutines reach the RNG* decides which draw each one gets, and that order is set by the Go scheduler. Same seed, same calls, different fault sequence. The mutex makes it race-free, not deterministic.

**Options considered**
1. **Per-connection derived streams** — the master seed seeds only a derivation function; each connection (and, if directions can be faulted independently, each direction) gets its own `rand.Rand` derived from `(masterSeed, connectionOrdinal, direction)`. A connection's draw sequence then depends only on its own I/O, not on interleaving with other connections. Requires connection ordinals to be assigned deterministically — i.e. `Dial`/`Listen` order, which the contract already fixes.
2. **Single global RNG, serialized** — one `rand.Rand` under a mutex. Simplest, and genuinely deterministic *only* in the single-goroutine case. Would require weakening the contract in [04](../04-api-design.md#determinism-contract) to "deterministic for tests whose I/O is sequenced", which undercuts the concurrency-testing use case.
3. **Precomputed fault schedule** — draw the entire fault sequence up front from the seed and consume it positionally. Deterministic, but requires knowing the number of draws in advance, which a byte-stream workload does not.

Option 1 is the recommendation; it is the only one that keeps the contract as written without constraining how tests use goroutines.

**Decision required**
The derivation scheme, and — whichever option is chosen — the *exact* wording of the determinism guarantee, including its limits. If any scenario is outside the guarantee, [04](../04-api-design.md#determinism-contract) must say so rather than over-promising.

**Where the decision gets recorded**
- [04 — API Design](../04-api-design.md#determinism-contract) — expand the determinism contract with the derivation model and its stated limits.
- [03 — Architecture](../03-architecture.md#fault-injection-layer) — the sentence describing "the same seeded random source owned by the `Network`" needs to match.

---

### M0-5 — Freeze the v1 API surface

**Status:** todo
**Roadmap item:** prerequisite for every code task in M1–M2
**Depends on:** M0-1, M0-2, M0-3, M0-4
**Blocks:** M1-5, M1-6, M1-7, M2-2, M2-3, M2-4

**Objective**
Turn [04 — API Design](../04-api-design.md)'s **PROPOSED / NOT YET IMPLEMENTED** sketch into an agreed set of signatures, so M1 and M2 implement against a fixed target instead of a moving one.

**Scope**
- Reconcile every signature in [04](../04-api-design.md) with the outcomes of M0-1 through M0-4.
- Confirm the flat single-package shape (`package netchaos`, no subpackages) still holds given those outcomes.
- Decide the error-returning surface: [04](../04-api-design.md#dynamic-partition-control) currently has `Partition`/`Heal` returning nothing — settle whether healing a non-existent partition, or partitioning an unknown peer, is a silent no-op, a panic, or an error return. Same question for `NewNetwork` given an invalid option (see M2-6).
- Confirm `Dial`'s signature. [04](../04-api-design.md#dialing-and-listening) uses `Dial(network, addr string)` so it drops into a `func(network, addr string) (net.Conn, error)` hole; decide whether a `DialContext` variant ships in v1 too, since HTTP and gRPC transports want `func(ctx, network, addr)`.
- Out of scope: implementing any of it.

**Files**
- [`docs/04-api-design.md`](../04-api-design.md) — replace the PROPOSED banner with the frozen surface; keep the usage sketch in sync.

**Acceptance criteria**
- [ ] Every exported identifier v1 will ship is listed with its final signature.
- [ ] Each of M0-1..M0-4's decisions is visibly reflected in at least one signature or doc paragraph.
- [ ] Error/no-op behaviour is specified for `Partition`, `Heal`, `Dial` to an unregistered address, and invalid options.
- [ ] The `DialContext` question is answered either way, in writing.
- [ ] The **PROPOSED / NOT YET IMPLEMENTED** banner is replaced by wording that distinguishes "API agreed" from "API implemented" — the latter is still false at this point.

**Tests**
None — documentation task. Once M1 exists, an `api_test.go` compile-time assertion (assigning `Network.Dial` to a `func(string, string) (net.Conn, error)` variable, asserting `*conn` satisfies `net.Conn`) is the mechanical guard that the frozen surface is honoured; that assertion is written in M1-2.

---

### M0-6 — Establish the test baseline

**Status:** todo
**Roadmap item:** prerequisite for every code task
**Depends on:** —
**Blocks:** M1-1

**Objective**
The module currently contains one Go file — `doc.go`, a package comment — and **zero test files**. CI has therefore never actually run a test, a race detector pass, or golangci-lint over real code. Establish that baseline before the first real implementation lands, so a failure in M1 is unambiguously a code problem rather than a tooling one.

**Scope**
- Add the first `_test.go` to the module, exercising the toolchain rather than any feature (the trivial case: a test that asserts the package builds and a placeholder helper behaves).
- Confirm each command in the verification block runs clean locally and on CI.
- Confirm `golangci-lint` v2 with `.golangci.yml` (govet, staticcheck, unused, errcheck; gofmt + goimports formatters) reports nothing on a file containing real code — `unused` in particular has never had unexported identifiers to evaluate.
- Confirm the CI matrix (Go 1.25 and 1.26, `.github/workflows/ci.yml`) passes on both versions with a test file present. This matters because M3 depends on `testing/synctest`, which is only stable from Go 1.25 — the `go 1.25` directive in `go.mod` is what makes that available.
- Out of scope: adding a Makefile or Taskfile; none exists and none is required.

**Files**
- One new `_test.go` at the module root (replaced by real tests as M1 lands).

**Acceptance criteria**
- [ ] `go build ./...` succeeds.
- [ ] `go vet ./...` reports nothing.
- [ ] `gofmt -l .` prints nothing.
- [ ] `go test -race ./...` passes, running at least one real test (not `[no test files]`).
- [ ] `golangci-lint run` reports nothing.
- [ ] CI is green on both Go 1.25 and Go 1.26.
- [ ] `testing/synctest` is confirmed importable and `synctest.Test` / `synctest.Wait` resolve on both matrix versions.

**Tests**
- The placeholder test itself is the deliverable.
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`
