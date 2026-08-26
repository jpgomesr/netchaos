# M4 — API polish & release

> See the [task index](README.md) for the milestone map and conventions.

**Covers:** the release of all six v1 checklist items from [06 — Scope & Roadmap](../06-scope-and-roadmap.md), rather than any single one.

**What this milestone is for:** every doc in this repository currently says netchaos has no implementation — [docs/README.md](../README.md), [03](../03-architecture.md), [04](../04-api-design.md), [05](../05-fault-injection.md) and [07](../07-contributing.md) each carry a design-stage banner, and `AGENTS.md` tells agents to assume nothing is implemented. Once M1–M3 land, every one of those statements is false. M4 makes the repository tell the truth about itself, gives the public API the godoc a library people import needs, and tags `v0.1.0`.

**Order matters here:** do not flip the status banners (M4-3) before the code they describe is actually merged. A doc claiming an implementation that is not on `main` is worse than one that is out of date in the safe direction.

---

### M4-1 — Godoc pass on the public surface

**Status:** done
**Roadmap item:** release readiness for all six items
**Depends on:** M2-6, M3-1
**Blocks:** M4-2

**Objective**
Document every exported identifier to the standard a library people `go get` requires. For netchaos specifically, the doc comments must carry the two things a user cannot infer from a signature: the determinism contract, and the constraints that using `testing/synctest` imposes.

