# 07 — Contributing

## Current project status

netchaos has a working core transport (M1) and all three v1 fault types — latency, packet loss, partition — implemented and composed (M2). The [04 — API Design](04-api-design.md) surface frozen by [M0](tasks/m0-decisions-and-foundations.md) is fully built, plus one addition made during M2-4 (`WithPeerName`). What remains is M3 (a `testing/synctest`-based reproducibility test suite exercising the whole package together) and M4 (API polish and release).

## What's useful to contribute right now

With the core implemented, the highest-value contributions are shifting from design discussion toward exercising and hardening what's there:

- **Discussion on API shape.** Does the [frozen `Network`/`Option` API](04-api-design.md) feel right for real usage? Are there ergonomics issues or naming concerns worth raising before implementation makes the surface expensive to change? (Note: the surface is frozen for v1, not immutable — a strong enough case can still reopen a decision, but that's now a deliberate change, not a pending open question.)
- **Issues describing real-world scenarios.** If you have a concrete resilience-testing scenario (a retry policy, a circuit breaker, a specific timeout behavior) that's hard to test today, describing it as an issue helps validate that the v1 scope in [06 — Scope & Roadmap](06-scope-and-roadmap.md) actually covers the cases people need.
- **Feedback on the comparison in [02 — Comparison](02-comparison.md).** If you've used Toxiproxy, gosim, Chaos Mesh, Litmus, or Antithesis and think the comparison mischaracterizes something, that's worth flagging — the positioning depends on getting these comparisons right.

## Implementation contributions

`net.Conn`/`net.Listener` simulation, the fault-injection layer, and the `Network` type are implemented (M1–M2), built against the frozen interfaces in [04 — API Design](04-api-design.md). See [Task breakdown](tasks/README.md) for the remaining milestones (M3 onward) that new implementation PRs should map to.

## Working method

Code contributions follow test-first development: write the test before the code it drives, confirm it fails, then implement until it passes. A pull request that adds production code without the test that motivated it will be asked to add one.

## License

netchaos is licensed under [MIT](../LICENSE). Contributions are accepted under the same license.
