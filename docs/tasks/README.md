# Task breakdown — netchaos v1

> **Status: planning artifact.** This folder breaks the v1 scope into executable tasks. It describes work that has **not** been done — nothing in `docs/tasks/` implies an implementation exists.

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

All six checklist lines from [06 — Scope & Roadmap](../06-scope-and-roadmap.md) appear exactly once in the table above.

### A note on M3's real size

[03 — Architecture](../03-architecture.md#composing-with-testingsynctest) says latency should ride on standard timer primitives that `synctest` already virtualizes, rather than introducing a clock abstraction. That decision means M3 contains very little new production code — its real content is the reproducibility harness and the synctest-based test suite. Do not budget M3 as a large build.

## Dependency order

```
M0  (decisions — gates API freeze)
 │
 ▼
M1  M1-1 ─▶ M1-2 ─▶ M1-3
     └─▶ M1-4 ─▶ M1-5 ─▶ M1-6 ─▶ M1-7 ─▶ M1-8
 │
 ▼
M2  M2-1 (seeded RNG) ─┬─▶ M2-2 (latency)
                       ├─▶ M2-3 (packet loss)
                       └─▶ M2-4 (partition)
                              └─▶ M2-5 ─▶ M2-6
 │
 ▼
M3  M3-1 ─▶ M3-2 / M3-3 ─▶ M3-4
 │
 ▼
M4  M4-1 ─▶ M4-2 ─▶ M4-3 ─▶ M4-4 ─▶ M4-5
```

The two orderings that matter most, because getting them wrong causes rework:

1. **M0 before any API-shaping code.** Three open questions (reordering, fault scoping, fault granularity) each change the public surface.
2. **M2-1 before M2-2 and M2-3.** Both faults draw from the seeded random source; building them against an ad-hoc RNG and retrofitting determinism later means rewriting both.

## Open question that gates M0

Whether **reordering** is in v1 is unresolved — flagged in [05 — Fault Injection](../05-fault-injection.md#reordering-open-question), [06 — Scope & Roadmap](../06-scope-and-roadmap.md#reordering-in-or-out-of-v1), the [docs index](../README.md) and `AGENTS.md`. It is task **M0-1**. No task in this folder assumes an answer; every mention of reordering elsewhere is written conditionally. Do not silently resolve it while executing another task.

## Task ID scheme and status

- IDs are `M<milestone>-<n>` — e.g. `M2-2`. **IDs are stable and are never renumbered.** If a task is dropped, mark it `dropped` and leave the ID in place so cross-references in other tasks and in commit messages keep resolving.
- Status is tracked by the `- [ ]` / `- [x]` checkboxes on each task's acceptance criteria, plus a `**Status:**` line on the task itself: `todo` · `in progress` · `done` · `dropped`.
- A task is `done` only when every acceptance-criteria box is ticked and the verification block below passes.

## Verification for every code task

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

These tasks are shaped so they map onto issues one-to-one if that is wanted later. **No issues are being created from this document.** `AGENTS.md` notes the repo deliberately has only `design-feedback` and `use-case-scenario` forms, with no `bug`/`enhancement` workflow yet — introducing one is a decision for the maintainer, not a side effect of this breakdown.
