Take the current changes (or a change already committed on a branch) through this repo's PR workflow: branch → Conventional Commit → push → PR filling `.github/PULL_REQUEST_TEMPLATE.md` in full. This chains what `/commit` already does with the repo-specific rules layered on top.

`main` requires 1 approving review to merge (branch protection). Never commit or push directly to `main` — always go through a branch and a PR.

## Steps

1. **Check the current branch.**
   - Run `git fetch origin` and `git diff HEAD origin/main --stat`.
   - If the current branch has no divergence from `origin/main`, it's safe to branch from here.
   - Otherwise, create the new branch from `origin/main`, not from a branch carrying unrelated committed history — one concern per PR.
   - Branch name: `<type>/<kebab-slug>` matching the commit type, e.g. `git checkout -b docs/api-design-clarification`.

2. **Commit the changes** following the exact same rules as `.claude/commands/commit.md`: analyze staged/unstaged diffs, group by scope, Conventional Commits format, explicit `git add <path>` (never `-A`/`.`), present the proposed commit(s) and get confirmation before committing. Use `Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>` as the only trailer.

3. **Push:** `git push -u origin <branch>`.

4. **Draft the PR body using every section of `.github/PULL_REQUEST_TEMPLATE.md`** — read that file first, do not paraphrase or drop sections. Fill in:
   - `What changed and why`: concrete summary of the change and its motivation.
   - `Related design doc`: link the relevant `docs/0X-*.md` file if this is a design-shaping change; delete the section otherwise (per the template's own instruction).
   - `Checklist`: check only items you actually verified locally (`go build ./...`, `go vet ./...`, `go test -race ./...`, `gofmt -l .`) — don't check a box you didn't act on. If there's no Go code touched by this PR, say so instead of checking boxes for commands that had nothing to run against.
   - If a related issue exists (from `/issue`), reference it in the body (e.g. `Refs #<number>`) even though the template has no dedicated field for it — this repo doesn't require every PR to close an issue.

5. **Present the full PR title + body** and get confirmation before creating it — opening a PR is a visible, shared-state action.

6. **On confirmation**, create it:
   ```bash
   gh pr create --title "<type>: <summary>" --body "$(cat <<'EOF'
   <full template, filled in>
   EOF
   )"
   ```

7. **Report** the PR URL, and remind the user that once a review is requested, pushing new commits (rather than force-pushing/amending) is expected — `dismiss_stale_reviews` is on, so any new push resets an existing approval regardless.

## Constraints

- Never push or commit directly to `main`.
- Never skip a section of the PR template.
- Never use `--no-verify`, force-push, or skip the commit confirmation step.
- Never check a template checklist item that wasn't actually done.
