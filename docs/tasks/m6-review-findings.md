# M6 — Post-v0.1.0 review findings

> See the [task index](README.md) for the milestone map and conventions.

**Covers:** no v1 checklist item — v1 shipped and is closed. This milestone is the recorded output of a full review of the `v0.1.0` source tree (production code, tests, and the `docs/` set), rather than a grouping of planned work. Every task below traces to something read in the code, not to a wish list.

**What this milestone is for:** [M5](m5-hardening-and-ergonomics.md) closes out the release and does process groundwork. This one is the engineering backlog that review turned up: one contract contradiction, a handful of small correctness and precision fixes, three test-coverage gaps, and the feature candidates that pass [06 — Scope & Roadmap](../06-scope-and-roadmap.md)'s own scope rubric — recorded so they exist as decisions to make, not as work that has been agreed to.

**What this milestone deliberately does not do:** it decides nothing on its own. The decision tasks (`M6-10` through `M6-17`) produce a written decision, per the [M0](m0-decisions-and-foundations.md) precedent — they do not authorize the feature they describe. Two of them (`M6-15` reordering, `M6-16` per-peer-pair scoping) restate questions that [06 — Scope & Roadmap](../06-scope-and-roadmap.md) has already gated on "real usage evidence rather than a fixed timeline," and `AGENTS.md` says the reordering question is not to be resolved without the maintainer; neither task reopens either question, they only record that the review reached them and found the gate still closed.

**Triage — what actually matters here.** Not all seventeen are worth the same attention, and reading them as a flat list is misleading:

1. **`M6-1` is the only finding that touches a stated contract.** Everything else is either invisible to users or a question rather than a defect. If only one task from this milestone is ever done, it is this one.
2. **`M6-5` and `M6-6` are the ones most likely to find something nobody predicted.** A conformance suite and the named coverage gaps test claims the existing suite asserts by construction rather than by observation; that is where an unknown defect would still be hiding.
3. **`M6-3`, `M6-4` and `M6-8` are trivia** — an error message, a comment that overstates its own precision, and a timer allocation no user can observe. `M6-8` in particular has a worse risk/benefit than it first looks (see the task); doing it badly breaks delivery ordering, and doing it well saves an allocation nobody measured.

---

### M6-1 — Reconcile ordinal assignment with the determinism contract

