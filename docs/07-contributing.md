# 07 — Contributing

## Current project status

netchaos is in **early design**. There is no implementation yet — the repository currently consists of the root [`README.md`](../README.md), this `docs/` design-doc set, and a `LICENSE`. The API described in [04 — API Design](04-api-design.md) is a proposed sketch, not a stable contract, and is expected to change as design questions (like the [reordering open question](05-fault-injection.md#reordering-open-question)) get resolved and as real implementation work surfaces issues the design docs didn't anticipate.

## What's useful to contribute right now

Since there's no implementation to build against yet, the highest-value contributions at this stage are about **shaping the design before code gets written**:

- **Discussion on API shape.** Does the [proposed `Network`/`Option` API](04-api-design.md) feel right for real usage? Are there ergonomics issues, missing configuration knobs, or naming concerns worth raising before the interfaces stabilize and become harder to change?
- **Resolving open questions.** The [reordering scope question](06-scope-and-roadmap.md#reordering-in-or-out-of-v1) is the one currently flagged in these docs — input on whether it belongs in v1 is directly useful.
- **Issues describing real-world scenarios.** If you have a concrete resilience-testing scenario (a retry policy, a circuit breaker, a specific timeout behavior) that's hard to test today, describing it as an issue helps validate that the v1 scope in [06 — Scope & Roadmap](06-scope-and-roadmap.md) actually covers the cases people need.
- **Feedback on the comparison in [02 — Comparison](02-comparison.md).** If you've used Toxiproxy, gosim, Chaos Mesh, Litmus, or Antithesis and think the comparison mischaracterizes something, that's worth flagging — the positioning depends on getting these comparisons right.

## What will make more sense once the core stabilizes

Implementation PRs (the actual `net.Conn`/`net.Listener` simulation, the fault-injection layer, the `Network` type) will make more sense once the core interfaces in [04 — API Design](04-api-design.md) have had a chance to settle from discussion. Landing implementation against an API that's still actively being debated risks churn and wasted work — so at this stage, design-shaping input is more valuable than code.

## License

netchaos is licensed under [MIT](../LICENSE). Contributions are accepted under the same license.
