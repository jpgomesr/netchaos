# M8 — v1.0.0 readiness

> See the [task index](README.md) for the milestone map and conventions.

**Covers:** no v1 checklist item — v1 shipped and is closed. This milestone closes what's left before `v1.0.0` can tag: the `v0.2.0` surface's own ergonomics review ([M5-2](m5-hardening-and-ergonomics.md#m5-2--api-ergonomics-review-before-v100) closed before that surface existed, and [#75](https://github.com/jpgomesr/netchaos/issues/75) tracks the gap), the four bugs against real-`net.Conn` fidelity filed since `v0.2.0`, and two tooling gaps.

**Tagging `v1.0.0` is not part of this milestone's scope.** The maintainer has said the tag waits for real usage evidence ("tração"), independent of whether the surface is ready. This milestone's job is only to make the surface *tag-ready*: every decision that would otherwise be a breaking change after `v1.0.0` gets made now, while it is still additive or free to change.

**Issue map:**

| Task | Issue | Kind |
|---|---|---|
| `M8-1` | [#82](https://github.com/jpgomesr/netchaos/issues/82) | bug |
| `M8-2` | [#76](https://github.com/jpgomesr/netchaos/issues/76) | bug |
| `M8-3` | [#80](https://github.com/jpgomesr/netchaos/issues/80) | bug |
| `M8-4` | [#81](https://github.com/jpgomesr/netchaos/issues/81) | bug |
| `M8-5` | [#79](https://github.com/jpgomesr/netchaos/issues/79) | tooling |
| `M8-6` | [#87](https://github.com/jpgomesr/netchaos/issues/87) | tooling |
| `M8-7` | [#75](https://github.com/jpgomesr/netchaos/issues/75) | review — this file's "Review outcome" section |

`M8-1` through `M8-6` are independent, single-PR, test-first fixes with no exported-surface impact (`M8-2`'s panic-message change touches a pinned test string, not a signature). `M8-7` is the review below; **it recommends, it does not decide** — the same posture [M5-2](m5-hardening-and-ergonomics.md#m5-2--api-ergonomics-review-before-v100) took toward F2/F4, per [07 — Contributing](../07-contributing.md)'s issue-first rule for any exported-surface change. Every recommendation below that changes the surface stays a `needs-discussion` issue until the maintainer decides it; nothing in this file closes an issue or lands code on its own authority.

---

## M8-7 — API ergonomics review of the `v0.2.0` surface (the `v1.0.0` gate)

**Status:** done — decisions recorded, see "Review outcome" below
**Issue:** [#75](https://github.com/jpgomesr/netchaos/issues/75)
**Depends on:** —
**Blocks:** `v1.0.0` (per `docs/04`'s and `AGENTS.md`'s existing gate note); does not block a `v0.3.0`-style release of `M8-1`..`M8-6`

**Objective**

`M5-2` reviewed the `v0.1.0` frozen surface before it had external users. The `v0.2.0` surface — `DialerFor`, `SetLatency`/`SetPacketLoss`, `Reset`, `Trace`/`FaultEvent`/`Side`, `WithBandwidth`/`WithDuplication`/`WithCorruption`, `WithPipeBound`/`WithListenerBacklog`, and the host:port address shape — never had that pass; `#75` exists to make that gap trackable. This task is that pass, done the same way: read every design-feedback and enhancement issue that touches the frozen surface, and produce a recommendation per finding, in descending order of how much each matters — not a decision. [`docs/07`](../07-contributing.md)'s issue-first rule means any exported-signature change stays with the maintainer.

**Findings**

### F1 — `docs/04`'s status banner still says "not expected to [change] for `v0.1.0`" (fixed here)

Prose only, no signature moves — the same class `M5-2`'s F1 was, and landed the same way (directly, not as an issue). The banner predates `v0.2.0` and reads as if the surface below it is still the `v0.1.0`-only set. Corrected in this PR to say the `v0.1.0` surface is unchanged and stable, and that the `v0.2.0` additions are what this review (F2 through F7 below) is about.

### F2 — `Partition`/`Heal`/`Reset` accept a self-pair or an empty peer name silently, unlike `WithPartition` *(issue [#83](https://github.com/jpgomesr/netchaos/issues/83))*

`WithPartition`'s construction-time validation (`validatePartitionPair`, `partition.go`) panics on `peerA == ""`, `peerB == ""`, or `peerA == peerB`. The three runtime equivalents apply none of it — `n.Partition("a", "a")` and `n.Partition("", "b")` both return normally and store the pair as if valid.

**Recommendation: make the runtime methods call `validatePartitionPair` too, panicking on the same two conditions.** This is the cheapest kind of fix available before `v1.0.0`: it does not change any signature, only turns two silently-accepted misuse cases into the same panic `WithPartition` already gives a programmer for the identical mistake at construction time. The asymmetry has no design rationale on record — nothing in `docs/04` or `docs/05` describes a self-partition or an empty-name partition as meaningful traffic state, which is what the issue's own text already suspected. Not a `needs-discussion` item in the usual sense (it changes behavior, not surface — an input that used to be silently accepted now panics), but flagged for the maintainer's sign-off before landing since it is a behavior change on three already-shipped methods.

### F3 — No half-close (`CloseRead`/`CloseWrite`) *(issue [#84](https://github.com/jpgomesr/netchaos/issues/84))*

Documented, deliberate: `conn.go` matches `net.Pipe`'s full-teardown `Close`, not a real socket's independent half-close. The issue is filed as design feedback, not asking for an implementation, and is right to frame it that way — this is exactly the kind of thing `docs/06`'s two-tier deferred list exists for.

**Recommendation: record it as deferred, not implement it now.** Add a line to [`docs/06`](../06-scope-and-roadmap.md#explicitly-out-of-scope-for-v1)'s "genuinely open for post-v1 consideration" list, alongside reordering and per-peer-pair scoping, with the same gating language: contingent on real evidence that code under test actually type-asserts `CloseWrite`/`CloseRead`, not a fixed timeline. Adding half-close later is additive (a new interface a `*conn` can start satisfying), so nothing is lost by waiting, and no `v1.0.0` freeze is at risk either way — the risk this review exists to catch (an addition that's breaking *after* the freeze) does not apply here.

### F4 — `FaultEvent` can't diagnose a corruption/duplication failure or attribute a `Reset` *(issue [#78](https://github.com/jpgomesr/netchaos/issues/78))*

Two different asks bundled in one issue, worth splitting:

- **Payload size and corruption site** (byte/bit index) are already computed internally (`faults.go`'s `corruptionSite`) and simply discarded. Exposing them is a pure additive struct-literal change to `FaultEvent` (callers using keyed literals, which the existing `FaultEvent` godoc already recommends, are unaffected).
- **Attributing a `Reset` in the trace** needs new plumbing — `Network.Reset` is an imperative action evaluated nowhere near the per-unit evaluator, by design (`reset.go`), so recording it in the same trace changes the trace's shape.

**Recommendation: split them.** The size/corruption-site fields are cheap and safe enough to land now as a `needs-discussion` issue update (still the maintainer's call, since `FaultEvent` is a `v1.0.0` compatibility surface per `M6-14`, but the cost is small and well-understood). The `Reset` attribution should move to `docs/06`'s deferred list next to F3 — it is real plumbing work, not a field add, and does not need to block anything.

### F5 — `DialerFor` has no way to bound a wait on a partitioned peer *(issue [#86](https://github.com/jpgomesr/netchaos/issues/86))*

`DialerFor`'s own godoc already names this gap: a dialer built with `DialerFor` is partition-targetable, but its dial always uses `context.Background()`, so there is no way to make a dial against a partitioned peer fail fast instead of hanging until `Heal`. The escape hatch the godoc suggests — use `DialContext` with `WithPeerName` and a deadline instead — defeats the reason `DialerFor` exists (a plain `net.Dial`-shaped function for a client constructor with no context parameter to plumb a deadline through).

**Recommendation: add a variadic option**, e.g. `DialerFor(name string, opts ...DialerOption)` with `WithDialTimeout(d time.Duration)`, building a `context.WithTimeout` per dial internally rather than `context.Background()`. Additive (existing `DialerFor(name)` calls keep compiling), and it closes a real first-impression risk: a test exercising the headline partition feature through the drop-in dialer path hangs until `go test`'s own timeout today, which is a rough thing for a new adopter to hit. Still an exported-surface change, so it is `needs-discussion`, not landed here.

### F6 — Runtime setters exist for two of five fault kinds *(issue [#85](https://github.com/jpgomesr/netchaos/issues/85))*

`SetLatency`/`SetPacketLoss` (`M7-4`) let a test mutate an already-established connection's policy; `WithBandwidth`, `WithDuplication`, `WithCorruption` have no runtime counterpart. `#85` is filed `needs-discussion` by its own author for a specific reason worth preserving: `WithBandwidth` draws nothing (deterministic function of size and rate, not a Bernoulli trial), so a `SetBandwidth`'s interaction with the pipe's serialization clock needs its own look, not an assumption that it mirrors `SetLatency`.

**Recommendation: defer, record in `docs/06`.** This is additive either way (three new methods), so nothing about `v1.0.0`'s freeze forces a decision now — the asymmetry can ship as documented, intentional scope (v1's global-scoping precedent already accepts "some knobs are more built-out than others" without it reading as a defect) until real usage shows the gap matters.

### F7 — `Network.Trace` has no disable or clear *(issue [#77](https://github.com/jpgomesr/netchaos/issues/77))*

Additive (`WithTrace(bool)` and/or `Network.ClearTrace()`), and not urgent — the failure mode is unbounded memory growth in a long-running/high-throughput test, which is a real but narrow case.

**Recommendation: defer, record in `docs/06`.** Same reasoning as F6: nothing here becomes harder or more breaking by waiting, so it does not need to be resolved before a `v1.0.0` freeze the way F1–F2 do.

**Not examined, deliberately**

Reordering and per-peer-pair scoping stay out of this review's scope, unchanged from `M5-2`'s own exclusion — both remain gated on real usage evidence, and `AGENTS.md` bars resolving reordering without the maintainer.

**Decisions required (maintainer)**

1. F2 (`#83`): panic on self-pair/empty-name for `Partition`/`Heal`/`Reset`, yes or no.
2. F3 (`#84`): confirm deferral, add the `docs/06` line.
3. F4 (`#78`): split as recommended — land the size/corruption-site fields, defer `Reset` attribution.
4. F5 (`#86`): add `DialerOption`/`WithDialTimeout`, yes or no, and which shape.
5. F6 (`#85`), F7 (`#77`): confirm deferral, add the `docs/06` lines.

**Where the decision gets recorded**

Each decision lands as a comment or label change on its own issue, and a "Review outcome" addendum to this section once decided — the same pattern `M5-2` used. No issue is closed by this document; closing happens on the PR that actually implements or explicitly defers each one.

---

## Review outcome

Decided by the maintainer, 2026-09-15. In the same order as the findings above.

### F2 (`#83`) — decided: panic

Accepted as recommended: `Partition`, `Heal`, and `Reset` are to validate the same way `WithPartition` already does, panicking on an empty peer name or a self-pair. Not yet implemented — tracked as [`M9-1`](m9-v1-surface-additions.md#m9-1--83-validate-partitionhealreset-the-way-withpartition-already-does).

### F3 (`#84`) — decided: defer

Accepted as recommended. Half-close moves to [`docs/06`](../06-scope-and-roadmap.md#explicitly-out-of-scope-for-v1)'s "genuinely open for post-v1 consideration" list. `#84` is closed by the PR that adds that line — the issue asked only for a decision on record, not an implementation.

### F4 (`#78`) — decided: split, as recommended

Payload size and the corruption byte/bit index are accepted — both are already computed in `faults.go` and simply discarded today, so exposing them is a pure additive change to `FaultEvent`. `Reset` attribution is deferred to [`docs/06`](../06-scope-and-roadmap.md#explicitly-out-of-scope-for-v1): it needs new plumbing outside the per-unit evaluator (`reset.go`), not a field add, so it does not belong in the same PR as the two cheap fields. Tracked for implementation as [`M9-3`](m9-v1-surface-additions.md#m9-3--78-faultevent-gains-payload-size-and-corruption-site).

### F5 (`#86`) — decided: add `DialerOption`/`WithDialTimeout`

Accepted as recommended, and the specific shape too: `DialerFor(name string, opts ...DialerOption)` plus `WithDialTimeout(d time.Duration) DialerOption`, matching the functional-options pattern the rest of the exported surface already uses (`WithLatency`, `WithPartition`, etc.). Without `WithDialTimeout`, `DialerFor` keeps today's behaviour — an unbounded wait against a partitioned peer — so this is purely additive; no existing `DialerFor(name)` call needs to change. Tracked for implementation as [`M9-2`](m9-v1-surface-additions.md#m9-2--86-dialerfor-gains-a-bounded-wait).

### F6 (`#85`) — decided: split, diverging from the recommendation

F6 recommended deferring all three setters together. The maintainer instead split it: **`SetDuplication` and `SetCorruption` are accepted now; `SetBandwidth` is deferred.** The reason the split holds is the same one `#85`'s own author flagged and F6 restated — duplication and corruption are both Bernoulli draws that mirror `SetPacketLoss`'s existing shape exactly (same validation, same live semantics, same draw discipline), while bandwidth draws nothing and its interaction with the pipe's serialization clock (`pipe.busyUntil`) needs its own look before a setter can safely reach it. `SetBandwidth` moves to [`docs/06`](../06-scope-and-roadmap.md#explicitly-out-of-scope-for-v1)'s deferred list; the other two are tracked for implementation as [`M9-4`](m9-v1-surface-additions.md#m9-4--85-partial-setduplication-and-setcorruption).

### F7 (`#77`) — decided: defer

Accepted as recommended. `Network.Trace`'s disable/clear moves to [`docs/06`](../06-scope-and-roadmap.md#explicitly-out-of-scope-for-v1). `#77` is closed by the PR that adds that line.

---

### M8-1 — `#82`: `SetDeadline`/`SetReadDeadline`/`SetWriteDeadline` on a closed conn return `nil`

**Status:** done — [#92](https://github.com/jpgomesr/netchaos/pull/92)
**Issue:** [#82](https://github.com/jpgomesr/netchaos/issues/82)

Real `net.Conn` returns a `*net.OpError` (`Op: "set"`) satisfying `errors.Is(err, net.ErrClosed)` once `Close` has run; netchaos's `conn` forwards straight to `deadline.set` with no closed-check. Fix: check `c.closed` (non-blocking select, matching the existing pattern in `Read`/`Write`) before calling `c.rd.set`/`c.wd.set`, returning `c.opError("set", net.ErrClosed)` — reusing the existing `opError` helper rather than hand-rolling the `*net.OpError`. Test-first in `conn_test.go`.

### M8-2 — `#76`: `SetLatency`/`SetPacketLoss` panic naming the wrong identifier

**Status:** done — [#93](https://github.com/jpgomesr/netchaos/pull/93)
**Issue:** [#76](https://github.com/jpgomesr/netchaos/issues/76)

`validateLatencyRange`/`validateLossRate` are shared by the `Option` constructors (via `networkConfig.validate()`) and the setters, and hardcode `WithLatency`/`WithPacketLoss` in their panic text regardless of caller. Fix: thread the caller's own name through (a `caller string` parameter, since `validate()`'s batched pass has no other way to know which identifier invoked it), and update `setters_test.go:108-111` to expect `"SetLatency"`/`"SetPacketLoss"` — flip that test first and watch it fail before changing the message.

### M8-3 — `#80`: `serializationDelay` overflows above ~9.22 GB/s combined with a similarly large single `Write`

**Status:** done — [#94](https://github.com/jpgomesr/netchaos/pull/94)
**Issue:** [#80](https://github.com/jpgomesr/netchaos/issues/80)

`time.Duration(rem)*time.Second` wraps once `rem` (always `< bytesPerSecond`) exceeds `math.MaxInt64/1e9`. Fix with exact integer arithmetic, not a `float64` intermediate — determinism is the whole pitch, and a float would trade an overflow bug for a precision one. `rem < bytesPerSecond` always, so `rem*1e9` fits in 128 bits: `bits.Mul64` then `bits.Div64` (or an equivalent big-multiply-then-divide) computes the nanosecond remainder term exactly. Before landing, check whether `time.Duration(sec)*time.Second` (the whole-seconds term) can *also* overflow for a low `bytesPerSecond` and a multi-GB write — `size/bytesPerSecond` is unbounded above, so if it can, the fix needs to cover both terms or the issue only half-closes.

### M8-4 — `#81`: synthesized listener ports exceed the 16-bit range after ~57,536 listeners

**Status:** done — [#95](https://github.com/jpgomesr/netchaos/pull/95)
**Issue:** [#81](https://github.com/jpgomesr/netchaos/issues/81)

`nextListenPort` increments unbounded; `ephemeralPort` (`addr.go:139-147`) already wraps via modulo into its range. Confirmed safe to mirror that here: `n.listeners` is keyed by peer name alone (`netchaos.go`), not by `host:port`, so two listeners sharing a wrapped port cannot collide on `ErrAddressInUse` the way they would if uniqueness were port-based. Fix: apply the same modulo-into-range pattern `ephemeralPort` uses to `nextListenPort`'s advance. Test directly against the port-synthesis logic (seed the counter near the boundary), not via 57,536 real `Listen` calls.

### M8-5 — `#79`: CI has no coverage reporting and never runs the fuzz targets

**Status:** done — [#96](https://github.com/jpgomesr/netchaos/pull/96)
**Issue:** [#79](https://github.com/jpgomesr/netchaos/issues/79)

Add a coverage step (`go test -coverprofile=...`, surfaced via `go tool cover -func`, no hard gate yet) and a short fuzz run (`go test -run=^$ -fuzz=FuzzPipeAccounting -fuzztime=20s ./...`) to `.github/workflows/ci.yml`, on one Go version only to avoid tripling CI time.

### M8-6 — `#87`: enable `errorlint`, `revive`, `misspell`, `godot` in `.golangci.yml`

**Status:** done — [#97](https://github.com/jpgomesr/netchaos/pull/97)
**Issue:** [#87](https://github.com/jpgomesr/netchaos/issues/87)

One PR: add the four linters, run `golangci-lint run`, and fix whatever findings surface (expect most volume from `godot`/`revive` on existing comments). `errorlint` directly enforces `errors.go`'s own documented `errors.Is`-only convention.

---

## What comes after M8

`M8-1` through `M8-6` are merged and `M8-7`'s five decisions are recorded above. What was accepted now has its own task, following the `M6`→`M7` precedent: [M9 — v1.0.0 surface additions](m9-v1-surface-additions.md) implements `#83`, `#86`, `#78` (partial), and `#85` (partial). Once M9 lands, the tree is tag-ready — the only thing left before `v1.0.0` is the maintainer's own usage-based timing decision, which neither this milestone nor M9 attempts to influence.