**Scope**
- Expand `doc.go` from its current four lines into a package doc with a runnable-shaped overview: what netchaos is, the `Network` → `Dial`/`Listen` model, the seed-and-reproduce workflow, and a short usage snippet.
- Godoc on every exported identifier: `Network`, `NewNetwork`, `Option`, `WithSeed`, `WithLatency`, `WithPacketLoss`, `WithPartition`, `Dial` (and `DialContext` if in scope), `Listen`, `Partition`, `Heal`, and every error sentinel from M1-8.
- Each option states its valid range and what happens outside it (M2-6's decision).
- **Document the synctest usage constraints found in M3-1** — these are the ones a user will otherwise hit as a confusing panic:
  - A `Network` must be constructed *inside* the `synctest.Test` bubble that uses it, because channels and timers created in a bubble panic when operated on from outside.
  - Connections must not be shared across bubbles.
  - netchaos works outside a bubble too, in which case latency consumes real wall-clock time — say so, so nobody assumes virtual time comes for free.
- Document the determinism contract on `WithSeed`, including its limits as settled in M0-4. Under-promising here is far cheaper than a user building a CI gate on a guarantee that does not hold.
- Document the fault composition order from M2-5 where a user will look for it — on the option functions, not only in `docs/`.
- Out of scope: the `docs/` design set (M4-3).

**Files**
- `doc.go` — package documentation
- Every `.go` file with exported identifiers
- `errors.go` — sentinel docs

**Acceptance criteria**
- [x] Every exported identifier has a doc comment starting with its own name, per Go convention.
- [x] `go doc github.com/jpgomesr/netchaos` reads as a usable introduction on its own.
- [x] The synctest constraints above appear in the package doc.
- [x] `WithSeed`'s doc states the determinism guarantee *and* its limits.
- [x] Each option documents its valid range and out-of-range behaviour.
- [x] Fault composition order is discoverable from godoc alone.
- [x] `golangci-lint run` is clean (`staticcheck` flags malformed doc comments) — verified in CI, not available locally on this machine.
- [x] No doc comment describes behaviour that is not implemented.

**Tests**
- Not test-driven; verified by reading `go doc -all`.
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M4-2 — Runnable examples

**Status:** done
**Roadmap item:** release readiness for all six items
**Depends on:** M3-4, M4-1
**Blocks:** M4-3

**Objective**
Ship `Example` functions that compile and run as part of `go test`, so the code on pkg.go.dev is guaranteed to work. The example in [04 — API Design](../04-api-design.md#full-usage-sketch) and the one in the root `README.md` are currently hand-written sketches against an API that did not exist — turning them into compiled tests is what stops them drifting.

**Scope**
- `Example` (package-level) — the basic dial/listen round trip.
- `ExampleNetwork_Dial`, `ExampleWithPacketLoss`, `ExampleWithLatency`, `ExampleNetwork_Partition` — one per headline feature.
- Adapt the M3-4 scenarios down to example size; the circuit-breaker one in particular is the most persuasive thing the library can show.
- Examples that produce deterministic output use `// Output:` comments and a fixed seed. Anything whose output cannot be made deterministic must not have an `// Output:` block, since it would flake — prefer restructuring the example so it *is* deterministic.
- Reconcile the `README.md` snippet and the [04](../04-api-design.md#full-usage-sketch) sketch with the shipped API. Both currently use `synctest.Test(t, func(t *testing.T){...})`, which is correct for Go 1.25+ and should be kept.
- Note the structural constraint: examples using `synctest` need a `*testing.T`, which `Example` functions do not have. Either the examples wrap their body differently, or the synctest-dependent ones live as `Test` functions referenced from prose. Decide and apply consistently rather than fighting it per-example.
- Out of scope: new features surfaced by writing examples — file those separately rather than growing v1.

**Files**
- `example_test.go` (new)
- `README.md` — sync the snippet
- [`docs/04-api-design.md`](../04-api-design.md) — sync the usage sketch

**Acceptance criteria**
- [x] Every example compiles and passes under `go test`.
- [x] Examples with `// Output:` blocks are deterministic and use a fixed seed.
- [x] There is at least one example per headline feature: latency, packet loss, partition, seeding.
- [x] The `README.md` snippet compiles as written — verified by an equivalent example test, since the README itself is not compiled.
- [x] The [04](../04-api-design.md#full-usage-sketch) sketch matches the shipped API exactly.
- [x] The synctest-in-examples structural decision is applied consistently: `testing/synctest.Test` requires a `*testing.T`, which `Example` functions do not have, so `Example*` functions never enter a bubble and the two synctest-dependent demonstrations (`TestReadmeUsageSnippet`, `TestCircuitBreakerAcrossPartitionAndHeal`) live as `Test` functions instead.
- [x] `go vet` reports no malformed example names.

**Tests**
- The examples are the tests.
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M4-3 — Flip the design-stage status banners

**Status:** done
**Roadmap item:** release readiness for all six items
**Depends on:** M4-2
**Blocks:** M4-4

**Objective**
Every status statement in the repository currently asserts that no implementation exists. Update each to match reality. Miss one and a reader is actively misled — and an AI agent reading `AGENTS.md` will be told, incorrectly, that no implementation exists.

**Scope** — each of these needs its banner or status paragraph rewritten:

- [`docs/README.md`](../README.md) — "**Status: design-stage.** `netchaos` has no implementation yet…", plus the "Open question flagged in these docs" section if M0-1 resolved it.
- [`docs/03-architecture.md`](../03-architecture.md) — "**Status: design-stage.** No implementation exists yet… it is not a description of existing code." Once true, this doc describes real structure and should say so.
- [`docs/04-api-design.md`](../04-api-design.md) — "**PROPOSED / NOT YET IMPLEMENTED.**" Replace with the shipped surface; M0-5 already narrowed this to "agreed", and this step takes it to "implemented".
- [`docs/05-fault-injection.md`](../05-fault-injection.md) — "**Status: design-stage.**", plus the reordering open-question section per M0-1.
- [`docs/07-contributing.md`](../07-contributing.md) — the whole "Current project status" and "What's useful to contribute right now" framing inverts: implementation PRs were deferred *because* the API was unsettled; with v1 shipped, that reasoning no longer applies. This is the largest rewrite in the task.
- Root `README.md` — the status line and any "no implementation yet" phrasing.
- `AGENTS.md` — "**Current stage: pre-implementation, design-only.** The module … contains nothing but a placeholder `doc.go` … Do not assume any implementation exists". This is what other agents read first; a stale claim here misleads every future session.
- `CONTRIBUTING.md` — check for status claims. Note `AGENTS.md` asks that changes to `CONTRIBUTING.md` be flagged, so raise the specific edits rather than rewriting silently.
- [`docs/tasks/`](README.md) — mark completed tasks `done`; this file set becomes the historical record of how v1 was built.

**Files**
- All files listed above.

**Acceptance criteria**
- [x] No file in the repository claims netchaos has no implementation.
- [x] Every design doc distinguishes what is implemented from what remains proposed for post-v1.
- [x] [07 — Contributing](../07-contributing.md) describes a contribution model appropriate to a shipped library.
- [x] `AGENTS.md`'s project snapshot matches the actual module contents.
- [x] The reordering open question is either resolved in every location (per M0-1) or still accurately described as open.
- [x] Proposed `CONTRIBUTING.md` edits are flagged to the maintainer before landing — see this PR's description.
- [x] Every internal doc link still resolves.

**Widened beyond the task's own file list**, since the grep sweep and the M0-1 retrospective's own note (its recording list "turned out to be incomplete in practice") both flagged more: `.claude/commands/architecture.md`, `commit.md`, `issue.md` (all said "pre-implementation"), `docs/tasks/README.md` (said "nothing in `docs/tasks/` implies an implementation exists" — the other flat false claim besides `CONTRIBUTING.md`), and the README's Contributing section and out-of-scope line.

**Tests**
- Not test-driven. Grep for "design-stage", "no implementation", "PROPOSED", "not yet implemented", "pre-implementation" and confirm every hit is either updated or deliberately still accurate.

---

### M4-4 — Tick the v1 checklist

**Status:** done
**Roadmap item:** all six
**Depends on:** M4-3
**Blocks:** M4-5

**Objective**
Check off the six boxes in [06 — Scope & Roadmap](../06-scope-and-roadmap.md) and the duplicate checklist in the root `README.md`. Small, but it is the repository's own definition of "v1 is done" — leaving it unticked leaves v1 formally incomplete.

**Scope**
- Tick each of the six `- [ ]` items in [06](../06-scope-and-roadmap.md) as its milestone lands, rather than all at once at the end.
- Keep the identical checklist in `README.md` in sync — it is duplicated, so it will drift unless updated in the same commit.
- Review [06](../06-scope-and-roadmap.md)'s "Explicitly out of scope for v1" section: it says these "may be revisited once the core is solid". With the core shipped, that condition is met, so the section should say what is now genuinely open for consideration versus what remains permanently out.
- If M0-1 put reordering in v1, it needs a seventh checklist line here.

**Files**
- [`docs/06-scope-and-roadmap.md`](../06-scope-and-roadmap.md)
- `README.md`

**Acceptance criteria**
- [x] All six items are ticked in [06](../06-scope-and-roadmap.md) — already true before this task; verified still true.
- [x] The `README.md` checklist matches [06](../06-scope-and-roadmap.md) exactly.
- [x] Any M0-1 reordering item is reflected in both — reordering stayed out of v1, so no seventh line; it's named as a genuinely-open post-v1 item in both.
- [x] The out-of-scope section is revisited now that its stated precondition holds — split into "genuinely open for post-v1 consideration" (reordering, per-pair scoping) versus "excluded on a design principle" (disk, syscall, UDP, protocol-level).

Landed in the same PR as M4-3, since both checklists were already ticked and matching by the time this milestone started — the only real remaining work was the out-of-scope section revisit.

**Tests**
- None. Verify by diffing the two checklists.

---

### M4-5 — Changelog entry and `v0.1.0` tag

**Status:** in progress — release PR prepared; tag push and post-tag verification are a maintainer handoff (see below).
**Roadmap item:** all six
**Depends on:** M4-4
**Blocks:** —

**Objective**
Cut the first release. Until a version is tagged, `go get github.com/jpgomesr/netchaos` resolves to a pseudo-version off `main`, which signals "not ready" to anyone evaluating the library.

**Scope**
- Write the `v0.1.0` entry in the existing `CHANGELOG.md` covering the six v1 features, the determinism contract and its limits, and the synctest usage constraints from M4-1.
- Confirm `go.mod`'s `go 1.25` directive is still what is wanted for a release: it sets the *minimum* Go version consumers need. `testing/synctest` requires 1.25, so this cannot be lowered — state that constraint in the changelog and README, since it is a real adoption barrier for anyone on an older toolchain.
- Decide the version number. `v0.1.0` signals a usable but unstable API, which matches a first release whose surface has never had external users; `v1.0.0` would commit to Go module compatibility guarantees on an API nobody has used in anger yet. `v0.1.0` is the recommendation, and the [07 — Contributing](../07-contributing.md) update in M4-3 should say the API may still change before `v1.0.0`.
- Tag and push the tag; confirm pkg.go.dev picks it up and that godoc from M4-1 renders correctly there.
- Verify the module is consumable end to end: from a scratch directory outside the repo, `go get` the tagged version and run one of the M4-2 examples against it. This catches anything that only works because it was compiled inside the repo.
- Out of scope: post-v1 features. [06](../06-scope-and-roadmap.md)'s deferred list stays deferred; the point of the tight scope was to reach exactly this milestone.

**Files**
- `CHANGELOG.md`
- `README.md` — an installation section with the minimum Go version, if not already present

**Acceptance criteria**
- [x] `CHANGELOG.md` has a complete, dated `v0.1.0` entry.
- [x] The Go 1.25 minimum is stated in both `CHANGELOG.md` and `README.md`, with `testing/synctest` given as the reason.
- [x] The version number is decided and justified in the changelog.
- [ ] The tag is pushed from `main` after the release PR merges — never by pushing directly to `main` (`AGENTS.md`). **Maintainer action**, not taken by this PR — the branch protection / never-push-to-main rule means tagging is the maintainer's call, done after merge.
- [ ] pkg.go.dev shows the tagged version with rendered godoc and examples. **Verify after tagging.**
- [ ] A scratch module outside the repo can `go get` the tag and run an example successfully. **Verify after tagging** — impossible before the tag exists and is fetchable.
- [ ] CI is green on the tagged commit for both Go 1.25 and 1.26. **Verify after tagging.**

**Tests**
- The external-consumption check is the real test: fresh directory, `go mod init`, `go get github.com/jpgomesr/netchaos@v0.1.0`, compile and run an example.
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`
