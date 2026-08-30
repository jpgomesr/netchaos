# M5 — v0.1.0 hardening & v0.2.0 groundwork

> See the [task index](README.md) for the milestone map and conventions.

**Covers:** no v1 checklist item — v1 shipped and is closed. This milestone is the first forward-looking one: it closes the one thing `v0.1.0`'s release left unverified, and does the groundwork that is safe to do *now*, while `docs/07-contributing.md` still says the API is "stable but not frozen until `v1.0.0`."

**What this milestone deliberately does not do:** [06 — Scope & Roadmap](../06-scope-and-roadmap.md) names exactly two items as "genuinely open for post-v1 consideration" — reordering and per-peer-pair scoping of latency/loss — and both are explicitly contingent on "real usage evidence rather than a fixed timeline." `v0.1.0` has no external users yet ([07](../07-contributing.md)), so there is no evidence to act on. `AGENTS.md` also says not to resolve the reordering question "without the maintainer." None of the tasks below decide either question; M5-2 explicitly excludes them from its own scope.

---

### M5-1 — Close out M4-5's post-tag verification

**Status:** todo
**Roadmap item:** none (release hygiene, not a v1 checklist item)
**Depends on:** [M4-5](m4-api-polish-and-release.md#m4-5--changelog-entry-and-v010-tag)
**Blocks:** —

**Objective**
`M4-5` shipped the `v0.1.0` tag but left three acceptance-criteria boxes explicitly marked "verify after tagging." The tag is now pushed (`git tag -l` shows `v0.1.0` on `main`), so all three are actionable. This task does not restate them under a new ID — completing it means going back and ticking the checkboxes in `M4-5` itself, per [the task index](README.md#task-id-scheme-and-status)'s rule that IDs are stable and never duplicated.

**Scope**
- Confirm pkg.go.dev shows the tagged version with rendered godoc and examples (checks `M4-1`'s godoc pass and `M4-2`'s examples actually render correctly outside the repo).
- From a scratch directory outside this repo: `go mod init`, `go get github.com/jpgomesr/netchaos@v0.1.0`, compile and run one of the `M4-2` examples against it. This is the only check that catches something that only worked because it was compiled inside the repo.
- Confirm CI is green on the tagged commit (`2bf8d00` or later, whichever `main` pointed to when the tag was cut) for both Go 1.25 and Go 1.26.
- Out of scope: any new release work. If any of the three checks fails, file the failure as its own issue rather than silently patching it under this task.

**Files**
- [`m4-api-polish-and-release.md`](m4-api-polish-and-release.md) — tick the three remaining `M4-5` boxes once each is verified.

**Acceptance criteria**
- [ ] pkg.go.dev renders `v0.1.0`'s godoc and examples correctly.
- [ ] A scratch module outside the repo can `go get github.com/jpgomesr/netchaos@v0.1.0` and run an example successfully.
- [ ] CI is green on the tagged commit for both Go 1.25 and 1.26.

**Tests**
- The scratch-module check is the real test; no repo-internal test covers "does this work when consumed externally."

---

### M5-2 — API ergonomics review before v1.0.0

**Status:** todo
**Decision:** *(not yet made — this is a decision task)*
**Roadmap item:** none — uses the time-boxed window `docs/07-contributing.md` describes: "`v0.1.0` leaves room to correct an ergonomics mistake or naming regret before committing to the stricter compatibility expectations a `v1.0.0` tag implies."
**Depends on:** —
**Blocks:** any future breaking-change PR to the frozen surface

**Objective**
That window is real but not permanent — every day `v0.1.0` is out, the cost of a breaking rename goes up. This task is a single structured pass over the frozen surface in [04 — API Design](../04-api-design.md) to catch anything worth fixing while it's still cheap, rather than leaving ergonomics review to whenever an external user happens to complain.

**Options considered**
1. **Do the review now, before any external adoption** — cheapest possible time to change a name or signature, but with the least real usage evidence to go on; the review can only be informed by netchaos's own examples and scenario tests (`example_test.go`, `M3-4`'s scenarios), not by an outside user's friction.
2. **Wait for the first external issue or usage report** — more informed, but the cost of any resulting change is already higher by then, and there's no guarantee feedback arrives before `v1.0.0` is otherwise ready to tag.

Weighing input, not a decision: option 1 costs little (it's a review, not a rewrite) and produces a recorded baseline either way — even "reviewed, no changes needed" is worth having on record before `v1.0.0`.

**Specific surface points worth checking** (not exhaustive — the review may find others):
- `WithPeerName(ctx, name)` is the one identifier in the frozen surface that isn't a `Network` method or a `With*` `Option` constructor — it takes and returns a `context.Context`. Confirm this shape still reads clearly next to the rest of the options, or whether it was only ever the least-bad fit given `Dial`'s fixed signature (see [04 — API Design](../04-api-design.md#frozen-v1-surface)'s note on why it was added).
- `Dial`/`DialContext` symmetry — both are shipped; confirm nothing about their relationship reads as surprising now that both have real callers in the example/scenario tests.
- `Partition`/`Heal`'s silent-no-op semantics on unregistered or non-partitioned peers (["Error and no-op behaviour"](../04-api-design.md#error-and-no-op-behaviour)) — confirm this still feels right now that there's a body of test code exercising it, not just the design-time argument for it. **Input from [M6-9](m6-review-findings.md#m6-9--cross-reference-the-ephemeral-dialer-caveat-from-partition-and-heal):** the review found the sharpest version of this case — an ephemeral dialer composes with the silent no-op so that `Partition("client", "server")` returns cleanly while traffic keeps flowing. `M6-9` documented the caveat and deliberately left the semantics question here; whether `Partition` should surface a diagnostic instead is this review's call to make.

**Explicitly out of scope**
- Deciding reordering or per-peer-pair scoping — both stay gated on external usage evidence per [06 — Scope & Roadmap](../06-scope-and-roadmap.md); this review does not touch either.
- Landing any signature change directly. [07 — Contributing](../07-contributing.md) already requires an issue-first process for any change to an exported signature; this task's output feeds that process, it doesn't bypass it.

**Decision required**
Whether anything on the frozen surface should change before `v1.0.0`, and if so, what — recorded as an issue (or issues) per the `M0` precedent's `needs-discussion` label, not landed directly.

**Where the decision gets recorded**
- A `needs-discussion`-labeled issue per finding, or a single issue if the review turns up nothing — either way, the outcome should be linked back here.
- [07 — Contributing](../07-contributing.md), if the review changes the guidance about how much room `v0.1.0` actually has left.

---

### M5-3 — Decide on bug/enhancement issue-template forms

**Status:** todo
**Decision:** *(not yet made — this is a decision task; adding templates is a repo-settings-adjacent change `AGENTS.md` says not to do without being asked)*
**Roadmap item:** the gap `AGENTS.md`'s "Issue & label conventions" section names directly: "with v1 implemented... bug reports are now a real, expected category this set doesn't cover — that's a genuine gap, not a deliberate omission."
**Depends on:** —
**Blocks:** —

**Objective**
`.github/labels.yml` already defines `bug` and `enhancement` labels (retained GitHub defaults), but `.github/ISSUE_TEMPLATE/` only has `design-feedback.yml` and `use-case-scenario.yml` — there is no structured form that applies either label. Reporters currently have to pick the closest-fitting existing form or open a blank issue. This task decides whether that gap gets closed, and drafts the fix, without landing it unilaterally.

**Options considered**
1. **Add `bug.yml` and `enhancement.yml` forms**, matching the existing two templates' structure (see `.github/ISSUE_TEMPLATE/design-feedback.yml` for the house style: YAML issue form, not a Markdown template). Closes the gap `AGENTS.md` already flags as real.
2. **Leave it as-is** — a blank/default issue is still usable for bug reports; adding structure has a cost (another form to keep in sync, another thing contributors have to pick correctly).

Weighing input, not a decision: `AGENTS.md` itself calls the current state "a genuine gap," which leans toward option 1, but the same file is explicit that adding templates is the maintainer's call, not something to do as a side effect of other work.

**Decision required**
Add the two forms or not — the maintainer's call, per `AGENTS.md`'s "What not to do" section.

**Where the decision gets recorded**
- `.github/ISSUE_TEMPLATE/bug.yml` and `enhancement.yml` — drafted for review, not merged until accepted.
- `AGENTS.md`'s "Issue & label conventions" section — update once decided, either to list the two new forms or to explicitly note the gap was considered and left as-is.

**Tests**
- None — a GitHub issue-form change has no automated test; verify by opening a test issue against the draft form in a fork or draft PR before merging.
