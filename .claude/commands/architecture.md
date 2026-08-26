Check whether a proposed or already-made change is consistent with netchaos's recorded design, or answer a deeper architectural question with grounded context rather than a guess.

netchaos has no ADR system — the design is recorded in the numbered docs under `docs/` (`03-architecture.md`, `04-api-design.md`, `05-fault-injection.md`, `06-scope-and-roadmap.md`). This command only evaluates against those; it does not draft new design docs or resolve open questions on its own.

## Steps

1. Read `docs/03-architecture.md`, `docs/04-api-design.md`, `docs/05-fault-injection.md`, and `docs/06-scope-and-roadmap.md`.
2. Read the relevant code paths for the area in question — v1 is implemented, so for anything within its scope there should be real code to compare against, not just design prose.
3. Compare the change or question against what the docs actually say — quote the specific doc and section when something aligns or conflicts, don't assert from memory.
4. Report:
   - Which doc sections are relevant and what they say.
   - Whether the current/proposed change is consistent, and if not, exactly where it diverges.
   - Whether it touches something explicitly deferred post-v1 (see `docs/06-scope-and-roadmap.md#explicitly-out-of-scope-for-v1`) versus a genuinely new divergence.

## Constraints

- Never invent architectural rationale not backed by an actual doc under `docs/` or the code itself — say "no doc covers this" rather than guessing why something was designed a certain way.
- Never propose resolving an open question yourself — flag it and point to `/issue` (design-feedback template) so it gets decided through the normal process.
- Never treat this command as authority to draft or edit `docs/` — that's a manual, sign-off-required edit, not something this command automates.
