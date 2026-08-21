# M3 — `testing/synctest` integration & reproducibility

> See the [task index](README.md) for the milestone map and conventions.

**Covers v1 checklist item:** *Integration with `testing/synctest` for virtual time* ([06 — Scope & Roadmap](../06-scope-and-roadmap.md)).

**This milestone is mostly tests, not new production code.** [03 — Architecture](../03-architecture.md#composing-with-testingsynctest) is explicit that netchaos should *ride on* synctest rather than duplicate it: latency uses standard timer primitives that synctest already virtualizes, so there is no clock abstraction and no bubble-detection logic to build. If M1 and M2 respected the durable-blocking constraint throughout, integration is largely a matter of proving it. Its real content is:

1. **M3-1** — verifying every blocking path is bubble-compatible, and fixing whatever is not.
2. **M3-3** — the reproducibility harness, which is what makes "reproduce a failure from a seed" a tested guarantee rather than an aspiration.

Budget M3 as verification plus one harness, not as a build.

**The API, confirmed against Go 1.25+:** `synctest.Test(t *testing.T, f func(*testing.T))` and `synctest.Wait()`. The example in [04 — API Design](../04-api-design.md#full-usage-sketch) uses this form and is correct as written. The older experimental `synctest.Run` from Go 1.24 must not appear anywhere. `go.mod` declares `go 1.25` and CI covers 1.25 and 1.26, so the stable API is available on both.

---

### M3-1 — Verify bubble-compatibility of every blocking path

**Status:** done
**Roadmap item:** *Integration with `testing/synctest` for virtual time*
**Depends on:** M1-8, M2-5
**Blocks:** M3-2, M3-3, M4-1

**Objective**
Prove that every place netchaos can block a goroutine is **durably blocking**, so a bubble reaches idle and virtual time advances instead of the bubble panicking with a deadlock.

**The rule being verified** (from the `testing/synctest` package documentation): a goroutine is durably blocked when it can only be unblocked by another goroutine *in the same bubble*. Durably blocking: a send/receive on a channel created inside the bubble; a select where every case is such a channel; `sync.Cond.Wait`; `sync.WaitGroup.Wait` when `Add` was called inside the bubble; `time.Sleep`. **Not** durably blocking: locking a `sync.Mutex` or `sync.RWMutex`, I/O on a real socket, syscalls. When every goroutine in a bubble is durably blocked, time advances to the next timer; if nothing can ever unblock, `synctest.Test` panics with a deadlock.

The consequence for netchaos: a `Read` waiting for data, an `Accept` waiting for a connection, or a `Write` waiting for buffer space must park on a channel or `sync.Cond` — **never on a mutex**. A mutex-blocked goroutine keeps the bubble non-idle, so virtual time never advances and the test hangs or deadlock-panics. Short mutex-guarded critical sections that always make progress are fine; a mutex must not be the thing a blocked call waits *on*.

**Scope**
- Enumerate every blocking point: `pipe` read-when-empty, `pipe` write-when-full, `listener.Accept` on an empty queue, latency delivery wait, deadline wait, `Close` paths that wait for in-flight work.
- For each, confirm the primitive is durably blocking and write a test that demonstrates it.
- Confirm bubble-created channels and timers are not operated on from outside their bubble — per the synctest docs this panics. This constrains the usage model: a `Network` must be created *inside* the bubble that uses it. Verify this and record it as a documentation obligation for M4-1.
- Confirm no internal goroutine outlives its bubble — `synctest.Test` waits for all bubble goroutines to exit before returning, so a leaked goroutine hangs the test.
- Check `sync.WaitGroup` usage, if any: the synctest docs note a `WaitGroup` in a *package variable* cannot associate with a bubble and its `Wait` may not be durably blocking. Avoid package-level `WaitGroup` values entirely.
- Fix anything found. Fixes land in the M1/M2 files, not in new ones.

**Files**
- `synctest_test.go` (new) — the compatibility suite
- Fixes in `pipe.go`, `conn.go`, `listener.go`, `latency.go` as needed

**Acceptance criteria**
- [x] Every blocking point is enumerated in the test file, one test each.
- [x] No blocked netchaos call parks on a mutex — verified by review and by a bubble-idleness test per blocking point.
- [x] A test blocking on `Read`, then calling `synctest.Wait()`, observes the bubble reach idle.
- [x] Same for `Accept`.
- [x] A full dial → write → read → close cycle completes inside `synctest.Test` with no deadlock panic.
- [x] No goroutine outlives the bubble.
- [x] No package-level `sync.WaitGroup` exists in the package.
- [x] The "construct the `Network` inside the bubble" requirement is confirmed and recorded for M4-1.
- [x] Tests pass on both Go 1.25 and 1.26 in CI.

**Decision:** the "construct the `Network` inside the bubble" requirement was already documented at `netchaos.go`'s `Network` godoc before M3-1 began; M3-1 verified it by test (every test in `synctest_test.go` constructs its `Network` inside the enclosing `synctest.Test`) rather than rewriting the paragraph, and records the M4-1 obligation to surface it in user-facing docs.

**Decision:** the leak criterion is carried by `TestCloseWithInFlightWorkInBubble`, not by goroutine counting. `time.AfterFunc` spawns no goroutine until it fires, so `runtime.NumGoroutine` cannot observe an unstopped latency timer; `TestNoLatencyTimerLeaks` is retained as a backstop and its docstring says so explicitly.

**Tests**
- `TestBubbleIdleOnBlockedRead`, `TestBubbleIdleOnBlockedAccept`, `TestBubbleIdleOnBlockedWrite`
- `TestBubbleIdleOnLatencyDelivery`, `TestBubbleIdleOnDeadlineWait`, `TestBubbleIdleOnPartitionDialWait`
- `TestCloseWithInFlightWorkInBubble`, `TestFullCycleInBubble`
- `TestNoGoroutinesOutliveBubble`, `TestNoLatencyTimerLeaks`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M3-2 — Virtual-time latency tests

**Status:** done
**Roadmap item:** *Integration with `testing/synctest` for virtual time*
**Depends on:** M2-2, M3-1
**Blocks:** M3-4

**Objective**
Prove the value proposition in [03 — Architecture](../03-architecture.md#composing-with-testingsynctest): a test configured with 50–150 ms of injected latency exercises the client's waiting behaviour **without spending 50–150 ms of real time**.

**Scope**
- Tests inside `synctest.Test` asserting that `time.Since(start)` across a latency-injected round trip equals the configured virtual duration exactly — the bubble clock is exact, so this is an equality assertion, not a tolerance one.
- A test asserting the whole suite's real wall-clock cost stays negligible even with large configured latencies (e.g. 30 s of virtual latency completing in microseconds of real time).
- Tests combining injected latency with `context.WithTimeout` and with conn deadlines, since propagating a deadline through a slow call is the scenario [05 — Fault Injection](../05-fault-injection.md#latency) names.
- A test with several connections at different latencies in one bubble, asserting each advances correctly relative to the others.
- Correct use of `synctest.Wait()` where a test needs all other bubble goroutines to reach a durable block before asserting.
- Out of scope: changing latency's implementation — if these tests need production changes, that is an M2-2 bug, fixed there.

**Files**
- `latency_synctest_test.go` (new)

**Acceptance criteria**
- [x] A round trip with `WithLatency(d, d)` advances the bubble clock by exactly `d`.
- [x] A test with 30 s of configured virtual latency completes in negligible real time.
- [x] A `context.WithTimeout` shorter than the injected latency cancels; longer, and the call succeeds.
- [x] A conn read deadline shorter than the injected latency yields `os.ErrDeadlineExceeded`.
- [x] Multiple connections at different latencies interleave correctly in one bubble.
- [x] Tests pass on Go 1.25 and 1.26.

**Decision:** "advances the bubble clock by exactly `d`" is asserted two ways. For a one-way delivery this criterion was already satisfied by `TestLatencyFixed` (`latency_test.go`, predates M3) — not duplicated here. Latency is per-direction, so a full round trip advances `2d`; that reading is new and covered by `TestRoundTripAdvancesTwiceLatency`.

**Decision:** the conn-read-deadline-under-latency criterion was already fully covered, including the longer-deadline success case, by `TestLatencyVsReadDeadline` (`latency_test.go`, predates M3) — not duplicated here.

**Decision:** the wall-clock cost assertion is taken **outside** the bubble (`TestLatencyCostsNoRealTime`) — inside one, `time.Since` reports virtual time and cannot demonstrate real-time cheapness. The bound is loose (<1s real for 30s virtual) because the claim is "virtual time is not real time," not a performance target.

**Tests**
- `TestLatencyCostsNoRealTime`, `TestRoundTripAdvancesTwiceLatency`
- `TestContextTimeoutUnderLatency`
- `TestMultipleLatenciesInOneBubble`
- (already covered pre-M3: `TestLatencyFixed`, `TestLatencyVsReadDeadline`, both in `latency_test.go`)
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M3-3 — Reproducibility harness

**Status:** todo
**Roadmap item:** *Seeded randomness for reproducible failure scenarios* + *Integration with `testing/synctest`* ([06](../06-scope-and-roadmap.md))
**Depends on:** M2-1, M2-5, M3-1
**Blocks:** M3-4

**Objective**
Turn the determinism contract in [04 — API Design](../04-api-design.md#determinism-contract) into an enforced, regression-tested property. Without this harness, determinism is a claim; with it, any change that breaks reproducibility fails CI. This is the milestone's most valuable single artifact.

**Scope**
- A harness that runs a scenario against a `Network` and captures its M2-1 fault trace in a canonical, comparable form.
- Assert: the same seed and the same scenario produce identical traces across repeated runs in one process.
- Assert: the trace is identical when the scenario uses **concurrent** goroutines — the property that a naive shared-RNG implementation silently fails, and the reason M0-4 exists. Run the scenario N times with real concurrency and assert all N traces match.
- Assert: different seeds produce different traces, so the harness is actually sensitive and is not passing vacuously.
- **Golden traces** for cross-machine stability: check a small set of traces into the repo for fixed seeds and fixed scenarios, and assert against them. CI runs Go 1.25 and 1.26 on `ubuntu-latest`, so a golden file catches a change in the derivation or in Go's RNG behaviour. Note the trade-off: golden traces mean any deliberate change to the fault sequence requires regenerating them, which is the intended friction — the contract says the sequence is stable, so changing it *is* a breaking change.
- A documented workflow for the end-user path this exists to serve: a test fails → the seed is reported → re-running with that seed reproduces the identical failure. If M1-5 made the default seed random, the reporting half of that loop must be demonstrated here.
- Out of scope: changing the derivation (M2-1).

**Files**
- `reproducibility_test.go` (new) — the harness and its tests
- `testdata/traces/*.golden` (new)

**Acceptance criteria**
- [ ] Repeated runs of the same scenario with the same seed produce identical traces.
- [ ] Identical traces hold for a scenario with N concurrent goroutines, across N repetitions.
- [ ] Different seeds produce different traces (sensitivity check).
- [ ] Golden traces are checked in and asserted against, on both Go 1.25 and 1.26.
- [ ] The regeneration procedure for golden files is documented in the test file (e.g. a `-update` flag).
- [ ] The failure → seed → replay workflow is demonstrated by a test.
- [ ] The harness exercises all three faults composed, not just one.
- [ ] `-race` clean; the concurrent case runs under `-race` specifically.

**Tests**
- `TestSameSeedSameTrace`, `TestSameSeedSameTraceUnderConcurrency`
- `TestDifferentSeedDifferentTrace`
- `TestGoldenTraces`
- `TestReplayFromReportedSeed`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M3-4 — End-to-end scenario tests

**Status:** todo
**Roadmap item:** validates all six v1 checklist items together
**Depends on:** M3-2, M3-3
**Blocks:** M4-2

**Objective**
Test the three scenarios [05 — Fault Injection](../05-fault-injection.md) names as the *reason each fault exists*. These are the acceptance tests for v1 as a whole: if a realistic client cannot be tested this way, the library has not delivered on its pitch regardless of unit-test coverage.

**Scope**
Three scenarios, each with a small in-test client implementing the behaviour under test — deliberately minimal, since the point is to exercise netchaos, not to ship a client library:

1. **Retry under packet loss** ([05](../05-fault-injection.md#packet-loss)) — a client with a retry loop against a moderate loss rate succeeds within its retry budget, and returns the correct error once the budget is exhausted at a punishing rate. This is the README's headline example.
2. **Timeout and backoff under latency** ([05](../05-fault-injection.md#latency)) — a client with a context deadline correctly times out when injected latency exceeds it, retries with backoff, and succeeds when latency is within budget. All in virtual time.
3. **Circuit breaker across partition and heal** ([05](../05-fault-injection.md#partition)) — a client with a circuit breaker sees a partition open the circuit, `Heal` restores the link, and the breaker closes and recovers **without a process restart**, which [05](../05-fault-injection.md#partition) calls out specifically.

- Every scenario runs inside `synctest.Test`, uses a fixed seed, and asserts on virtual time.
- Each scenario is also run through the M3-3 harness to confirm it reproduces.
- Structure these so M4-2 can adapt them into runnable `Example` functions with minimal rewriting.
- Also verify the multi-peer topology from [03 — Architecture](../03-architecture.md#single-process-multi-peer-topology): one `Network` with `client`, `server-a` and `server-b`, where `server-b` is partitioned and `server-a` is not, exercising failover.
- Out of scope: godoc examples (M4-2).

**Files**
- `scenario_test.go` (new)

**Acceptance criteria**
- [ ] The retry-under-loss scenario passes and fails for the right reasons at both a moderate and a punishing loss rate.
- [ ] The timeout-under-latency scenario asserts on virtual elapsed time.
- [ ] The circuit-breaker scenario completes the full open → heal → close cycle on the same `Network` with no restart and no re-construction.
- [ ] A three-peer failover scenario passes with one peer partitioned and the other reachable.
- [ ] Every scenario is deterministic under M3-3.
- [ ] The whole scenario suite runs in negligible real wall-clock time despite large configured latencies.
- [ ] `-race` clean.

**Tests**
- `TestScenarioRetryUnderPacketLoss`, `TestScenarioRetryExhaustsBudget`
- `TestScenarioTimeoutAndBackoffUnderLatency`
- `TestScenarioCircuitBreakerPartitionHeal`
- `TestScenarioFailoverBetweenPeers`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`
