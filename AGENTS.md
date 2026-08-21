# AGENTS.md

Instructions for AI coding agents working in this repository. Human-facing process docs live in [`CONTRIBUTING.md`](CONTRIBUTING.md); this file is the agent-specific complement — read both.

## Project snapshot

`netchaos` ([README](README.md)) is a Go library providing simulated `net.Conn`/`net.Listener` with deterministic fault injection. **Current stage: M1 (core simulated transport), M2 (determinism & faults), and M3 (synctest & reproducibility) done; M4 (API polish & release) not started.** The module (`github.com/jpgomesr/netchaos`, `go 1.25`) has a working `Network` type (`NewNetwork`, `Dial`/`DialContext`, `Listen`, `WithSeed`) with all three v1 faults implemented and composed: `WithLatency`, `WithPacketLoss`, and `WithPartition`/`Network.Partition`/`Network.Heal` (plus `WithPeerName`, added during M2-4 to make a dialer partition-targetable). Do not assume unimplemented work exists; check before referencing it.

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
| `.claude/commands/issue.md` | Create a GitHub issue matching `design-feedback` or `use-case-scenario` |
| `.claude/commands/architecture.md` | Check a change/question against `docs/03`, `04`, `05`, `06` |

## Issue & label conventions

Only two issue forms exist — `.github/ISSUE_TEMPLATE/design-feedback.yml` (label `design`) and `use-case-scenario.yml` (label `use-case`) — plus `needs-discussion` for anything blocking a decision. There is deliberately no `bug`/`enhancement` workflow yet, since nothing is implemented. Don't create issues or labels outside this set without the user asking.

## What not to do

- Don't write implementation code speculatively beyond what's actually asked — this is a design-stage repo on purpose.
- Don't create `docs/adr/` or `docs/specs/` — no such system exists here.
- Don't modify branch protection, repo labels, or other GitHub repo settings without being explicitly asked.
- Don't change `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, or `SECURITY.md` content without flagging it — their scope was deliberately chosen (e.g. security contact routes through GitHub private vulnerability reporting, not email).
