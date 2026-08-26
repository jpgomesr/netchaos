# 07 — Contributing

## Current project status

netchaos v1 is implemented: the core transport, all three fault types (latency, packet loss, partition), and `testing/synctest` integration are built, tested, and documented. The [04 — API Design](04-api-design.md) surface frozen by [M0](tasks/m0-decisions-and-foundations.md) is fully built, plus one addition made during implementation (`WithPeerName`). See `CHANGELOG.md` for the tagged release state.

The tagged version is `v0.1.0`, not `v1.0.0` — deliberately. The surface has never had external users yet, and `v0.1.0` leaves room to correct an ergonomics mistake or naming regret before committing to the stricter compatibility expectations a `v1.0.0` tag implies. Treat the API as stable but not frozen until `v1.0.0`: a breaking change is possible, but it needs a real justification, not routine churn.

## What's useful to contribute right now

With v1 shipped, implementation contributions are now the highest-value ones — this inverts the earlier guidance, which deferred them because the API was still unsettled:

- **Bug reports.** If netchaos's simulated `net.Conn`/`net.Listener` behaves differently from a real one in a way that isn't documented as a deliberate v1 trade-off (see [06 — Scope & Roadmap](06-scope-and-roadmap.md) and the [determinism contract](04-api-design.md#determinism-contract)), that's a bug — file it.
- **Implementation PRs.** Fixes, test coverage, and small ergonomic improvements that don't change the exported surface are welcome without prior discussion. A PR that would change an exported signature should still open with an issue first, since that's the kind of change M0-5 froze deliberately and a real justification is needed to reopen it before `v1.0.0`.
- **Discussion on API shape.** Does the [`Network`/`Option` API](04-api-design.md) feel right for real usage? Ergonomics issues or naming concerns are worth raising now, while `v0.1.0`'s room to change still exists — see the version note above.
- **Issues describing real-world scenarios.** If you have a concrete resilience-testing scenario (a retry policy, a circuit breaker, a specific timeout behavior) that's hard to test today, describing it as an issue helps validate that the v1 scope in [06 — Scope & Roadmap](06-scope-and-roadmap.md) actually covers the cases people need.
- **Feedback on the comparison in [02 — Comparison](02-comparison.md).** If you've used Toxiproxy, gosim, Chaos Mesh, Litmus, or Antithesis and think the comparison mischaracterizes something, that's worth flagging — the positioning depends on getting these comparisons right.

## Implementation contributions

`net.Conn`/`net.Listener` simulation, the fault-injection layer, and the `Network` type are implemented, built against the frozen interfaces in [04 — API Design](04-api-design.md). See [Task breakdown](tasks/README.md) for how v1 was sequenced and built, as a reference for the working method below.

## Working method

Code contributions follow test-first development: write the test before the code it drives, confirm it fails, then implement until it passes. A pull request that adds production code without the test that motivated it will be asked to add one.

## License

netchaos is licensed under [MIT](../LICENSE). Contributions are accepted under the same license.
