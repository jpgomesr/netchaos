# M5 — v0.1.0 hardening & v0.2.0 groundwork

> See the [task index](README.md) for the milestone map and conventions.

**Covers:** no v1 checklist item — v1 shipped and is closed. This milestone is the first forward-looking one: it closes the one thing `v0.1.0`'s release left unverified, and does the groundwork that is safe to do *now*, while `docs/07-contributing.md` still says the API is "stable but not frozen until `v1.0.0`."

**What this milestone deliberately does not do:** [06 — Scope & Roadmap](../06-scope-and-roadmap.md) names exactly two items as "genuinely open for post-v1 consideration" — reordering and per-peer-pair scoping of latency/loss — and both are explicitly contingent on "real usage evidence rather than a fixed timeline." `v0.1.0` has no external users yet ([07](../07-contributing.md)), so there is no evidence to act on. `AGENTS.md` also says not to resolve the reordering question "without the maintainer." None of the tasks below decide either question; M5-2 explicitly excludes them from its own scope.

---

### M5-1 — Close out M4-5's post-tag verification

**Status:** done
**Roadmap item:** none (release hygiene, not a v1 checklist item)
**Depends on:** [M4-5](m4-api-polish-and-release.md#m4-5--changelog-entry-and-v010-tag)
**Blocks:** —

**Objective**
`M4-5` shipped the `v0.1.0` tag but left acceptance-criteria boxes open pending the tag. The tag is on the remote (`git ls-remote --tags origin` → `refs/tags/v0.1.0` at `bd8e198`), so they are all actionable. This task does not restate them under a new ID — completing it means going back and ticking the checkboxes in `M4-5` itself, per [the task index](README.md#task-id-scheme-and-status)'s rule that IDs are stable and never duplicated.

**Four boxes, not three.** `M4-5` left *four* unticked, not the three marked "verify after tagging." The fourth is the tag-push box, marked **Maintainer action** rather than "verify after tagging" — it is now factually satisfied, and leaving it orphaned would hold `M4-5` at `in progress` forever. A lightweight tag records no pusher, so the verifiable form of "pushed from `main`" is that `bd8e198` is an ancestor of `main` and `origin` carries the tag pointing at it.

**Scope**
- **Run the external `go get` before judging pkg.go.dev.** pkg.go.dev indexes on proxy demand: if nothing has ever fetched `v0.1.0`, the page can legitimately 404, and filing that as a defect would be a false positive. The `go get` primes `proxy.golang.org` and triggers indexing.
- From a scratch directory outside this repo: `go mod init`, `go get github.com/jpgomesr/netchaos@v0.1.0`, compile and run one of the `M4-2` examples against it. This is the only check that catches something that only worked because it was compiled inside the repo. Note the `Example*` functions live in `example_test.go` and are therefore **not importable** — the body has to be transcribed into the scratch module by hand.
- Confirm pkg.go.dev shows the tagged version with rendered godoc and examples (checks `M4-1`'s godoc pass and `M4-2`'s examples actually render correctly outside the repo).
- Confirm CI is green on the tagged commit for both Go 1.25 and Go 1.26. The tagged commit is **`bd8e198`**, not `2bf8d00` — `2bf8d00` ("mark AGENTS.md project snapshot as tagged `v0.1.0`") landed *after* the tag and describes it retroactively. And `ci.yml` has no `tags:` trigger, so the run to check is the `main`-push run for `bd8e198`.
- Out of scope: any new release work. If any check fails, file the failure as its own issue rather than silently patching it under this task.

**Files**
- [`m4-api-polish-and-release.md`](m4-api-polish-and-release.md) — tick the four remaining `M4-5` boxes once each is verified, and move `M4-5` to `done`.

**Acceptance criteria**
- [x] The `v0.1.0` tag is confirmed present on `origin`, pointing at a commit that is an ancestor of `main`.
- [x] A scratch module outside the repo can `go get github.com/jpgomesr/netchaos@v0.1.0` and run an example successfully.
- [x] pkg.go.dev renders `v0.1.0`'s godoc and examples correctly.
- [x] CI is green on the tagged commit for both Go 1.25 and 1.26.

**Tests**
- The scratch-module check is the real test; no repo-internal test covers "does this work when consumed externally."

**Result** — all four checks passed:
- `go get github.com/jpgomesr/netchaos@v0.1.0` resolved to the tag itself, not a pseudo-version off `main`, and the transcribed `Example` / `ExampleNetwork_Dial` bodies printed `ping` and `true`.
- Independently, [`jpgomesr/netchaos-example`](https://github.com/jpgomesr/netchaos-example) already requires `github.com/jpgomesr/netchaos v0.1.0` exactly. It is a real external consumer of the tag, which is a stronger signal than a throwaway module — worth keeping in mind as a build-level canary for future releases.
- pkg.go.dev renders `v0.1.0` with all six `Example*` functions and the full 17-symbol frozen surface in its index.
- CI run `32998417916` on `bd8e198`: `test (1.25)` and `test (1.26)` both `success`. `golangci-lint` and CodeQL were also green on the same commit, as separate workflows.

---

### M5-2 — API ergonomics review before v1.0.0

**Status:** done
**Decision:** **One doc correction landed directly; two questions raised as `needs-discussion` issues; nothing else on the frozen surface changes before `v1.0.0`.** See "Review outcome" below.
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
- A `needs-discussion`-labeled issue per finding, or a single issue if the review turns up nothing — either way, the outcome should be linked back here. Filed: [#36](https://github.com/jpgomesr/netchaos/issues/36) (F2, cross-linked from `M6-9`) and [#37](https://github.com/jpgomesr/netchaos/issues/37) (F4, cross-linked from `M6-10`). These are the repository's first issues.
- [07 — Contributing](../07-contributing.md), if the review changes the guidance about how much room `v0.1.0` actually has left.

---

## Review outcome

The review covered M5-2's own three points plus the findings [M6](m6-review-findings.md) routed into it (`M6-9`, `M6-10`, `M6-17`). Findings in descending order of how much they matter.

### F1 — `04`'s partition-establishment paragraph was factually wrong (fixed here)

[04 — API Design](../04-api-design.md#dynamic-partition-control) said: *"`Dial` (which uses `context.Background()`) therefore hangs forever against a partitioned peer; give it a context with a deadline if that's not the intended behaviour."*

`Dial` does not hang. It connects immediately. The claim was self-contradicted by the very next sentence in the same paragraph — *"Only a dialer that named itself via `WithPeerName` can be blocked this way"* — and `Dial` cannot name itself, because `WithPeerName` records the identity on a `context.Context` and `Dial` has no context parameter. The suggested remedy was impossible for the same reason: there is nowhere to put a deadline.

Verified against the published `v0.1.0` from an external module, not the working tree:

```
1. Dial, with partition client<->server already declared
    CONNECTED, LocalAddr=ephemeral:0
2. DialContext + WithPeerName("client"), same partition
    HUNG (still blocked after 500ms)
```

This is the same class as [`M6-1`](m6-review-findings.md#m6-1--reconcile-ordinal-assignment-with-the-determinism-contract) — a doc sentence overreaching past what the code does, on the frozen surface. Corrected in this task rather than deferred: it is prose only, no signature moves, so [07](../07-contributing.md)'s issue-first rule does not apply. `DialContext`'s godoc (`netchaos.go:138-143`) was checked and is already accurate; `Partition`'s (`partition.go:56-58`) attributes the blocking to `WithPeerName` and is not wrong, though `M6-9` may sharpen it.

### F2 — `Dial` cannot participate in partition at all *(issue — the substantive finding)*

F1's mechanism is the real finding, and it is not a documentation problem. `Dial` is the `net.Dial`-shaped drop-in the library's adoption claim rests on — [01 — Vision](../01-vision.md)'s no-rewrite promise — and it is *structurally incapable* of being partition-targetable, because peer identity travels by context and `Dial` has no context. A user who wants the headline partition feature must abandon the drop-in entry point.

This subsumes M5-2's point (b), "`Dial`/`DialContext` symmetry": the asymmetry is not stylistic, it is a capability gap. It also reframes point (a): `WithPeerName`'s context-carried shape is defensible as the least-bad fit for `Dial`'s frozen signature (as [04](../04-api-design.md#frozen-v1-surface) argues), but F2 is the cost that argument was paying, and it was never written down.

Raised as a `needs-discussion` issue rather than decided — it would add an exported identifier: [#36](https://github.com/jpgomesr/netchaos/issues/36).

### F3 — `Partition` on an unnamed dialer is a silent no-op *(input from `M6-9`, folded into F2's issue)*

`M6-9` recorded this and routed the semantics decision here. Reproduced empirically, same run:

```
3. Dial (unnamed), then Partition("client","server"), then send
    dialer identity is ephemeral:0 - not "client"
    read after Partition: "ping" DELIVERED - partition had no effect
```

**Decision: the silent no-op stays.** [04](../04-api-design.md#error-and-no-op-behaviour)'s argument holds — partitioning before either side connects is legitimate test setup ("start partitioned"), and erroring would force tests to order setup calls for no benefit. There is no way for `Partition` to distinguish "peer not connected yet" from "peer will never exist" without breaking that. The composed failure is real, but its cause is F2, and that is where the fix belongs. `M6-9`'s own godoc cross-reference remains worth doing on its own terms.

### F4 — Addresses have no `host:port` structure *(input from `M6-10`, its own issue)*

`peerName` is the identity function (`addr.go:32`), so `addr.String()` returns the peer name verbatim and `net.SplitHostPort` fails against netchaos where it succeeds against the real stack. Confirmed unchanged. `M6-10` asked for a `needs-discussion` issue cross-linked to this review: [#37](https://github.com/jpgomesr/netchaos/issues/37). Not decided here — it is a breaking change to every address string a test prints, so it belongs to the issue-first process.

### F5 — `M6-17`'s gate: **not blocked by this review**

`M6-17` asked whether `WithPipeBound` / `WithListenerBacklog` should wait for this review to conclude, on the grounds that they cost "two more names on a surface `M5-2` is about to review for being too large."

**The premise does not survive the review.** The frozen surface's problem is not that it is too large — it is that one entry point is missing a capability (F2). Option count is not the binding constraint, so `M6-17` should be decided on its own merits, now, without waiting. Recorded on `M6-17` itself.

### F6 — Panic on invalid option values: confirmed, no change

[04](../04-api-design.md#error-and-no-op-behaviour) said this was "decided now, not deferred, because reversing it after v1 tags is expensive." Re-examined while reversal is still cheap: the reasoning holds. It matches `regexp.MustCompile`, invalid values are programmer errors in test code, and `options.go:37-48` documents the one non-obvious consequence (validation runs once after all options apply, so a later valid option rescues an earlier invalid one of the same kind).

### `07 — Contributing` needs no change

Its guidance — the API is "stable but not frozen until `v1.0.0`," and a breaking change "needs a real justification, not routine churn" — is exactly the process F2 and F4 now enter. The review found nothing that changes how much room `v0.1.0` has left; it found two things worth spending some of that room on, which is the guidance working rather than a reason to rewrite it.

### Not examined, deliberately

Reordering (`M6-15`) and per-peer-pair scoping (`M6-16`) stay gated on external usage evidence; `AGENTS.md` bars resolving reordering without the maintainer. Error-wrapping policy (`M6-2`) and exporting fault observability (`M6-14`) touch the frozen surface and therefore inherit this task's `Blocks:` edge, but each has its own M6 decision task and is not folded in here.

`api_test.go` claims in a comment to assert "the frozen v1 API surface is honoured" but only checks `Dial`'s assignability and `net.Conn` conformance — there is no mechanical guard against an accidental exported-surface change. Noted as test tooling rather than ergonomics; it belongs in M6, not here.

---

### M5-3 — Decide on bug/enhancement issue-template forms

**Status:** done
**Decision:** **Add both forms** — the maintainer's call, taken 2026-08-30. Option 1 below.
**Roadmap item:** the gap `AGENTS.md`'s "Issue & label conventions" section names directly: "with v1 implemented... bug reports are now a real, expected category this set doesn't cover — that's a genuine gap, not a deliberate omission."
**Depends on:** —
**Blocks:** —

**Objective**
`.github/labels.yml` already defines `bug` and `enhancement` labels (retained GitHub defaults), but `.github/ISSUE_TEMPLATE/` only has `design-feedback.yml` and `use-case-scenario.yml` — there is no structured form that applies either label. This task decides whether that gap gets closed, and drafts the fix, without landing it unilaterally.

**Correction:** this task originally said reporters "have to pick the closest-fitting existing form or open a blank issue." Blank issues are **disabled** — `.github/ISSUE_TEMPLATE/config.yml` sets `blank_issues_enabled: false`. A bug reporter's only escape hatch today is the Discussions contact link, which strengthens option 1 rather than weakening it: the gap is not "unstructured reports," it is "no path at all."

**Options considered**
1. **Add `bug.yml` and `enhancement.yml` forms**, matching the existing two templates' structure (see `.github/ISSUE_TEMPLATE/design-feedback.yml` for the house style: YAML issue form, not a Markdown template). Closes the gap `AGENTS.md` already flags as real.
2. **Leave it as-is** — a blank/default issue is still usable for bug reports; adding structure has a cost (another form to keep in sync, another thing contributors have to pick correctly).

Weighing input, not a decision: `AGENTS.md` itself calls the current state "a genuine gap," which leans toward option 1, but the same file is explicit that adding templates is the maintainer's call, not something to do as a side effect of other work.

**Decision required**
Add the two forms or not — the maintainer's call, per `AGENTS.md`'s "What not to do" section. **Answered: add them.**

**Where the decision gets recorded**
- `.github/ISSUE_TEMPLATE/bug.yml` and `enhancement.yml` — added. `bug.yml` requires the **seed** and whether the failure is inside a `testing/synctest` bubble, because a fault-dependent report is unreplayable without the first and ambiguous between virtual and wall-clock time without the second. `enhancement.yml` points at [06](../06-scope-and-roadmap.md)'s scope rubric up front, so proposals arrive pre-filtered against the already-excluded list.
- `AGENTS.md`'s "Issue & label conventions" section — rewritten to list four forms, and to state the distinction that actually decides routing: behaviour the docs describe as deliberate is `design`, not `bug`.
- `docs/tasks/README.md`'s "On GitHub issues" section — updated; it pointed here as the open decision.

**Widened beyond the task's own file list**, because all three sit in this change's blast radius:
- `.github/ISSUE_TEMPLATE/design-feedback.yml` still said **"netchaos has no implementation yet"**, live on `main`. That falsified `M4-3`'s ticked box *"No file in the repository claims netchaos has no implementation"* — its status sweep covered prose and `.claude/commands/` but never `.github/`. It is also the house-style source a new `bug.yml` copies from, so the false banner would have propagated. `use-case-scenario.yml`'s dated "right now" / "validate the v1 scope" framing was refreshed in the same pass, and `config.yml`'s contact-link text no longer enumerates form names that go stale.
- `.claude/commands/issue.md` said *"Never label an issue `bug` or `enhancement` — no such labels exist in this repo yet."* Both labels have existed in `.github/labels.yml` all along; the rule was wrong before this task and would have been wrong twice over after it. Replaced with a rule that holds under change: never apply a label outside `labels.yml`, since labels are synced from that file and an ad-hoc one is deleted on the next sync.

**Tests**
- None — a GitHub issue-form change has no automated test.

**What was actually verified, and what was not.** All four forms were parsed and shape-checked before merge: YAML validity, `labels` referencing labels that exist in `.github/labels.yml`, unique non-empty field `id`s, and dropdown option counts. That catches a malformed file, which is the failure that matters.

The task's original Tests line called for opening a test issue against the form "in a fork or draft PR before merging." **That check was not performed.** GitHub renders issue forms from the **default branch only**, so neither this repo's chooser nor a draft PR shows them — the fork route additionally requires re-pointing a fork's default branch, and a repo cannot be forked into the account that owns it. The accepted remedy is a post-merge check of the *New issue* chooser, with a follow-up commit if a form does not render. Recorded rather than quietly substituted, because a task file that claims a verification nobody ran is the same defect this task exists to fix.
