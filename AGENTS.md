# AGENTS.md

Instructions for AI coding agents working in this repository. Human-facing process docs live in [`CONTRIBUTING.md`](CONTRIBUTING.md); this file is the agent-specific complement — read both.

## Project snapshot

`netchaos` ([README](README.md)) is a Go library providing simulated `net.Conn`/`net.Listener` with deterministic fault injection. **Current stage: M1-M8 implemented and godoc'd; tagged and released as `v0.2.0`** (2026-08-31), on top of `v0.1.0`'s three-fault core, plus four bug fixes and two tooling additions landed on `main` since that tag (`M8-1`..`M8-6`). The module (`github.com/jpgomesr/netchaos`, `go 1.25`) has a working `Network` type (`NewNetwork`, `Dial`/`DialContext`/`DialerFor`, `Listen`, `WithSeed`) with the full shipped fault set implemented and composed in one fixed order: `WithLatency`, `WithPacketLoss`, `WithBandwidth`, `WithDuplication`, `WithCorruption`, and `WithPartition`/`Network.Partition`/`Network.Heal` (plus `WithPeerName`, added during v0.1.0 to make a dialer partition-targetable), alongside `Network.Reset` (imperative mid-stream reset), the live setters `Network.SetLatency`/`Network.SetPacketLoss`, the exported `Network.Trace`, and the tuning options `WithPipeBound`/`WithListenerBacklog`. Sentinel errors live in `errors.go` (`ErrUnsupportedNetwork`, `ErrConnectionRefused`, `ErrAddressInUse`, `ErrBacklogFull`). The API is stable but not frozen until `v1.0.0` — the M5-2 ergonomics review the v0.2.0 surface never got (`docs/04-api-design.md`, issue #75) ran as `M8-7` and is now decided (see `docs/tasks/m8-v1-readiness.md`'s "Review outcome"); [M9 — v1.0.0 surface additions](docs/tasks/m9-v1-surface-additions.md) implements what it accepted (`#83`, `#86`, `#78` in part, `#85` in part) and is the remaining gate before `v1.0.0`. See [docs/07-contributing.md](docs/07-contributing.md). Check `CHANGELOG.md` and the repo's tags for the current release state; do not assume unimplemented work exists.

## Source of truth for design

The [`docs/`](docs/README.md) set (`01-vision.md` through `07-contributing.md`) is the only design authority — there is no ADR or spec system in this repo. When implementing or discussing design:

- Ground every claim in a specific doc section or in existing code — never invent rationale. If nothing covers a question, say so explicitly rather than guessing.
- The three open design questions gating implementation (reordering scope, fault scoping, fault granularity/determinism) were resolved as [M0](docs/tasks/m0-decisions-and-foundations.md) and recorded in `docs/03-06`. Reordering is **out of v1**; don't resolve it differently or reopen it without the maintainer.
- Use `/architecture` (`.claude/commands/architecture.md`) to check a change against these docs before assuming consistency.

## Test-first (red → green)

Every code task follows a strict test-first cycle: write the test before the production code it drives, run it and confirm it fails (red) — a compile error because the identifier doesn't exist yet counts — then write the smallest change that makes it pass (green). A commit that adds production code must contain the test that motivated it; the test must never land in a later commit. Never skip observing the red — a test only ever seen green may be asserting nothing, and that defect is invisible afterwards.

## Build & verify

Run before considering any Go change complete (matches CI in `.github/workflows/`):

```
go build ./...
go vet ./...
gofmt -l .        # must print nothing
go test -race ./...
golangci-lint run # config: .golangci.yml
```

`-race` requires `CGO_ENABLED=1` — on some local setups (e.g. Windows without a C toolchain) this fails locally even though it works on CI's `ubuntu-latest` runners. Don't treat a local `-race` failure as a code problem without checking whether cgo is actually available first.

## Git workflow

- Conventional Commits (`type: summary` or `type(scope): summary`), matching existing `git log` history.
- Stage files explicitly (`git add <path>`) — never `-A` or `.`.
- **Never commit or push directly to `main`.** Branch protection requires 1 approving review, blocks force-push, and dismisses stale approvals on new commits — always work on a branch and open a PR.
- Never use `--no-verify`, and never amend a commit that's already been pushed/reviewed.
- Full procedures: `.claude/commands/commit.md` (commit only) and `.claude/commands/pr.md` (branch → commit → push → PR, including the `.github/PULL_REQUEST_TEMPLATE.md` checklist).

## Available agent commands

| Command | Purpose |
|---|---|
| `.claude/commands/commit.md` | Propose and create Conventional Commits from the current diff |
| `.claude/commands/pr.md` | Full branch → commit → push → PR flow |
| `.claude/commands/issue.md` | Create a GitHub issue matching one of the four forms in `.github/ISSUE_TEMPLATE/` |
| `.claude/commands/architecture.md` | Check a change/question against `docs/03`, `04`, `05`, `06` |
| `.claude/skills/netchaos/` (`SKILL.md` + `references/api.md`) | Self-contained usage reference for consumers of the published package — see "Keeping design docs and the skill in sync" below for when this gets updated |

## Issue & label conventions

Four issue forms exist in `.github/ISSUE_TEMPLATE/` — `bug.yml` (label `bug`), `enhancement.yml` (`enhancement`), `design-feedback.yml` (`design`) and `use-case-scenario.yml` (`use-case`) — plus `needs-discussion` for anything blocking a decision. `bug` and `enhancement` were added under [M5-3](docs/tasks/m5-hardening-and-ergonomics.md#m5-3--decide-on-bugenhancement-issue-template-forms), closing the gap this section used to describe as open.

Priority is a separate axis from type: `critical` / `high` / `medium` / `low` say how urgent an issue is, independent of whether it's a `bug`, `enhancement`, or `design` item. Apply at most one.

Two things to keep straight. Behaviour the docs describe as deliberate is `design`, not `bug` — the fault model in [docs/05](docs/05-fault-injection.md) and the no-op/panic semantics in [docs/04](docs/04-api-design.md#error-and-no-op-behaviour) are design decisions, not defects. And blank issues are disabled (`.github/ISSUE_TEMPLATE/config.yml`), so every issue goes through a form or through Discussions. Don't create issues, or labels outside `.github/labels.yml`, without the user asking.

## Keeping design docs and the skill in sync

A PR that changes the exported surface or a fault's semantics updates, in the **same PR**: the godoc on the changed identifier, `docs/04-api-design.md` (and `docs/05-fault-injection.md` if fault semantics moved), and `doc.go`'s determinism-contract comment if the change touches the ordered-calls list. This is existing practice, not a new rule — every `M7` task did it, and `#89`'s address-handling fix is a clear single-PR example.

`README.md` and `.claude/skills/netchaos/` are the exception: both describe the **last tagged release** by design (the skill's own `go get …@v0.2.0` instruction, README's "Status: `v0.2.0` released" banner), and git history confirms neither has been touched by any post-`v0.2.0` fix — they are updated together, once, in a milestone-close PR (`#71`, `#90`), not per feature PR. Don't update them for a change that hasn't shipped in a tag yet; do flag in the relevant milestone's task file when a close-out pass is due, the way [M9-5](docs/tasks/m9-v1-surface-additions.md#m9-5--close-out-m9-sync-readmemd-and-the-skill) does.

## What not to do

- Don't write implementation code speculatively beyond what's actually asked, or add scope beyond a task's stated boundaries — v1 is deliberately narrow (see [docs/06-scope-and-roadmap.md](docs/06-scope-and-roadmap.md)), and that discipline doesn't end once the core ships.
- Don't create `docs/adr/` or `docs/specs/` — no such system exists here.
- Don't modify branch protection, repo labels, or other GitHub repo settings without being explicitly asked.
- Don't change `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, or `SECURITY.md` content without flagging it — their scope was deliberately chosen (e.g. security contact routes through GitHub private vulnerability reporting, not email).
