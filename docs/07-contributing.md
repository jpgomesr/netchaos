# 07 — Contributing

## Current project status

netchaos v1 is implemented: the core transport, all three fault types (latency, packet loss, partition), and `testing/synctest` integration are built, tested, and documented. The [04 — API Design](04-api-design.md) surface frozen by [M0](tasks/m0-decisions-and-foundations.md) is fully built, plus one addition made during implementation (`WithPeerName`). See `CHANGELOG.md` for the tagged release state.

The latest tag is `v0.3.0`, not `v1.0.0` — deliberately. The surface has never had external users yet, and staying below `v1.0.0` leaves room to correct an ergonomics mistake or naming regret before committing to the stricter compatibility expectations a `v1.0.0` tag implies. Treat the API as stable but not frozen until `v1.0.0`: a breaking change is possible, but it needs a real justification, not routine churn.

## Go version support

**The minimum supported Go version is 1.25**, the release that introduced `testing/synctest`, which netchaos's virtual-time integration depends on. netchaos has promised 1.25+ support from its first release, and the floor stays at 1.25: it is not raised to follow Go's own two-release support window. The one trigger for revisiting it is a security fix that cannot be had without a newer Go — in practice a CVE in `golang.org/x/net`, the project's only third-party import — and that decision goes to the maintainer, not to a dependency bump.

How the repository keeps that promise ([#110](https://github.com/jpgomesr/netchaos/issues/110)):

- **The library module has no requirements.** `golang.org/x/net` is used only by the `nettest` conformance suite, which lives in its own module under `internal/conformance/`. x/net can follow upstream there, even when a release raises its own `go` line, without touching the library's.
- **CI runs a Go 1.25 leg with `GOTOOLCHAIN: local`.** A change that raises the library's `go` line fails that leg instead of silently dropping Go 1.25 for every consumer.
- **Dependabot watches both modules.** Updates to the conformance module never affect what consumers of netchaos need.

If a change ever does need to raise the floor, it ships in a minor version, never a patch, and the CHANGELOG calls it out.

## What's useful to contribute right now

With v1 shipped, implementation contributions are now the highest-value ones — this inverts the earlier guidance, which deferred them because the API was still unsettled:

- **Bug reports.** If netchaos's simulated `net.Conn`/`net.Listener` behaves differently from a real one in a way that isn't documented as a deliberate v1 trade-off (see [06 — Scope & Roadmap](06-scope-and-roadmap.md) and the [determinism contract](04-api-design.md#determinism-contract)), that's a bug — file it.
- **Implementation PRs.** Fixes, test coverage, and small ergonomic improvements that don't change the exported surface are welcome without prior discussion. A PR that would change an exported signature should still open with an issue first, since that's the kind of change M0-5 froze deliberately and a real justification is needed to reopen it before `v1.0.0`.
- **Discussion on API shape.** Does the [`Network`/`Option` API](04-api-design.md) feel right for real usage? Ergonomics issues or naming concerns are worth raising now, while the pre-`v1.0.0` room to change still exists — see the version note above. Anything that doesn't fit one of the issue forms belongs in [Discussions](https://github.com/jpgomesr/netchaos/discussions).
- **Use-case reports.** If you have a concrete resilience-testing scenario (a retry policy, a circuit breaker, a specific timeout behavior) that's hard to test today, file it with the [use-case scenario form](https://github.com/jpgomesr/netchaos/issues/new?template=use-case-scenario.yml). This is the single highest-value input right now: every item [06 — Scope & Roadmap](06-scope-and-roadmap.md) lists as "genuinely open for post-v1 consideration" — reordering, per-peer-pair scoping, `SetBandwidth`, `Reset` trace attribution — and the `v1.0.0` tag itself are gated on real usage evidence rather than a timeline, and this form is where that evidence arrives.
- **Feedback on the comparison in [02 — Comparison](02-comparison.md).** If you've used Toxiproxy, gosim, Chaos Mesh, Litmus, or Antithesis and think the comparison mischaracterizes something, that's worth flagging — the positioning depends on getting these comparisons right.

## Implementation contributions

`net.Conn`/`net.Listener` simulation, the fault-injection layer, and the `Network` type are implemented, built against the frozen interfaces in [04 — API Design](04-api-design.md). See [Task breakdown](tasks/README.md) for how v1 was sequenced and built, as a reference for the working method below.

## Working method

Code contributions follow test-first development: write the test before the code it drives, confirm it fails, then implement until it passes. A pull request that adds production code without the test that motivated it will be asked to add one.

## License

netchaos is licensed under [MIT](../LICENSE). Contributions are accepted under the same license.
