Analyze all staged and unstaged changes in the repository, then propose a set of Conventional Commits grouped by scope.

## Steps

1. **Collect changes**
   - Run `git status` to see staged, unstaged, and untracked files.
   - Run `git diff` (unstaged) and `git diff --cached` (staged) to read the full diffs.
   - For untracked files that appear relevant (new docs, config, code), read their content before proposing a commit — do NOT stage them automatically.
   - If the working tree has no changes at all, report that and stop.

2. **Group by scope**
   - Identify logical scopes from the changed paths (e.g. `docs`, `ci`, `api`, `contrib` for CONTRIBUTING/CODE_OF_CONDUCT/SECURITY, `.claude` for command/skill config).
   - Group related files into the same commit when they form a single coherent change.
   - Keep unrelated changes in separate commits.

3. **Draft commit messages**
   - Follow this repo's existing convention (see `git log`): `<type>: <summary>` or `<type>(<scope>): <summary>` — e.g. `docs: enhance README with project details and usage`.
   - Valid types: `feat`, `fix`, `chore`, `refactor`, `docs`, `test`, `ci`, `build`, `perf`, `style`.
   - Summary: imperative mood, lowercase, no trailing period, ≤72 chars.
   - If a change introduces a breaking change to the exported API, append `!` after the type and include a `BREAKING CHANGE:` footer — the API is stable but not frozen until `v1.0.0` (see `docs/07-contributing.md`), so this is a real possibility now, not a hypothetical.

4. **Present the proposal**
   - Show a numbered list of proposed commits, each with the full commit message and the files it covers.
   - Ask clearly for confirmation before proceeding. Do NOT create any commit until confirmed.

5. **On confirmation**
   - For each proposed commit (in order):
     - Stage only the relevant files explicitly: `git add <file1> <file2> ...`
     - Commit using a heredoc to preserve formatting, with `Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>` as the only trailer:

       ```
       git commit -F - <<'EOF'
       docs: add contributing guide

       Co-Authored-By: Claude <noreply@anthropic.com>
       EOF
       ```

     - If `git commit` fails (e.g. rejected by a hook), report the error output exactly as-is and stop — do NOT retry with `--no-verify` or work around the hook.
   - After all commits, run `git status` to confirm the working tree is clean.

6. **On rejection or correction**
   - Revise the proposal based on the feedback, re-present it, and ask for confirmation again before committing.

## Constraints

- Never skip the confirmation step — not even when there is only one file changed.
- Never use `git add .` or `git add -A` — always add files explicitly by path.
- Never use `--no-verify` to bypass hooks.
- Never amend published commits (this repo requires 1 approval on `main` — an amended, already-reviewed commit invalidates that review).
- Never commit files that look like secrets (`.env`, credentials, private keys). If found, warn and exclude them from all proposals.
