# Task breakdown — netchaos v1 and beyond

> **Status: M0–M5 are a historical record; M6 is the only active milestone.** This folder broke the v1 scope into executable tasks and sequences how v1 was actually built — M0 through M4 are done, `v0.1.0` is tagged and pushed, and that part of the folder is a record of what was decided, in what order, and why, not a forward-looking plan. [M5 — v0.1.0 hardening & v0.2.0 groundwork](m5-hardening-and-ergonomics.md) is now also closed: it verified the release end to end, ran the pre-`v1.0.0` ergonomics review, and added the `bug`/`enhancement` issue forms. Its review raised the repository's first two issues, [#36](https://github.com/jpgomesr/netchaos/issues/36) and [#37](https://github.com/jpgomesr/netchaos/issues/37), both `needs-discussion`. [M6 — Post-v0.1.0 review findings](m6-review-findings.md) is the recorded output of a full review of the tagged source tree — a backlog of findings, not a plan that was agreed to in advance, and the only milestone with open work. See each milestone file's own task statuses for the current state.

## What this is

[06 — Scope & Roadmap](../06-scope-and-roadmap.md) defines v1 as a flat six-item checklist. That checklist says *what* v1 contains, but not what order the items must land in, which ones block which, or what "implement latency injection" actually entails. This folder fills that gap: one file per milestone, each containing tasks with an objective, scope, affected files, dependencies, acceptance criteria and tests.

**The milestones are derived groupings of that checklist, not headings that exist in the design docs.** [06 — Scope & Roadmap](../06-scope-and-roadmap.md) contains no milestones — M0–M4 below were introduced here purely to sequence the existing checklist items. They add no scope. Every task traces back to a line of the v1 checklist, and the mapping table below shows exactly how.

## Milestones

