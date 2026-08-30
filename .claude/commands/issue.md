Create a GitHub issue for this repository using `gh issue create`, always matching one of the forms in `.github/ISSUE_TEMPLATE/` — never a blank/freeform issue (blank issues are disabled at the repo level).

## Steps

1. **Determine the issue type** from the conversation context. If it isn't obvious, ask the user which of these it is:
   - **Bug report** → mirrors `.github/ISSUE_TEMPLATE/bug.yml` (label: `bug`) — netchaos behaves differently from a real `net.Conn`/`net.Listener` in a way `docs/` doesn't describe as a deliberate trade-off. Always capture the seed and whether the failure is inside a `testing/synctest` bubble; a fault-dependent bug can't be replayed without them.
   - **Enhancement** → mirrors `.github/ISSUE_TEMPLATE/enhancement.yml` (label: `enhancement`) — a proposed addition or change: a new fault kind, an option, an ergonomic improvement. Check it against `docs/06-scope-and-roadmap.md`'s rubric first; several plausible ideas are already excluded there with reasons.
   - **Design feedback** → mirrors `.github/ISSUE_TEMPLATE/design-feedback.yml` (label: `design`) — feedback on the API, architecture, or scope described in `docs/`, rather than a defect or a request.
   - **Use-case scenario** → mirrors `.github/ISSUE_TEMPLATE/use-case-scenario.yml` (label: `use-case`) — a real resilience-testing scenario that's hard to test today, used to validate scope.

   The distinction that actually matters: behaviour the docs describe is `design`, not `bug`. If the request doesn't fit any type above, say so and ask the user how to proceed rather than forcing it into one of these forms.

2. **Read the matching template file** before drafting — field order and required fields must match. `gh issue create --body` does not render the GitHub issue form, so reproduce the template's fields as plain Markdown headings in the body, in the same order.

3. **Draft the issue** — a plain-language title and a body filling in every field from the template, based on the actual feedback/scenario discussed. Do not invent scope beyond what the user described. Apply the `needs-discussion` label in addition to the type label if the issue surfaces an open question that blocks a design decision.

4. **Present the draft** (title, labels, full body) and ask for confirmation before creating anything.

5. **On confirmation**, create it:
   ```bash
   gh issue create --title "<summary>" --label <bug|enhancement|design|use-case>[,needs-discussion] --body "$(cat <<'EOF'
   <body matching the template's fields>
   EOF
   )"
   ```

6. **Report back** the issue URL and number.

## Constraints

- Never create a blank/freeform issue — always follow one of the four templates above.
- Never skip the confirmation step.
- Never apply a label outside the set in `.github/labels.yml`, and never create a new one — label definitions are synced from that file, so an ad-hoc label is deleted or drifts on the next sync.
