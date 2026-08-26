Create a GitHub issue for this repository using `gh issue create`, always matching one of the forms in `.github/ISSUE_TEMPLATE/` — never a blank/freeform issue (blank issues are disabled at the repo level).

## Steps

1. **Determine the issue type** from the conversation context. If it isn't obvious, ask the user which of these it is:
   - **Design feedback** → mirrors `.github/ISSUE_TEMPLATE/design-feedback.yml` (label: `design`) — feedback on the API, architecture, or scope described in `docs/`.
   - **Use-case scenario** → mirrors `.github/ISSUE_TEMPLATE/use-case-scenario.yml` (label: `use-case`) — a real resilience-testing scenario that's hard to test today, used to validate v1 scope.

   There is no bug-report or feature-request template yet (see `docs/07-contributing.md`) — a genuine gap now that v1 is implemented, not a deliberate one; adding templates is a repo-settings change, so don't add one without the user asking. If the request doesn't fit either type above, say so and ask the user how to proceed rather than forcing it into one of these forms.

2. **Read the matching template file** before drafting — field order and required fields must match. `gh issue create --body` does not render the GitHub issue form, so reproduce the template's fields as plain Markdown headings in the body, in the same order.

3. **Draft the issue** — a plain-language title and a body filling in every field from the template, based on the actual feedback/scenario discussed. Do not invent scope beyond what the user described. Apply the `needs-discussion` label in addition to the type label if the issue surfaces an open question that blocks a design decision.

4. **Present the draft** (title, labels, full body) and ask for confirmation before creating anything.

5. **On confirmation**, create it:
   ```bash
   gh issue create --title "<summary>" --label <design|use-case>[,needs-discussion] --body "$(cat <<'EOF'
   <body matching the template's fields>
   EOF
   )"
   ```

6. **Report back** the issue URL and number.

## Constraints

- Never create a blank/freeform issue — always follow one of the two templates above.
- Never skip the confirmation step.
- Never label an issue `bug` or `enhancement` — no such labels exist in this repo yet (see step 1); use `design` or `use-case` instead, even for reports about implemented behavior, until those labels/templates are actually added.
