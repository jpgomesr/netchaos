# 07 — Contributing

## Current project status

netchaos is in **early design**. There is no fault-injection implementation yet — the repository consists of the root [`README.md`](../README.md), this `docs/` design-doc set, project/CI scaffolding, and a toolchain-baseline test file. The [04 — API Design](04-api-design.md) surface was frozen by [M0](tasks/m0-decisions-and-foundations.md): the open design questions that used to leave it unstable (reordering scope, fault scoping, fault granularity, determinism under concurrency) are resolved, so the API shape itself is settled ahead of implementation, even though the implementation may still surface issues the design docs didn't anticipate.

## What's useful to contribute right now

Since there's no implementation to build against yet, the highest-value contributions at this stage are about **shaping the design before code gets written**:

- **Discussion on API shape.** Does the [frozen `Network`/`Option` API](04-api-design.md) feel right for real usage? Are there ergonomics issues or naming concerns worth raising before implementation makes the surface expensive to change? (Note: the surface is frozen for v1, not immutable — a strong enough case can still reopen a decision, but that's now a deliberate change, not a pending open question.)
- **Issues describing real-world scenarios.** If you have a concrete resilience-testing scenario (a retry policy, a circuit breaker, a specific timeout behavior) that's hard to test today, describing it as an issue helps validate that the v1 scope in [06 — Scope & Roadmap](06-scope-and-roadmap.md) actually covers the cases people need.
- **Feedback on the comparison in [02 — Comparison](02-comparison.md).** If you've used Toxiproxy, gosim, Chaos Mesh, Litmus, or Antithesis and think the comparison mischaracterizes something, that's worth flagging — the positioning depends on getting these comparisons right.

## Implementation contributions

Implementation PRs (the actual `net.Conn`/`net.Listener` simulation, the fault-injection layer, the `Network` type) can now build against the frozen interfaces in [04 — API Design](04-api-design.md) — [M0](tasks/m0-decisions-and-foundations.md) closed the open questions specifically to avoid the churn of landing implementation against an API that was still being debated. See [Task breakdown](tasks/README.md) for the sequenced milestones (M1 onward) that implementation PRs should map to.

## Working method

Code contributions follow test-first development: write the test before the code it drives, confirm it fails, then implement until it passes. A pull request that adds production code without the test that motivated it will be asked to add one.

## License

netchaos is licensed under [MIT](../LICENSE). Contributions are accepted under the same license.