| Milestone | v1 checklist items covered | Why it is a unit |
|---|---|---|
| [M0 — Decisions & foundations](m0-decisions-and-foundations.md) | *(gates all six)* | [06](../06-scope-and-roadmap.md#reordering-in-or-out-of-v1) says the reordering call is needed *before v1 API design is finalized*; [04](../04-api-design.md) and [05](../05-fault-injection.md) flag two further open questions. Resolving them first avoids the churn [07 — Contributing](../07-contributing.md) warns about. |
| [M1 — Core simulated transport](m1-core-transport.md) | Simulated `net.Conn` / `net.Listener` (TCP-shaped) with pluggable fault injection | The substrate everything else plugs into: a correct, buffered, deadline-honouring in-memory transport, with no faults applied yet. |
| [M2 — Determinism & the three faults](m2-determinism-and-faults.md) | Latency injection · Packet loss · Network partition · Seeded randomness | Seeded randomness is the *input* to latency and loss, so it lands first **inside** this milestone rather than after the faults that consume it. |
| [M3 — synctest & reproducibility](m3-synctest-and-reproducibility.md) | Integration with `testing/synctest` for virtual time | Proves the two properties the library is sold on: latency costs no real wall-clock time, and a seed reproduces a failure exactly. |
| [M4 — API polish & release](m4-api-polish-and-release.md) | *(release of all six)* | Godoc, runnable examples, flipping the design-stage status banners, tagging `v0.1.0`. |
| [M5 — v0.1.0 hardening & v0.2.0 groundwork](m5-hardening-and-ergonomics.md) | *(none — post-v1)* | Not part of the v1 checklist, and now complete. Closed out `M4-5`'s post-tag verification, ran the pre-`v1.0.0` ergonomics review, and closed the issue-template gap — see the file's own banner for what it deliberately excluded. |
| [M6 — Post-v0.1.0 review findings](m6-review-findings.md) | *(none — post-v1)* | Not a grouping of planned work: the output of a full review of the tagged tree. One contract contradiction, some small correctness and precision fixes, three coverage gaps, and the feature candidates that pass [06](../06-scope-and-roadmap.md#how-to-think-about-future-scope-decisions)'s scope rubric — recorded as decisions to make, not work agreed to. |

All six checklist lines from [06 — Scope & Roadmap](../06-scope-and-roadmap.md) appear exactly once in the table above; `M5` and `M6` cover no checklist line, since v1's checklist is closed.

### A note on M3's real size

[03 — Architecture](../03-architecture.md#composing-with-testingsynctest) says latency should ride on standard timer primitives that `synctest` already virtualizes, rather than introducing a clock abstraction. That decision means M3 contains very little new production code — its real content is the reproducibility harness and the synctest-based test suite. Do not budget M3 as a large build.

## Dependency order

Milestones M0 through M5 gate each other in order — M0 → M1 → M2 → M3 → M4 → M5 — and within that, these are the exact edges. **M6 is not gated on M5:** it collects findings against code that already shipped, so nothing in it waits on M5's release verification or its ergonomics review, and its tasks' own `Depends on:` lines say so. Each task file repeats these edges as its own `Depends on:` / `Blocks:` lines; if the two ever disagree, the task file is authoritative.

```
M0   M0-1, M0-2      ─▶ M0-5
     M0-3            ─▶ M0-4 ─▶ M0-5
     M0-3, M0-6      ─▶ M1-1
     M0-5            ─▶ M1-4

M1   M1-1, M1-4      ─▶ M1-2 ─▶ M1-3
     M1-4            ─▶ M1-5 ─▶ M1-6
     M1-2, M1-6      ─▶ M1-7 ─▶ M1-8

M2   M0-4, M1-5, M1-7      ─▶ M2-1   (seeded RNG)
     M1-3, M2-1            ─▶ M2-2   (latency)
     M0-3, M2-1            ─▶ M2-3   (packet loss)
     M1-4, M1-7, M1-8      ─▶ M2-4   (partition)
     M2-2, M2-3, M2-4      ─▶ M2-5, M2-6

M3   M1-8, M2-5      ─▶ M3-1
     M2-2, M3-1      ─▶ M3-2
     M2-1, M2-5, M3-1 ─▶ M3-3
     M3-2, M3-3      ─▶ M3-4

M4   M2-6, M3-1      ─▶ M4-1
     M3-4, M4-1      ─▶ M4-2 ─▶ M4-3 ─▶ M4-4 ─▶ M4-5

M5   M4-5            ─▶ M5-1
     (no code dependency) ─▶ M5-2, M5-3

M6   M6-7            ─▶ M6-8
     (no code dependency) ─▶ M6-1..M6-6, M6-9..M6-17
```

M6's tasks are deliberately almost all independent — it is a review's findings, not a build, so there is no order the work has to happen in. The one edge is `M6-7` before `M6-8`: without a latency benchmark there is no way to tell whether `M6-8`'s optimization did anything. Two relationships are **not** dependency edges and are kept in prose so the two task files don't disagree: `M6-9` and `M6-10` both feed [M5-2](m5-hardening-and-ergonomics.md#m5-2--api-ergonomics-review-before-v100)'s ergonomics review as input, and `M6-12` overlaps `M6-16` closely enough that if both are ever taken up they should be designed together. M6's own file states all of this on the tasks themselves.

Note that **partition (M2-4) does not depend on the RNG task (M2-1)**. A partition is binary, not probabilistic, and M2-4 requires it to consume no random draws at all — otherwise partitioning one peer pair would shift the fault sequence on unrelated connections. It hangs off M1, not off M2-1.

The two orderings that matter most, because getting them wrong causes rework:

1. **M0 before any API-shaping code.** Three open questions (reordering, fault scoping, fault granularity) each changed the public surface — all three are now resolved, see below.
2. **M2-1 before M2-2 and M2-3.** Both faults draw from the seeded random source; building them against an ad-hoc RNG and retrofitting determinism later means rewriting both.

## M0's open questions are resolved

M0's four decision tasks (M0-1 reordering, M0-2 fault scoping, M0-3 fault granularity, M0-4 determinism under concurrency) are done — see [M0 — Decisions & foundations](m0-decisions-and-foundations.md) for each decision and where it's recorded. Reordering is **out of v1**. Task files elsewhere in this folder that still read as conditional on these questions ("if reordering is in scope…") were written before M0 closed and have not been swept for that language; treat M0's task file as authoritative if the two disagree.

## Task ID scheme and status

- IDs are `M<milestone>-<n>` — e.g. `M2-2`. **IDs are stable and are never renumbered.** If a task is dropped, mark it `dropped` and leave the ID in place so cross-references in other tasks and in commit messages keep resolving.
- Status is tracked by the `- [ ]` / `- [x]` checkboxes on each task's acceptance criteria, plus a `**Status:**` line on the task itself: `todo` · `in progress` · `done` · `dropped`.
- A task is `done` only when every acceptance-criteria box is ticked and the verification block below passes.

## Verification for every code task

Work test-first: write the test before the production code it drives, run it and confirm it fails (red) — a compile error counts — then write the smallest change that makes it pass (green). Never commit production code without the failing test that motivated it in the same or an earlier commit, and never skip observing the red.

From `AGENTS.md`, matching CI in `.github/workflows/`:

```
go build ./...
go vet ./...
gofmt -l .        # must print nothing
go test -race ./...
golangci-lint run # config: .golangci.yml
```

`-race` requires `CGO_ENABLED=1`. On Windows without a C toolchain this fails locally even though it passes on CI's `ubuntu-latest` runners — check whether cgo is actually available before treating a local `-race` failure as a code problem.

Git workflow is unchanged: Conventional Commits, explicit `git add <path>`, branch + PR, never direct to `main`. See `AGENTS.md` and [`CONTRIBUTING.md`](../../CONTRIBUTING.md).

## On GitHub issues

These tasks are shaped so they map onto issues one-to-one if that is wanted later. **No issues are being created from this document.** `AGENTS.md` notes the repo deliberately has only `design-feedback` and `use-case-scenario` forms, with no `bug`/`enhancement` workflow yet — introducing one is a decision for the maintainer, not a side effect of this breakdown. [M5-3](m5-hardening-and-ergonomics.md#m5-3--decide-on-bugenhancement-issue-template-forms) is where that decision is now tracked.