**Status:** done — resolved as **outcome 1** (the code now matches the doc)
**Roadmap item:** none (correctness — the determinism contract in [04 — API Design](../04-api-design.md#determinism-contract))
**Depends on:** —
**Blocks:** —

**Objective**
[04 — API Design](../04-api-design.md#determinism-contract) states, at the end of the `connectionOrdinal` bullet: *"the ordinal is assigned only once the wait clears and the listener lookup succeeds, so a dial that never establishes never burns one."* The first clause describes `DialContext` accurately. The final clause does not: `netchaos.go:186-187` assigns and increments `n.nextOrdinal` immediately after the listener lookup, but the dial can still fail afterwards at `l.enqueue` (`netchaos.go:211`), which returns an error and no connection. The ordinal is consumed anyway, so every subsequent connection in that `Network` shifts by one and draws from a different RNG stream than it would have. The doc and the code disagree; this task resolves the disagreement, in either direction.

**Two failure paths, not one**
`listener.enqueue` (`listener.go:44-57`) fails two ways, and both are downstream of the assignment:
- `ErrBacklogFull` (`listener.go:55`) — the accept queue is at `listenerBacklog`.
- `ErrConnectionRefused` (`listener.go:49`) — the listener was closed between the lookup at `netchaos.go:181` and the enqueue.

Note that the *other* refused path — no listener ever registered for that peer (`netchaos.go:182-184`) — returns before the assignment and is already correct. "Refused dials are safe" is therefore true of one path and false of the other.

**Why the obvious fixes do not work**
Recorded here so they are not rediscovered and tried:
- *Move the assignment after the enqueue.* Not possible as-is: the ordinal feeds both the synthesized `ephemeral:%d` name (`netchaos.go:190-192`) and `newConnPairWithSeed` (`:194`), so the `*conn` that gets enqueued cannot be built without it.
- *Hold `n.mu` across the enqueue to make assignment and enqueue atomic.* This introduces a real deadlock. `DialContext` deliberately releases `n.mu` at `netchaos.go:188` **before** calling `l.enqueue`, which takes `l.mu`; `listener.Close` takes `l.mu` and then calls `n.deregister`, which takes `n.mu` (`listener.go:63-72`). The current code has exactly one lock order and no cycle. **Do not turn `netchaos.go:188` into a `defer`, and do not extend the critical section over the enqueue.**
- *Decrement `nextOrdinal` on failure.* Wrong under concurrent dials — another dial may already have taken the next ordinal, and rolling back hands out a duplicate.
- *Check the queue's length before assigning.* Racy, and it does not address the closed-listener path at all.

Which leaves two honest outcomes, and this task picks one rather than assuming the first:
1. **Make the code match the doc** — e.g. reserve a slot in the accept queue before assigning the ordinal and fill it afterwards, keeping the existing lock order intact.
2. **Narrow the doc to match the code** — the sentence's own preceding clause ("assigned only once the wait clears and the listener lookup succeeds") already describes the real mechanism; the general claim that follows it is what overreaches. This is a legitimate outcome, not a cop-out, *provided* the narrowed wording is explicit that a failed enqueue consumes an ordinal.

**Scope**
- Decide between the two outcomes above and implement it.
- Whichever is chosen, the behaviour must end up asserted by a test. The reason this survived to `v0.1.0` is worth stating precisely: `TestBacklogFull` (`listener_test.go:142-159`) calls `ln.enqueue(dummyConn())` directly in a loop, bypassing `DialContext` entirely. It proves `enqueue` returns `ErrBacklogFull` at capacity and never exercises the dial path where the ordinal is assigned — so no existing test could have caught this, regardless of what it asserted.
- Out of scope: any change to how ordinals are derived or to the derivation tuple in `rand.go`. This is about *when* one is handed out.

**Files**
- `netchaos.go` — the assignment site and `DialContext`'s godoc at `:145-151`, which repeats the same claim in the partition-cancellation framing
- `listener.go` — only if outcome 1 needs a reservation primitive
- `docs/04-api-design.md` — the `connectionOrdinal` bullet, under either outcome
- `dial_test.go` / `listener_test.go`

**Acceptance criteria**
- [x] A test exercises a dial that fails at `enqueue` with `ErrBacklogFull` and asserts the resulting ordinal behaviour, whichever way it was decided. — `TestBacklogFullOrdinalAccounting`, red at 129/want 128 before the fix.
- [x] The closed-listener `ErrConnectionRefused` path from `enqueue` is covered by the same assertion. — `TestClosedListenerDialOrdinalAccounting`, red at 1/want 0 before the fix.
- [x] `docs/04-api-design.md`'s `connectionOrdinal` bullet and `DialContext`'s godoc state the same thing as each other and as the code.
- [x] The `n.mu` / `l.mu` lock order is unchanged, with a comment at `netchaos.go:188` recording why the unlock sits where it does.
- [x] Existing golden traces in `testdata/traces/` are unaffected, or the change to them is deliberate and explained (per `M3-3`, a golden diff is a contract-change signal). — byte-identical, as expected: no golden scenario contains a failing dial.
- [x] `-race` clean on CI. — confirmed on PR #40: `test (1.25)` and `test (1.26)` both pass, along with `lint` and the `gofmt` check. Not verifiable locally (`CGO_ENABLED=0`, no C toolchain on the dev machine, per `AGENTS.md`), which is why this box was ticked from the CI run rather than a local one.

**How it was resolved**
Outcome 1, via a reservation primitive. `listener` gained `reserve`/`fill`: `reserve` claims an accept-queue slot under `l.mu`, checking both remaining failure modes (closed listener, full backlog); `fill` hands the `*conn` to the claimed slot and cannot block or fail, because the capacity was already accounted for. `DialContext` now reserves *before* taking an ordinal, which makes a successful reserve the point the dial commits — past it, establishment cannot fail, so an ordinal is only ever spent on a connection that establishes.

`enqueue` survives as a thin `reserve`-then-`fill` wrapper rather than being replaced, so `TestBacklogFull` and the two `errors_test.go` callers that exercise the capacity bound directly are untouched by this change.

**One semantics consequence, recorded deliberately**
A `Close` landing between the reserve and the fill now closes the filled conn instead of failing the dial, so the dialer gets a live conn whose peer is already closed rather than `ErrConnectionRefused`. This is not a new outcome: `listener.Close` already closes queued-but-unaccepted conns, so "successful dial, dead peer" was always reachable — the reservation widens an existing window. It is the price of the contract holding on every path, and it is noted in `fill`'s godoc so a later reader does not mistake it for an accident.

**Tests**
- Red first: write a test that dials until the backlog is full, lets one dial fail, then completes a successful dial and compares its ordinal (or its recorded trace) against the same sequence run without the failing dial. Confirm it fails before touching production code.
- `TestBacklogFullOrdinalAccounting`, `TestClosedListenerEnqueueOrdinalAccounting`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M6-2 — Decide a single error-wrapping policy across Listen, Dial and DialContext

**Status:** todo
**Decision:** *(not yet made — this is a decision task; it changes observable behaviour)*
**Roadmap item:** none (API consistency)
**Depends on:** —
**Blocks:** —

**Objective**
Errors leave the package in three different shapes depending on which line produced them, and the split does not follow a rule anyone could state:

| Site | Shape |
|---|---|
| `DialContext` ctx already done (`netchaos.go:164`) | bare `ctx.Err()` |
| `DialContext` bad network (`netchaos.go:168`) | bare `validateNetwork` error |
| `DialContext` refused (`netchaos.go:184`), enqueue failure (`:212`) | `*net.OpError` |
| `Listen` bad network (`netchaos.go:95`) | bare error |
| `Listen` address in use (`netchaos.go:103`) | bare `ErrAddressInUse` |
| `listener.Accept` on closed (`listener.go:37`) | `*net.OpError` |

Real `net.Listen` and `net.Dial` return `*net.OpError` for all of these. Code under test that type-asserts to `*net.OpError`, or that calls `Timeout()`/`Temporary()` on the result, behaves differently against netchaos than against the standard library — which cuts against the library's central adoption claim that it substitutes for the real thing without rewrites.

**Why this is a decision task and not a cleanup**
Wrapping changes observable behaviour in two ways that are not cosmetic: `err.Error()` strings change, and direct `==` comparison against a sentinel stops working where it currently works (today `err == ErrAddressInUse` succeeds for `Listen`). [07 — Contributing](../07-contributing.md) requires an issue first for any change to an exported signature's behaviour; this is that class of change even though no signature moves.

**Options considered**
1. **Wrap everything in `*net.OpError`, uniformly.** Closest to the standard library, and `errors.Is` against every sentinel keeps working because `OpError` unwraps. Costs: message strings change, and `==` comparisons in any existing user code break silently.
2. **Wrap nothing; return bare errors everywhere.** Also consistent, and simpler to document — but it moves *away* from `net` semantics and throws away the `Op`/`Net`/`Addr` context that makes a failure legible in test output.
3. **Leave it as-is and document the split.** Zero risk, but there is no rule to document — the current split is an artifact of which lines were written in which milestone.

Weighing input, not a decision: option 1 is the only one that serves the substitutability claim, and the sentinels are already documented as `errors.Is` targets (`errors.go`), which is the comparison style the package's own tests use. The real question is whether a behaviour change of this size is worth making before `v1.0.0` or at all, given `v0.1.0` has no known external users.

**Decision required**
Which of the three, and if option 1, whether it lands before `v1.0.0`.

**Where the decision gets recorded**
- An issue per [07 — Contributing](../07-contributing.md)'s issue-first rule, labeled `needs-discussion`.
- [04 — API Design](../04-api-design.md#error-and-no-op-behaviour)'s error-behaviour section, and `errors.go`'s package-level comment, once decided.

---

### M6-3 — Extend the udp rejection message to udp4 and udp6

**Status:** todo
**Roadmap item:** none (error-message quality)
**Depends on:** —
**Blocks:** —

**Objective**
`validateNetwork` (`addr.go:35-44`) special-cases the exact string `"udp"` to return the helpful *"udp support is out of scope for netchaos v1"* message. `"udp4"` and `"udp6"` fall through to the generic `default` branch and get `unsupported network: "udp4"` instead — the same explanation exists, the caller just does not receive it. `docs/06-scope-and-roadmap.md` treats UDP as one excluded thing, not three.

**Scope**
- Add `"udp4"` and `"udp6"` to the case that produces the explanatory message.
- Out of scope: supporting UDP in any form; changing `ErrUnsupportedNetwork` or the message for genuinely unknown networks.

**Files**
- `addr.go`
- `addr_test.go`

**Acceptance criteria**
- [ ] `validateNetwork("udp4")` and `validateNetwork("udp6")` return the same explanatory message as `"udp"`, still satisfying `errors.Is(err, ErrUnsupportedNetwork)`.
- [ ] An unrelated unknown network (e.g. `"unix"`) still gets the generic message.

**Tests**
- Red first: extend the existing network-validation test with the two new inputs and watch it fail on the message assertion.
- `TestValidateNetworkRejectsUDPVariants`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M6-4 — Correct the "exactly one draw" claim

**Status:** todo
**Roadmap item:** none (documentation precision)
**Depends on:** —
**Blocks:** —

**Objective**
`uniformDuration`'s comment (`rand.go:79-83`) says it consumes *"exactly one draw regardless of whether min == max."* It does not, strictly: it calls `boundedUint64` (`rand.go:94-103`), whose Lemire rejection loop draws again when the sample falls in the biased zone. The probability is negligible (roughly `span / 2^64`), but the comment states a precision it does not have, and `docs/05-fault-injection.md:13` carries a variant of the same claim.

The property the comment is *reaching for* is true and worth keeping: the fixed case is not special-cased, so a fault kind's draw sequence advances once per unit at the level that matters for the trace. Determinism is untouched either way — streams are per `(ordinal, side, kind)`, so however many raw draws a rejection consumes, the sequence is a deterministic function of the stream.

**Scope**
- Reword `rand.go:79-83` to state the invariant that actually holds (one *value* per unit, not one raw draw), and note the rejection loop.
- Check `docs/05-fault-injection.md:13` and `:33` for the same overstatement. `:33`'s claim about `bernoulli` is accurate as written — `bernoulli` consumes exactly one draw — so only the latency wording needs attention.
- Out of scope: changing `boundedUint64`'s algorithm. The rejection loop is what makes the distribution unbiased, and `TestDeriveStreamGoldenVector` pins its output.

**Files**
- `rand.go`
- `docs/05-fault-injection.md`

**Acceptance criteria**
- [ ] `rand.go`'s comment no longer claims a raw-draw count it cannot guarantee, and names the rejection loop as the reason.
- [ ] `docs/05-fault-injection.md`'s latency wording agrees with it.
- [ ] No behaviour change; golden vectors and traces are untouched.

**Tests**
- None — comment and prose only. Verified by `go build ./... && go vet ./... && gofmt -l . && go test -race ./...` still passing unchanged.

---

### M6-5 — Run the standard net.Conn conformance suite

**Status:** todo
**Decision:** *(not yet made — accepting a first dependency is part of this task)*
**Roadmap item:** none (validates the "drop-in `net.Conn`" claim the library is sold on)
**Depends on:** —
**Blocks:** —

**Objective**
netchaos's headline claim is that its `net.Conn` substitutes for the real one. `golang.org/x/net/nettest`'s `TestConn` is the standard harness for exactly that claim — it is what the standard library's own `net.Pipe` is validated against — and the repo does not run it. The existing suite asserts conformance through hand-written tests and compile-time interface assertions (`api_test.go`), which check the shape of the interface but not the semantics behind it.

**The dependency question**
`go.mod` currently has **zero** dependencies, which is a real property of the library and looks deliberate. `nettest` would be a test-only requirement (it does not enter any consumer's build graph through this module's non-test code), but it is still a `require` line. That trade is the decision embedded in this task; if it is refused, the task is `dropped`, not silently skipped.

**Scope**
- Add a `TestConnConformance` that hands `nettest.TestConn` a `MakePipe` built on netchaos.
- **The conformance run must use a fault-free `Network`** — no `WithLatency`, no `WithPacketLoss`, no partition. `nettest.TestConn` asserts vanilla stream semantics that the faults deliberately violate: a dropped write returns `n = len(p), nil` with nothing ever delivered (the silent-gap model decided in `M0-3`), which fails `BasicIO` by design. A failure under an injected fault would be the suite working correctly, not a defect.
- Triage whatever it reports. Some subtests may be legitimately inapplicable — netchaos has no half-close (`conn.go:22-31`) — and the honest response to those is a documented skip with the reason, not a behaviour change.
- Out of scope: fixing anything the suite finds beyond trivial corrections. A real semantic gap gets its own task rather than being folded in here.

**Files**
- `go.mod`, `go.sum`
- `nettest_test.go` (new)

**Acceptance criteria**
- [ ] The dependency decision is recorded (accepted with rationale, or the task is marked `dropped`).
- [ ] `nettest.TestConn` runs against a fault-free netchaos `Network` and passes, with any skipped subtest naming the design decision that makes it inapplicable.
- [ ] `go.mod`'s new requirement is test-only and does not appear in a consumer's build graph for non-test code.
- [ ] CI stays green on both Go 1.25 and 1.26.

**Tests**
- `TestConnConformance` is itself the test. Red is not meaningful here in the usual sense — the suite either passes or reports a real gap; record what it reported on first run either way.
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M6-6 — Close the three named test-coverage gaps

**Status:** todo
**Roadmap item:** none (test hardening)
**Depends on:** —
**Blocks:** —

**Objective**
Three specific gaps, each one a behaviour the code has but no test observes:

1. **Zero-length `Write`.** `conn.Write(nil)` / `Write([]byte{})` admits an empty unit that still consumes a loss draw and a latency draw. Nothing asserts what should happen — whether an empty write is a fault unit at all is currently answered only by whichever line of code runs first.
2. **Writer wedging under latency.** `TestLossDoesNotWedgeWriterOnRepeatedDrops` (`loss_test.go`) covers the loss analogue, but there is no latency equivalent. Pending units hold their `bufBytes` accounting until released, so a slow reader under high latency is the case where the 64 KiB bound and the pending queue interact — exactly where an accounting bug would live.
3. **`leak_test.go:59-69` polls `runtime.NumGoroutine()` on a wall-clock loop for up to two seconds.** Goroutine counts are noisy under parallel test execution, which makes this the one genuinely flake-prone test in an otherwise deterministic suite — and a flaky leak test is worse than none, because it trains people to re-run it.

**Scope**
- Add tests for (1) and (2). For (1), decide and then document the intended semantics — treating an empty write as a no-op that consumes no draws is a *behaviour change* to the draw discipline and would need `M6-11`-style handling, so prefer asserting what the code already does unless it is clearly wrong.
- For (3), replace the wall-clock poll with a deterministic signal. `synctest`'s bubble-exit already proves the property for bubbled paths (`synctest_test.go`); the goroutine-count baseline may be redundant with it.
- Out of scope: broad coverage work. These three, nothing else.

**Files**
- `conn_test.go` or `pipe_test.go`, `latency_test.go`, `leak_test.go`

**Acceptance criteria**
- [ ] Zero-length `Write` behaviour is asserted, and the draw-consumption consequence is stated in a comment or godoc.
- [ ] A slow reader under high latency does not wedge the writer, asserted in virtual time.
- [ ] `leak_test.go` no longer depends on a wall-clock polling window.
- [ ] The suite's runtime does not regress meaningfully.

**Tests**
- `TestZeroLengthWrite`, `TestLatencyDoesNotWedgeSlowReader`, and the rewritten leak assertion.
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M6-7 — Add a fuzz target and baseline benchmarks

**Status:** todo
**Roadmap item:** none (test hardening)
**Depends on:** —
**Blocks:** M6-8 (its `BenchmarkWriteUnderLatency` is how M6-8's benefit gets measured)

**Objective**
The repo has zero `Fuzz*` and zero `Benchmark*` functions. The pipe's buffer accounting is the natural fuzz target: `bufBytes`, the 64 KiB `bound`, the oversized-write rule (a write larger than the bound is admitted only when the pipe is empty, `pipe.go:132-138`), and partial/coalesced reads form a small state machine over write and read sizes, where the invariants are easy to state and a bad interleaving is hard to find by hand. Benchmarks matter less on their own, but without a baseline there is no way to tell whether a later change — `M6-8`, or any new fault kind from `M6-11` — cost anything.

**Scope**
- A fuzz target over a sequence of write and read sizes, asserting the invariants that must hold at every step: `bufBytes` equals the sum of buffered payload lengths, never exceeds `bound` except via the oversized-write rule, bytes read out equal bytes written in and in order, and a close always drains.
- Benchmarks for the paths worth watching: fault-free write/read round trip, write under loss, write under latency.
- Out of scope: performance work. This task measures; it does not optimize.

**Files**
- `fuzz_test.go` (new), `bench_test.go` (new)

**Acceptance criteria**
- [ ] `go test -run=Fuzz -fuzz=FuzzPipeAccounting -fuzztime=30s` runs clean, and any seed corpus it produces is committed.
- [ ] Benchmarks exist for the three named paths and their first results are recorded in the PR description as the baseline.
- [ ] The fuzz target runs as a normal (non-fuzzing) test in CI against its seed corpus, so it costs nothing per-run but still guards regressions.

**Tests**
- `FuzzPipeAccounting`; `BenchmarkRoundTrip`, `BenchmarkWriteUnderLoss`, `BenchmarkWriteUnderLatency`.
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M6-8 — Avoid re-arming the latency timer when the pending head is unchanged

**Status:** todo
**Roadmap item:** none (efficiency)
**Depends on:** [M6-7](#m6-7--add-a-fuzz-target-and-baseline-benchmarks) — without its latency benchmark there is no way to tell whether this changed anything
**Blocks:** —

**Objective**
`armLatencyTimerLocked` (`latency.go:53-66`) unconditionally stops and recreates the timer, and `installFaultPolicy`'s evaluator calls it on every admitted unit (`faults.go:94`). Because `pending` is release-ordered and new units append to the tail, admitting a unit changes the head only when `pending` was previously empty — so in the steady state every write allocates a fresh `*time.Timer` to re-arm for the same deadline it was already armed for.

**Read this before starting.** The benefit is one avoided allocation per write under latency, which no user has reported and no benchmark currently measures. The risk is not symmetric: a re-arm guard that is wrong in the other direction — failing to re-arm when the head *did* change — silently stops delivering pending units, which is a data-loss bug in the library's core path. This task is only worth doing carefully, and is a reasonable one to mark `dropped` on the grounds that the trade is not favourable.

**Scope**
- Guard the re-arm on whether the head's `releaseAt` actually changed; prefer `Timer.Reset` over stop-and-recreate where the head moved.
- Preserve exactly: the ordering guarantee (`M2-2` — units release in write order), the discard-on-close behaviour, and the benign-but-correct handling of a fired-but-not-yet-run callback being superseded.
- Land `M6-7`'s `BenchmarkWriteUnderLatency` first, or the improvement is unmeasured.
- Out of scope: changing the single-timer design for one-timer-per-unit. `M2-2` decided that explicitly, and a per-unit timer permits reordering.

**Files**
- `latency.go`
- `latency_test.go`, `latency_synctest_test.go`

**Acceptance criteria**
- [ ] No timer is recreated when a write appends behind an existing pending head.
- [ ] A write that *does* become the new head arms correctly — asserted directly, not assumed.
- [ ] All existing latency tests pass unchanged, including ordering and close-in-flight.
- [ ] The benchmark shows a measurable reduction in allocations per write under latency, or the task is marked `dropped` with that result recorded.

**Tests**
- Red first: a test asserting the timer is not replaced on an append behind the head (via an allocation count or a test-only counter).
- `TestLatencyTimerNotRearmedBehindHead`, plus the existing ordering suite as the regression guard.
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M6-9 — Cross-reference the ephemeral-dialer caveat from Partition and Heal

**Status:** todo
**Roadmap item:** none (documentation)
**Depends on:** —
**Blocks:** —

**Objective**
A dialer that never calls `WithPeerName` gets a synthesized `ephemeral:N` identity (`netchaos.go:190-192`), and `Partition` is a documented silent no-op on names it does not know (`partition.go:50-53`). Composed, a test can call `Partition("client", "server")`, receive no error, and be running entirely unpartitioned traffic:

```go
c, _ := n.Dial("tcp", "server")   // this peer is "ephemeral:0", not "client"
n.Partition("client", "server")   // no-op, no diagnostic, traffic keeps flowing
```

Each half is deliberate and each is already documented *from the `WithPeerName` side* — `addr.go:46-60`, `addr.go:63-71`, and `DialContext`'s godoc at `netchaos.go:138-143` all spell it out. What is missing is the reverse direction: `Partition`'s and `Heal`'s own godoc say "a no-op ... if either peer has never Dialed or Listened," which does not tell a reader that a peer *can* have dialed and still not be targetable. Someone reaching for `Partition` first has no path to the caveat.

**Scope**
- Add the cross-reference to `Partition` and `Heal`'s godoc: naming a dialer requires `WithPeerName`, and without it the dialer is not partition-targetable under any name.
- Out of scope — and this is the substantive half: whether `Partition` should surface a diagnostic at all instead of silently no-op'ing. That is a semantics change to the frozen surface, and [M5-2](m5-hardening-and-ergonomics.md#m5-2--api-ergonomics-review-before-v100) already lists `Partition`/`Heal`'s silent-no-op behaviour as one of its three named review points. This finding is **input to that review** — recorded here, decided there. Do not change the semantics under this task.

**Decided (M5-2, finding F3): the silent no-op stays.** The composed failure was reproduced empirically against the published `v0.1.0` — an unnamed dialer is `ephemeral:0`, `Partition("client","server")` is a no-op, and a subsequent write is delivered. But `Partition` cannot distinguish "peer not connected yet" from "peer will never exist" without breaking the legitimate "start partitioned" setup pattern. M5-2 located the real cause elsewhere: `Dial` is *structurally* unable to carry a peer name, because `WithPeerName` writes to a `context.Context` and `Dial` has no context parameter (M5-2 finding F2, raised as [#36](https://github.com/jpgomesr/netchaos/issues/36)). **This task's godoc cross-reference is still worth doing** and is unaffected by that decision — if anything it matters more, since the caveat is now known to be permanent for `Dial` rather than merely easy to miss.

**Files**
- `partition.go`

**Acceptance criteria**
- [ ] `Partition` and `Heal` godoc point a reader to `WithPeerName` and state the ephemeral-dialer consequence.
- [ ] No behaviour change.
- [ ] The task text records the semantics question as `M5-2` input, so the review picks it up without depending on this box.

**Tests**
- None — godoc only. Verify the rendered doc reads correctly with `go doc github.com/jpgomesr/netchaos.Network.Partition`.

---

### M6-10 — Decide whether addresses should have a host:port shape

**Status:** todo
**Decision:** *(not yet made — this is a decision task)*
**Roadmap item:** none — adoption friction found by review
**Depends on:** —
**Blocks:** —

**Objective**
`peerName` is the identity function (`addr.go:32`): the string passed to `Listen`/`Dial` *is* the peer name, with no host/port structure, and `addr.String()` returns it verbatim. So `Listen("tcp", "server")` yields a conn whose `RemoteAddr().String()` is `"server"`, and any code under test that calls `net.SplitHostPort` on a remote address — logging, metrics labelling, allow-listing, anything that wants the host half — gets an error against netchaos where it works against the real stack. `Listen` also has no `:0` ephemeral-port equivalent.

This is the one review finding that bears directly on the adoption claim in [01 — Vision](../01-vision.md): the cost is paid by code the user does not want to change, which is the specific friction netchaos exists to avoid.

**Options considered**
1. **Synthesize a port** — peers become `name:0` or get assigned sequential ports, so `SplitHostPort` succeeds. Costs: it changes every address string in existing test output and godoc examples, it makes the address→peer mapping non-trivial for the first time (`addr.go:8-16` currently guarantees exactly one place that relationship is defined), and it raises the question of what `Partition("server")` means when the peer is `server:0`.
2. **Accept an optional `host:port` form** — `peerName` strips a port when present, so `Listen("tcp", "server:8080")` works and `"server"` keeps working. Backwards compatible; costs the single-definition simplicity.
3. **Document the limitation and do nothing.** Zero risk. The friction stays, and it stays invisible until someone hits it.

Weighing input, not a decision: option 2 preserves every existing usage while removing the failure mode, but "the address is the peer name" is load-bearing simplicity that `addr.go`'s own comment calls out deliberately — and with no external users yet, there is no evidence anyone has hit this. Worth pairing with [M5-2](m5-hardening-and-ergonomics.md#m5-2--api-ergonomics-review-before-v100), which is looking at the same surface in the same window.

**M5-2 confirmed the finding and filed [#37](https://github.com/jpgomesr/netchaos/issues/37)** (its finding F4) rather than deciding it — the change breaks every address string a test prints, so it belongs to [07](../07-contributing.md)'s issue-first process. The decision itself is still open and still this task's.

**Decision required**
Whether addresses gain any port structure before `v1.0.0`, given that adding one afterwards is a breaking change to every address string a test prints.

**Where the decision gets recorded**
- A `needs-discussion` issue, cross-linked to `M5-2`.
- [04 — API Design](../04-api-design.md#dialing-and-listening)'s dialing/listening section and `addr.go`'s type comment, if it changes.

---

### M6-11 — Decide which new fault kinds, if any, enter v0.2.0

**Status:** todo
**Decision:** *(not yet made — this is a decision task)*
**Roadmap item:** none — candidates generated by applying [06's own scope rubric](../06-scope-and-roadmap.md#how-to-think-about-future-scope-decisions)
**Depends on:** —
**Blocks:** —

**Objective**
[06 — Scope & Roadmap](../06-scope-and-roadmap.md)'s rubric asks three questions of any proposed addition: does it operate at the connection/byte-stream layer, can it be added without adopters rewriting how their program executes, and is the core shipped. Four fault kinds pass all three and are not named anywhere in the existing docs — so they are unrecorded, rather than deliberately excluded. This task records them and decides.

**The candidates**
1. **Bandwidth throttling / rate limiting.** The most defensible: it is purely a delivery-timing model, it composes as a fourth stage in the existing `faults.go` evaluator alongside latency, and "the link is slow" is a first-class thing tests want. Note it interacts with the pipe's 64 KiB bound (`M6-17`) — a throttle that is slower than the reader is what makes the bound observable.
2. **Mid-stream connection reset.** The clearest *gap* rather than an enhancement: there is currently no way to make an established conn fail abruptly. `Partition` is the nearest thing and is deliberately a silent black hole (writes succeed, reads block to deadline), not an `ECONNRESET`. Testing "the peer dropped the connection" today means holding the server-side `net.Conn` and calling `Close()` yourself, which a test of client-side reconnect logic should not have to do.
3. **Packet duplication.**
4. **Data corruption / bit flips.**

**The question that decides 3 and 4**
netchaos is TCP-shaped — `validateNetwork` accepts only `tcp`/`tcp4`/`tcp6`. Real TCP hides duplication and corruption from the application entirely: the receiver dedupes, and the checksum drops corrupt segments. So an application reading a `net.Conn` never observes either, and injecting them models something no `net.Conn` consumer can experience. The counter-argument is that this line has already been crossed deliberately: real TCP also hides packet *loss* (it retransmits), yet `M0-3` chose the silent-gap model precisely so tests can exercise what the application sees when delivery does not happen. Whether that precedent extends to duplication and corruption, or stops at loss, is the actual decision — and it should be made explicitly rather than by analogy.

**A property worth knowing before deciding**
Adding a fault kind is cheap in determinism terms: streams are derived per `(masterSeed, ordinal, side, kind)` (`rand.go:43-53`), so a new `kind` byte gets an independent stream and **cannot perturb any existing test's latency or loss sequence**. That is by design (`docs/04-api-design.md:212`) and it means new kinds are additive, not a golden-trace break. The draw discipline in `M2-5` is the constraint to respect: a new kind must draw unconditionally on every unit past the partition gate, like the existing two.

**Explicitly out of scope**
- Implementing any of them. This task decides what, if anything, is in scope for `v0.2.0`; each accepted kind then gets its own task.
- Reordering — see [M6-15](#m6-15--reordering-gate-check-only). It is a delivery-order change, not a new kind, and it is gated separately.

**Decision required**
Which of the four, if any, enter `v0.2.0`, and in what order.

**Where the decision gets recorded**
- [06 — Scope & Roadmap](../06-scope-and-roadmap.md) — accepted kinds move into a post-v1 scope section; refused ones join the excluded list *with the reason*, so this review does not have to be redone.
- [05 — Fault Injection](../05-fault-injection.md) for anything accepted.

---

### M6-12 — Decide on per-direction (asymmetric) faults

**Status:** todo
**Decision:** *(not yet made — this is a decision task)*
**Roadmap item:** none
**Depends on:** —
**Blocks:** —

**Objective**
Faults are symmetric by construction. `DialContext` installs the *same* `faultPolicy` value on both directions (`netchaos.go:208-209`), and `newPairKey` sorts its arguments (`partition.go:11-16`) so a pair is inherently undirected. There is no way to express "50 ms client→server, 5 ms server→client" — a common real asymmetry, and the shape of most consumer network links.

The related sub-case is already on record: `M2-4` decided that *"Partition applies to both directions. If a one-directional partition is wanted, that is post-v1 — do not add it here."* This task is where "post-v1" gets looked at, for latency and loss as well as partition.

**Options considered**
1. **Per-direction configuration on the existing options** — e.g. a direction-scoped variant of `WithLatency`/`WithPacketLoss`, and a directed `Partition`. The substrate is already in place: streams are derived per `side` (`rand.go:43-53`), and the two pipes already carry separate policies structurally — they are merely given identical values today.
2. **Leave faults symmetric.** Simpler surface, and symmetric faults cover the failure modes most tests actually assert on (does my retry work, does my timeout fire).

Weighing input, not a decision: the derivation already separates directions, so this is closer to exposing an existing property than adding a mechanism — but it multiplies the option surface, and it overlaps heavily with per-peer-pair scoping ([M6-16](#m6-16--per-peer-pair-scoping-gate-check-only)), which is gated on usage evidence. Deciding this one *before* that gate opens risks designing the narrow case and then reworking it.

**Decision required**
Whether per-direction faults are in scope for `v0.2.0`, and whether the question should simply be merged into `M6-16`'s gate rather than decided separately.

**Where the decision gets recorded**
- [06 — Scope & Roadmap](../06-scope-and-roadmap.md)'s post-v1 section.
- [05 — Fault Injection](../05-fault-injection.md)'s partition section, which currently states the both-directions behaviour as unconditional.

---

### M6-13 — Decide on runtime mutation of latency and loss

**Status:** todo
**Decision:** *(not yet made — this is a decision task)*
**Roadmap item:** none
**Depends on:** —
**Blocks:** —

**Objective**
Partition is dynamic — `Network.Partition`/`Heal` change behaviour on live connections — but latency and loss are fixed at construction. There is no `SetLatency`/`SetPacketLoss`. A test that wants "healthy, then degraded, then healthy" for latency has to build a second `Network`, which means new connections and a reset of every ordinal.

This is an asymmetry in the mental model more than a missing feature: a user who learns that partition can be toggled mid-test reasonably expects the same of the other two faults.

**What it would cost**
`Network.latencyEnabled`, `latencyMin`, `latencyMax`, `lossEnabled` and `lossRate` are written once in `NewNetwork` and read **lock-free** thereafter, and their values are copied into each connection's `faultPolicy` at dial time (`netchaos.go:199-207`). Making them mutable means adding synchronization on a per-unit read path, and deciding whether a change applies to already-established connections (as `Partition` does) or only to subsequent dials. It also raises a determinism question that must be answered before any implementation: a mid-run change to the fault configuration is a new input to the contract in [04](../04-api-design.md#determinism-contract), which currently fixes only *"the order in which `Dial`, `Listen`, `Partition`, and `Heal` are called."* Any setter joins that list.

**Options considered**
1. **Add `SetLatency`/`SetPacketLoss`**, matching `Partition`/`Heal`'s live semantics and extending the determinism contract to cover them.
2. **Leave them construction-time.** Keeps the per-unit read path lock-free and the determinism contract as small as it is.

Weighing input, not a decision: the determinism contract is the library's core promise and its current wording is deliberately narrow; widening it deserves more than an ergonomics argument. Building a second `Network` is a real workaround, if an unsatisfying one.

**Decision required**
Whether the two faults become dynamic, and if so, what the determinism contract says about a mid-run change.

**Where the decision gets recorded**
- [04 — API Design](../04-api-design.md#determinism-contract) — the contract wording, if it widens.
- A `needs-discussion` issue per [07 — Contributing](../07-contributing.md), since it adds exported methods.

---

### M6-14 — Decide whether to export fault observability

**Status:** todo
**Decision:** *(not yet made — this is a decision task)*
**Roadmap item:** the deferral `M2-1` recorded: *"The fault trace is always recorded, not opt-in, and stays unexported — no accessor is in the frozen v1 surface, so exporting one is deferred to a later milestone."*
**Depends on:** —
**Blocks:** —

**Objective**
`traceRecorder` and `faultEvent` (`trace.go`) already record every fault decision on every connection direction, always, and the reproducibility harness relies on them (`reproducibility_test.go`). None of it is reachable from outside the package. So a user can write a test that exercises packet loss but cannot assert *"exactly three writes were dropped"* — only the downstream consequence. This is the deferral `M2-1` named explicitly, now due.

**Options considered**
1. **Export a snapshot accessor** — something like `Network.Trace()` returning a copy of the recorded events. The data already exists and `TestTraceSnapshotIsACopy` already establishes the copy semantics; the cost is that the event shape becomes public API, and the trace format is currently free to change (`M3-3`'s golden files are internal).
2. **Export counters only** — drops, delays, bytes discarded. Much smaller commitment, covers the common assertion ("was anything dropped at all"), and does not freeze the event structure.
3. **Keep it unexported.** The seed plus a reproducible run is already the debugging story the library sells; counters can be inferred from behaviour.

Weighing input, not a decision: option 2 gives most of the value for a fraction of the commitment, and is the easiest to widen later — going from counters to full events is additive, whereas retracting an exported event type is not. Note that whatever is exported becomes a compatibility surface at `v1.0.0`, and `M3-3` deliberately made the trace format high-friction to change.

**Decision required**
Export nothing, counters, or the full trace — and if anything, before or after `v1.0.0`.

**Where the decision gets recorded**
- [04 — API Design](../04-api-design.md#frozen-v1-surface)'s frozen-surface section, if the surface grows.
- `M2-1`'s deferral note in [M2](m2-determinism-and-faults.md#m2-1--seeded-rng-core-with-per-connection-derived-streams), linked to the outcome.

---

### M6-15 — Reordering: gate check only

**Status:** todo
**Decision:** *(not yet made — and not this task's to make)*
**Roadmap item:** [06 — Scope & Roadmap](../06-scope-and-roadmap.md) lists reordering as one of exactly two items "genuinely open for post-v1 consideration," contingent on "real usage evidence rather than a fixed timeline."
**Depends on:** —
**Blocks:** —

**Objective**
Recorded so the review is complete and so this milestone does not read as though it overlooked the most obvious missing fault. **This task does not reopen the question.** `M0-1` decided reordering out of v1, `AGENTS.md` states it is not to be resolved differently or reopened without the maintainer, and [M5](m5-hardening-and-ergonomics.md)'s banner already establishes that the gate is external usage evidence, of which there is currently none.

The mechanic sketch from the original decision is preserved in [06 — Scope & Roadmap](../06-scope-and-roadmap.md) — hold a small window of pending writes and deliver them in a seeded-random permutation — and does not need re-deriving.

**What this task actually is**
A checkpoint: when external usage evidence exists, revisit. Until then, the correct state of this task is `todo` and untouched.

**Two things a future decision will have to deal with**
- Reordering is the one fault that would break `M2-2`'s ordering guarantee, which is currently stated unconditionally in `WithLatency`'s godoc (`latency.go:20-23`) and in [05 — Fault Injection](../05-fault-injection.md). Those are user-visible promises, not internal notes.
- The pending-queue design (`latency.go`) is release-ordered *by construction* — `releaseAt = max(previous, now+drawn)`. Reordering is not a matter of relaxing a check; it changes the data structure's invariant.

**Decision required**
None now. The maintainer's, when the gate opens.

**Where the decision gets recorded**
- [06 — Scope & Roadmap](../06-scope-and-roadmap.md), and `M0-1` in [M0](m0-decisions-and-foundations.md), which is the authoritative record of the original decision.

---

### M6-16 — Per-peer-pair scoping: gate check only

**Status:** todo
**Decision:** *(not yet made — gated)*
**Roadmap item:** the second of [06 — Scope & Roadmap](../06-scope-and-roadmap.md)'s two "genuinely open for post-v1 consideration" items.
**Depends on:** —
**Blocks:** —

**Objective**
Latency and packet loss apply globally to every connection in a `Network` (`M0-2`'s decision; `netchaos.go:36-40`), while partition is pair-scoped. A test with three peers cannot make one link slow and leave the others fast. [04 — API Design](../04-api-design.md) already calls per-pair scoping "a plausible post-v1 refinement once real usage patterns are clearer."

As with `M6-15`, this is recorded for completeness and **is gated on the same external-usage evidence**, which does not exist yet. It is listed separately because, unlike reordering, `AGENTS.md` places no additional restriction on discussing it — and because [M6-12](#m6-12--decide-on-per-direction-asymmetric-faults) overlaps it directly. If both are eventually taken up, they should be designed together: per-pair and per-direction scoping are two axes of the same configuration model, and shipping one without considering the other is how an option surface becomes incoherent.

**Decision required**
None now.

**Where the decision gets recorded**
- [06 — Scope & Roadmap](../06-scope-and-roadmap.md); `M0-2` in [M0](m0-decisions-and-foundations.md) holds the original global-scoping decision.

---

### M6-17 — Decide whether the pipe bound and listener backlog become configurable

**Status:** todo
**Decision:** *(not yet made — this is a decision task)*
**Roadmap item:** none
**Depends on:** —
**Blocks:** —

**Objective**
Two constants shape observable behaviour and neither can be changed by a user: `defaultPipeBound = 64 * 1024` (`pipe.go:14`) and `listenerBacklog = 128` (`listener.go:12`). The bound determines when a writer blocks on back-pressure; the backlog determines when a dial fails with `ErrBacklogFull`. Both are reachable *only* from in-package tests — `newConnPairWithBound` (`conn.go:64`) exists precisely because the bound had to be varied to test back-pressure, which is direct evidence that the value matters to at least one kind of test.

A user wanting to test their own back-pressure handling, or a listener under accept-queue exhaustion, has to work around a value they cannot see or set.

**Options considered**
1. **Add `WithPipeBound` / `WithListenerBacklog` options.** Small, orthogonal to everything else, and validated by the existing `validate()` pass in `NewNetwork` (`options.go:49-59`) with the same panic-on-invalid convention. Costs two more names on a surface `M5-2` is about to review for being too large.
2. **Expose only the bound.** Back-pressure is the one users are more likely to want to exercise; the backlog is closer to an implementation detail.
3. **Leave both fixed** and document the values, so at least the behaviour is predictable from the docs rather than only from the source.

Weighing input, not a decision: option 3 has a real argument behind it — the values are chosen to be realistic (`listenerBacklog`'s comment calls it "analogous to a conventional `listen(2)` backlog default"), and every option added now is one `M5-2` has to weigh before `v1.0.0`. But option 1 is genuinely cheap and the in-package test constructor is evidence the need is real.

**Gate resolved: this task does not wait for `M5-2`.** M5-2 has concluded (its finding F5), and the premise behind the "wait" option did not survive it. The concern was that two more names would burden a surface M5-2 might find too large; M5-2 found the opposite problem — the surface's weakness is a *missing* capability on `Dial`, not excess breadth. Option count is not the binding constraint, so decide this one on its own merits whenever it is picked up.

**Decision required**
Which of the three. (No longer gated on `M5-2`.)

**Where the decision gets recorded**
- [04 — API Design](../04-api-design.md#functional-options)'s options list, if either lands.
- The godoc on whichever constants stay fixed, which should at minimum state the value.

---

## Confirmed exclusions

The review also considered the following and confirmed they stay out, so a future review does not re-derive them. Each fails [06 — Scope & Roadmap](../06-scope-and-roadmap.md)'s first rubric question — *does it operate at the connection/byte-stream layer, or does it require protocol or subsystem knowledge?* — and each is already on the excluded list in [06](../06-scope-and-roadmap.md#explicitly-out-of-scope-for-v1):

- **DNS / resolver faults.** There is no resolution layer to fault: `peerName` is the identity function (`addr.go:32`), so address→peer is direct by design. Injecting DNS failures means inventing a resolver first.
- **TLS-level faults** (handshake failure, certificate errors, truncation). Note that TLS itself needs nothing from netchaos — `crypto/tls.Client`/`Server` wrap any `net.Conn`, so TLS over a netchaos conn already works. It is *fault injection at the TLS layer* that is out.
- **Protocol-aware faults** (HTTP status injection, gRPC status codes). Excluded on principle, not sequencing.
- **UDP / `net.PacketConn`.** Rejected in code (`addr.go:35-44`) and in [06](../06-scope-and-roadmap.md): datagram semantics differ enough to be a distinct design effort. See [M6-3](#m6-3--extend-the-udp-rejection-message-to-udp4-and-udp6) for the message quality of that rejection, which is a separate matter from the exclusion itself.
- **Disk faults, full syscall simulation, goroutine-scheduling control.** [01 — Vision](../01-vision.md) excludes all three; the syscall case is incompatible with the no-rewrite adoption model the library is built on.
