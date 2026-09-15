# M9 — v1.0.0 surface additions

> See the [task index](README.md) for the milestone map and conventions.

**Covers:** no v1 checklist item — v1 shipped and is closed. This milestone implements what [M8-7](m8-v1-readiness.md#m8-7--api-ergonomics-review-of-the-v020-surface-the-v100-gate)'s ergonomics review recommended and the maintainer accepted, recorded in that task's "Review outcome" section and in [06 — Scope & Roadmap § Accepted before v1.0.0](../06-scope-and-roadmap.md#accepted-before-v100). Follows the `M6`→`M7` precedent: a decision task (`M8-7`) records *what*, this milestone builds it, one task and one PR per item.

**Tagging `v1.0.0` is not part of this milestone's scope**, same as `M8`: the maintainer ties the tag to real usage evidence, independent of surface readiness. Once `M9` lands, the surface is tag-ready.

**Issue map:**

| Task | Issue | Recommendation source |
|---|---|---|
| `M9-1` | [#83](https://github.com/jpgomesr/netchaos/issues/83) | [M8-7 F2](m8-v1-readiness.md#f2--partitionhealreset-accept-a-self-pair-or-an-empty-peer-name-silently-unlike-withpartition-issue-83) |
| `M9-2` | [#86](https://github.com/jpgomesr/netchaos/issues/86) | [M8-7 F5](m8-v1-readiness.md#f5--dialerfor-has-no-way-to-bound-a-wait-on-a-partitioned-peer-issue-86) |
| `M9-3` | [#78](https://github.com/jpgomesr/netchaos/issues/78) (payload size + corruption site only) | [M8-7 F4](m8-v1-readiness.md#f4--faultevent-cant-diagnose-a-corruptionduplication-failure-or-attribute-a-reset-issue-78) |
| `M9-4` | [#85](https://github.com/jpgomesr/netchaos/issues/85) (`SetDuplication`/`SetCorruption` only) | [M8-7 F6](m8-v1-readiness.md#f6--runtime-setters-exist-for-two-of-five-fault-kinds-issue-85) |
| `M9-5` | — | close-out: sync `README.md` and `.claude/skills/netchaos/` |

`M9-1` through `M9-4` are independent of each other — no task depends on another's code — and each is a single-PR, test-first change. They do share files, so expect rebase cost if they land out of order or in parallel: all four touch `docs/04-api-design.md` (different sections) and `CHANGELOG.md`, and `M9-4` also touches `doc.go`'s determinism-contract comment. `M9-5` depends on all four and is deliberately last.

**A convention this milestone follows, established by precedent rather than stated anywhere before now:** `docs/04-api-design.md` (and `docs/05` where a task changes fault semantics), `doc.go`'s determinism-contract comment, and `AGENTS.md`'s project-snapshot line are updated **in the same PR** as the code change — every prior `M7` task did this, and `#89`'s fix (address host handling) is the clearest single example, updating `docs/04`'s address section in the same commit as the behavior change. `README.md` and `.claude/skills/netchaos/` are **not** touched per task: both are pinned to the last tagged release by design (`README.md`'s "Status: `v0.2.0` released" banner, the skill's own `go get …@v0.2.0` instruction and "usable from any project" self-sufficiency claim), and git history confirms neither has been touched by any post-`v0.2.0` fix (`#73`/`#76`/`#79`–`#89`/`#92`–`#97`) — they were last updated together, once, at the `v0.2.0` milestone close (`#71`). `M9-5` is that same close-out step for this milestone.

---

## M9-1 — `#83`: validate `Partition`/`Heal`/`Reset` the way `WithPartition` already does

**Status:** todo
**Issue:** [#83](https://github.com/jpgomesr/netchaos/issues/83)
**Depends on:** —
**Blocks:** —

**Decision, recorded here so implementation doesn't have to pick it:** validate the **raw, unresolved** `peerA`/`peerB` arguments — the same way `WithPartition` does today (`validatePartitionPair`, `partition.go:41`, called on the raw `partitionPair{peerA, peerB}` in `networkConfig.validate()`, before `NewNetwork` resolves it through `peerName` at `netchaos.go:162`). Concretely, `Partition`/`Heal`/`Reset` must call validation on `peerA, peerB` **before** `peerName(peerA)`/`peerName(peerB)` runs, not after — matching `WithPartition`'s exact timing, so the two entry points panic on the same inputs and accept the same inputs.

**Known limitation this task does not close, worth stating so it isn't mistaken for an oversight:** `peerName` strips a `:port` suffix, and validation (both `WithPartition`'s today and `Partition`/`Heal`/`Reset`'s after this task) runs on the raw strings, before that stripping happens. So `WithPartition("client:1234", "client:5678")` does not panic today — the two raw strings differ — but `NewNetwork` then resolves both through `peerName` and stores a **self-pair** in `n.partitions` (`netchaos.go:162`), silently. Raw-arg validation on `Partition`/`Heal`/`Reset` reproduces this exact gap rather than closing it, which is the right call for *this* task (its stated goal is entry-point consistency, not a new validation rule `WithPartition` itself doesn't have) but is a real, separate hole worth its own issue if it matters in practice — do not fix it here without opening one.

**Scope:**
- `partition.go:41`'s `validatePartitionPair` already takes a `partitionPair` and hardcodes `"WithPartition"` in its panic text. Thread a `caller string` parameter through it, mirroring `validateLatencyRange`/`validateLossRate`'s existing shape (`latency.go:45`, `loss.go:46`, both already caller-aware since `M8-2`).
- Call `validatePartitionPair("Partition", partitionPair{peerA, peerB})` at the top of `Network.Partition` (`partition.go:80`), `"Heal"` at the top of `Network.Heal` (`partition.go:102`), and `"Reset"` at the top of `Network.Reset` (`reset.go:45`) — before each method's existing `peerName(...)` call.
- The existing no-op-for-unknown-peer behavior is unchanged: this only turns the two previously-silent misuse cases (empty name, self-pair) into panics; a partition/heal/reset naming a peer that was never dialed or listened stays a no-op.

**Tests** (`partition_test.go`, `reset_test.go`): for each of `Partition`/`Heal`/`Reset`, both conditions (`("", "b")` and `("a", "a")`) panic with a message naming that method; a pre-existing valid no-op case (unregistered peer name) still doesn't panic. One test documenting the known limitation above, not asserting it as correct: `Partition("client:1", "client:2")` does **not** panic (differing raw strings), matching `WithPartition`'s existing behavior on the same input — comment the test to say this reproduces a pre-existing gap, not that it was verified safe.

**Docs in the same PR:** godoc on all three methods (`partition.go`, `reset.go`) gains a line noting the panic, matching `WithPartition`'s. `docs/04-api-design.md#dynamic-partition-control` and `docs/05-fault-injection.md`'s no-op convention text get the same addition. `CHANGELOG.md` under `### Changed` (behavior change on three already-shipped methods, not a new identifier).

---

## M9-2 — `#86`: `DialerFor` gains a bounded wait

**Status:** done — [#100](https://github.com/jpgomesr/netchaos/pull/100)
**Issue:** [#86](https://github.com/jpgomesr/netchaos/issues/86)
**Depends on:** —
**Blocks:** —

**Scope:**
- New exported type `DialerOption func(*dialerConfig)` (unexported `dialerConfig` struct, one field: `timeout time.Duration`, zero meaning "no timeout" — matches today's `context.Background()` behavior exactly).
- New exported `WithDialTimeout(d time.Duration) DialerOption`. `d <= 0` panics (`netchaos: WithDialTimeout: timeout must be positive, got %v`), following the existing panic-on-invalid convention (`validateLatencyRange` et al.).
- `DialerFor(name string, opts ...DialerOption)` (`netchaos.go:291`): apply `opts` to a `dialerConfig`, keep the existing `WithPeerName(context.Background(), name)` base context. Inside the returned closure, if `timeout > 0`, wrap with `ctx, cancel := context.WithTimeout(base, timeout)` and `defer cancel()` per call before invoking `n.DialContext`; otherwise call exactly as today. Safe because `DialContext` never retains `ctx` past establishment (only reads it in the initial select and in `waitUnpartitioned`, `netchaos.go:336`, `:361`).
- No signature break: existing `DialerFor(name)` calls keep compiling and keep today's unbounded-wait behavior — confirm this is what `DialContext`'s existing error wrapping already produces for a `context.DeadlineExceeded` (it should come back as a `*net.OpError` via `dialOpError`, satisfying `errors.Is(err, context.DeadlineExceeded)`); don't add a second wrapping layer if the existing one already covers it.

**Tests** (`netchaos_test.go` or a new `dialer_test.go`, in `synctest`): a dial through `DialerFor(name, WithDialTimeout(d))` against a partitioned peer fails after `d` virtual time with an error satisfying `errors.Is(err, context.DeadlineExceeded)`; the same dial through plain `DialerFor(name)` still blocks until `Heal` (regression guard on the default); `WithDialTimeout(0)` and a negative duration panic.

**Docs in the same PR:** `DialerFor`'s godoc (`netchaos.go:262-290`) — the "there is no context here to bound the wait" sentence is no longer true and needs to say how to use `WithDialTimeout` instead. `docs/04-api-design.md#dialing-and-listening`. `CHANGELOG.md` under `### Added`.

---

## M9-3 — `#78`: `FaultEvent` gains payload size and corruption site

**Status:** todo
**Issue:** [#78](https://github.com/jpgomesr/netchaos/issues/78) — payload size and corruption-site fields only; `Reset` attribution stays deferred ([docs/06](../06-scope-and-roadmap.md#explicitly-out-of-scope-for-v1))

**Decision, recorded here so implementation doesn't have to pick it:** `FaultEvent`'s existing godoc promises "Partitioned is never true alongside any other field... every other field on that event is its zero value." **That invariant is preserved, not amended** — the new `Size`/corruption-site fields stay zero on a `Partitioned` event, exactly like every other field there. They are populated on every event past the partition gate, including a `Dropped` one (mirroring how `Duplicated`/`Corrupted` are already recorded on a dropped unit per the draw discipline), since size is known and meaningful even for a unit that was discarded.

**Scope:**
- `trace.go:10`'s unexported `faultEvent` gains `size int`, `corruptByte int`, `corruptBit uint8`.
- `faults.go:178` currently computes `byteIndex, bitIndex := p.corrupt.corruptionSite(len(data))` and uses them only to flip the bit — record them into the event being built for this unit instead of discarding them. `size` is recorded once per unit, at the point `len(data)` is first known, for every unit that reaches the evaluator (i.e., every non-partitioned unit) — zero on a partitioned event's `faultEvent{partitioned: true}` (`faults.go:132`), matching the decision above.
- `trace.go`'s exported `FaultEvent` (`trace.go:130` area) gains `Size int`, `CorruptedByte int`, `CorruptedBit uint8`. `Trace()` (`trace.go:195`) copies the three new fields alongside the existing ones.
- Godoc on `FaultEvent`: state that `CorruptedByte`/`CorruptedBit` are only meaningful when `Corrupted && Size > 0` (a zero-length write draws the corruption decision but has no byte to flip — see `corrupt_test.go`'s existing `TestCorruptionOnZeroLengthWriteDoesNotPanic`), and that `Size` is zero exactly when `Partitioned` is true, consistent with the existing invariant sentence.

**Tests:** a corrupted multi-byte write's `FaultEvent` reports the exact byte/bit index the corruption stream drew (compare directly against `stream.corruptionSite`'s output for the same draw — `rand_test.go` likely already has a helper); a zero-length corrupted write reports `Size == 0` and no corruption-site panic (extend `TestCorruptionOnZeroLengthWriteDoesNotPanic`); a partitioned event reports `Size == 0`, preserving the existing zero-value-invariant test if one exists, or adding one.

**Golden traces:** check whether `reproducibility_test.go`'s golden-trace format serializes every `FaultEvent` field or only the ones each `scenario`'s declared `fields` list names (`scenarioCorrupted` already scopes to `fields: []string{"corrupt"}`). If the format is field-scoped, add `size`/`corruptByte`/`corruptBit` columns only to scenarios that already declare `corrupt` (or a new scenario), leaving other goldens untouched — confirm with `go test -run 'Reproduc|Golden' ./...` before and after that unrelated goldens don't change.

**Docs in the same PR:** `docs/04-api-design.md#full-fault-trace-export`. `CHANGELOG.md` under `### Added`.

---

## M9-4 — `#85` (partial): `SetDuplication` and `SetCorruption`

**Status:** todo
**Issue:** [#85](https://github.com/jpgomesr/netchaos/issues/85) — `SetDuplication`/`SetCorruption` only; `SetBandwidth` stays deferred ([docs/06](../06-scope-and-roadmap.md#explicitly-out-of-scope-for-v1))

**Read path already handles this — verified, not assumed:** `netchaos.go:48`'s `faultMu` guards the whole `n.faults faultConfig` struct, and the per-unit evaluator already reads that entire struct in one `RLock` via `faultConfig()` (`netchaos.go:441-444`), which `faultPolicy.current()` (`faults.go:65-70`) calls on every unit regardless of which fault kinds are enabled. `duplicateEnabled`/`duplicateRate`/`corruptEnabled`/`corruptRate` are fields on that same struct, so they are **already** read under `faultMu` on the hot path — this is unlike the situation `M6-13`/`M7-3` faced for latency/loss, where the read path had to be *changed* from lock-free to locked. No read-path change is needed here; the gap is purely the missing setter methods.

**Scope:**
- `Network.SetDuplication(rate float64)` and `Network.SetCorruption(rate float64)`, mirroring `SetPacketLoss` exactly (`netchaos.go:492-499`): validate, then `n.faultMu.Lock(); defer n.faultMu.Unlock(); n.faults.duplicateEnabled = true; n.faults.duplicateRate = rate` (and the `corrupt*` equivalent).
- Thread a `caller string` parameter through `validateDuplicationRate` (`duplicate.go:46`) and `validateCorruptionRate` (`corrupt.go:46`), the same change `M8-2` already made to `validateLatencyRange`/`validateLossRate`, so `SetDuplication`/`SetCorruption`'s panics name themselves rather than `WithDuplication`/`WithCorruption`.
- Remove the now-inaccurate "There is no runtime setter... #50 named only latency and packet loss for runtime mutation" comments in `faults.go:28-32`, `faults.go:36-39`, `duplicate.go:31-34`, `corrupt.go` (equivalent). Leave `bandwidth.go:31-34`'s version in place but reword it to say bandwidth specifically (not "the other four") lacks a setter, since duplication and corruption no longer do.

**Contract widening, before the code (same posture `M7-3` took for `M6-13`):** `docs/04-api-design.md#determinism-contract`'s ordered-calls list (`doc.go:19-20`'s comment repeats it) currently names `Dial, Listen, Partition, Heal, SetLatency, SetPacketLoss` — add `SetDuplication, SetCorruption` to that list in the same PR, before or alongside the code, not after. Restate explicitly that this doesn't change draw discipline: enabling duplication or corruption mid-run (when it was off at construction) begins drawing from that kind's independent stream without shifting any other kind's sequence — same as `SetLatency`/`SetPacketLoss` already established.

**Tests** (`setters_test.go`): panic naming for both setters on out-of-range/NaN input; live effect — a connection dialed before either setter is called, then `SetDuplication(1.0)`/`SetCorruption(1.0)` called on it, observably duplicates/corrupts subsequent writes without a re-dial; determinism — two identical runs (same seed, same call order) produce identical traces.

**Docs in the same PR:** `docs/04-api-design.md#runtime-fault-mutation` (the "No setter exists for..." paragraph updates to name only `WithBandwidth`). `doc.go`'s contract comment. `CHANGELOG.md` under `### Added`, noting `#85` closes partially (with a pointer to the `docs/06` line for `SetBandwidth`).

---

## M9-5 — close out M9: sync `README.md` and the skill

**Status:** todo
**Issue:** —
**Depends on:** `M9-1`, `M9-2`, `M9-3`, `M9-4`
**Blocks:** —

Mirrors `#71` ("docs: close out the v0.2.0 milestone") and `#90` ("docs: bring doc.go, AGENTS.md and docs/03 up to the shipped v0.2.0 surface") — a single pass, once, after the individual task PRs land, rather than touching these two per task (see this file's intro for why).

- `README.md`: add a section for what `M9` added (`Partition`/`Heal`/`Reset` validation, `DialerFor`'s `WithDialTimeout`, the two new `FaultEvent` fields, `SetDuplication`/`SetCorruption`), matching the shape of the existing `## v0.2.0 additions` section. Update the status banner if a new tag has shipped by the time this runs; if not, phrase it as "on `main`, not yet tagged" rather than guessing a version number — tagging is the maintainer's own call ([docs/06](../06-scope-and-roadmap.md)), not this task's.
- `.claude/skills/netchaos/SKILL.md` and `.claude/skills/netchaos/references/api.md`: update the "Full surface" listing, the partition/DialerFor sections, the `FaultEvent` struct literal (both copies — quick orientation and the full reference), and the "No setter exists for..." gotcha to reflect `M9`'s additions. Bump the `go get …@vX` instruction only once a tag actually covers this work.

**Verification:** `grep -rn "there is no way to bound\|no runtime setter exists.*Duplication\|no runtime setter exists.*Corruption" .claude/skills README.md` returns nothing once this lands.
