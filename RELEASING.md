# Releasing netchaos

How a netchaos version is cut, written down from how `v0.3.1` was released. A published version is permanent: once anyone has fetched it, the Go module proxy and checksum database keep it even if the tag is later deleted. Do the checks before the tag, not after.

## 1. Finish the milestone

Every task in the milestone's `docs/tasks/mN-*.md` file is merged, and on the commit to be tagged:

- CI is green, including the Go 1.25 leg, macOS/Windows, 32-bit and the conformance module;
- `govulncheck` is green for both modules (see [docs/07 § Go version support](docs/07-contributing.md#go-version-support) for what a finding in `golang.org/x/net` means);
- the **API compatibility** job's report matches the version you are about to cut (step 3).

## 2. Close-out PR

One PR, mirroring the previous close-outs (`#71`, `#103`, `#125`):

- `CHANGELOG.md`: `## [Unreleased]` becomes `## [vX.Y.Z] — YYYY-MM-DD`, with a short summary paragraph, and a fresh empty `## [Unreleased]` above it.
- `README.md` and `.claude/skills/netchaos/`, **together**: the status banner, every `go get …@vX.Y.Z`, and anything the release changed that consumers need. Per [AGENTS.md](AGENTS.md), these two describe the last tagged release and are updated only here.
- `AGENTS.md`: the project snapshot (latest release, milestone range).
- `docs/tasks/`: mark the milestone and its close-out task done in the milestone file and the [task index](docs/tasks/README.md).

## 3. Choose the version

Read the **API compatibility** job (`gorelease`) on the close-out PR. It lists every added, changed or removed exported identifier and suggests a version.

- **Patch** (`v0.3.1` → `v0.3.2`): bug fixes, docs, CI, tests. No exported identifier changes, no documented behaviour changes.
- **Minor** (`v0.3.x` → `v0.4.0`): new identifiers, or — before `v1.0.0` only — breaking changes. Any raise of the Go floor is at least a minor ([docs/07](docs/07-contributing.md#go-version-support)).
- **`v1.0.0`**: the maintainer's usage-evidence-driven call ([docs/07](docs/07-contributing.md)). At that point, remove `continue-on-error: true` from `.github/workflows/api-compat.yml` so an incompatible change fails the PR from then on.

## 4. Tag

Tags are lightweight (no message), on the merge commit of the close-out PR — or on a later docs-only commit on `main` if one landed first:

```
git fetch origin
git tag vX.Y.Z <commit>
git push origin vX.Y.Z
```

Alternatively create the tag from the GitHub UI in step 5 ("Choose a tag" → new tag, target `main`) when `main` is at the intended commit.

## 5. GitHub Release

Title `vX.Y.Z`. The release notes follow the previous releases' shape:

- an opening paragraph: what the release builds on, which milestone, tasks/PRs and issues it covers;
- `### Requirements` (the Go floor);
- `### Added` / `### Changed` / `### Fixed` (and `### Documentation` if useful), each entry bolded with its `(closes #N)`;
- `### Upgrade note`: what, if anything, a consumer must change, plus any known issue;
- `### Versioning note`: why this is a patch/minor and not `v1.0.0`.

## 6. Verify

From a scratch directory outside the repo:

```
go mod init smoke
go get github.com/jpgomesr/netchaos@vX.Y.Z
go list -m github.com/jpgomesr/netchaos   # prints vX.Y.Z
```

then build and run a few lines using `NewNetwork`/`Listen`/`Dial`. Check that `https://pkg.go.dev/github.com/jpgomesr/netchaos@vX.Y.Z` renders (it can take a few minutes to appear; requesting the page triggers the fetch).

## 7. Afterwards

- Tick or close the issues the release covers, if their PRs didn't.
- Start the next milestone's task file when its first PR goes up.
