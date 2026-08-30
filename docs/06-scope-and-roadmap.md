# 06 — Scope & Roadmap

## Why scope is deliberately tight

The root README states the reasoning directly:

> To avoid the trap of "framework covering everything" — the same trap that turned a previous project into an unfinishable moving target — v1 is scoped tightly.

This is the single most important constraint on netchaos's design. A network-fault-injection library has an almost unlimited surface it *could* cover — UDP, TLS-level faults, HTTP/2 stream-level faults, disk faults, full syscall interception, protocol-aware fault injection, distributed clock skew simulation, and so on. Every one of these is individually reasonable to want. Building all of them before shipping anything is how projects stall out permanently. netchaos's v1 boundary exists specifically to avoid that failure mode, based on direct prior experience with a project that fell into it.

The operating principle: **ship a narrow, solid core first** (simulated TCP-shaped `net.Conn`/`net.Listener` with the three core faults, integrated with `testing/synctest`), and treat everything else as a deliberately deferred, revisitable decision — not a missing feature.

## v1 scope

- [x] Simulated `net.Conn` / `net.Listener` (TCP-shaped) with pluggable fault injection
- [x] Latency injection (fixed and ranged)
- [x] Packet loss (probabilistic, seeded/deterministic)
- [x] Network partition (drop all traffic between two simulated peers)
- [x] Seeded randomness for reproducible failure scenarios
- [x] Integration with `testing/synctest` for virtual time

Each item is covered in depth elsewhere: the connection/listener simulation and `synctest` integration in [03 — Architecture](03-architecture.md), the fault mechanics in [05 — Fault Injection](05-fault-injection.md), and the configuration surface in [04 — API Design](04-api-design.md).

## Explicitly out of scope for v1

The README lists most of these directly; reordering was an open question resolved by [M0-1](tasks/m0-decisions-and-foundations.md#m0-1--resolve-whether-reordering-is-in-v1). Now that v1 has shipped, the precondition for revisiting any of these — "once the core is solid" — holds; the two items below genuinely are open for post-v1 consideration, while the rest are excluded on a design principle that doesn't change with time:

**Genuinely open for post-v1 consideration**, contingent on real usage evidence rather than a fixed timeline:

- **Reordering** — considered for v1 and decided out. If picked up post-v1, the mechanic would be: instead of delivering queued writes to the other peer strictly in the order they were written, the fault-injection layer holds a small window of pending writes and delivers them in a seeded-random permutation — meaningful primarily for protocols/clients that assume in-order delivery over what they believe is a reliable stream, a stronger assumption than TCP itself actually guarantees is preserved end-to-end at the application level in all real-world conditions.
- **Per-peer-pair scoping of latency and packet loss** — v1 applies both globally across every connection in the `Network`; see [M0-2](tasks/m0-decisions-and-foundations.md#m0-2--decide-fault-scoping-global-vs-per-peer-pair) and [04 — API Design](04-api-design.md#fault-scoping-global-vs-per-peer-pair). A plausible refinement once real usage patterns show global scoping is too coarse. **Per-direction (asymmetric) faults were merged into this gate** by [M6-12](tasks/m6-review-findings.md#m6-12--decide-on-per-direction-asymmetric-faults) — the two are axes of one configuration model and are to be designed together, not sequentially.

**Excluded on a design principle, not a sequencing decision** — "core is solid" doesn't change why these are out:

- **Disk fault injection** — simulating disk I/O errors, latency, or corruption. A different layer entirely from network; excluded to keep netchaos's scope to "network" as its name promises, regardless of how mature the core is.
- **Full syscall simulation** — the approach taken by tools like gosim (see [02 — Comparison](02-comparison.md#gosim)). netchaos's in-process, no-rewrite adoption model is incompatible with also doing full syscall interception; picking one is a deliberate trade-off in favor of ease of adoption over full-system determinism.
- **UDP support** — v1 is TCP-shaped only (`net.Conn`/`net.Listener` are inherently stream-oriented). UDP's datagram semantics (no ordering guarantee, no connection state) are different enough that supporting it well would be a distinct design effort, not a small addition.
- **Protocol-level fault injection above the connection layer** — e.g., HTTP-aware faults (corrupt a specific header, truncate a response body at the framing level), gRPC-aware faults, etc. netchaos operates at the `net.Conn` byte-stream level; anything that requires understanding a specific application protocol is out of scope by design, since it would tie netchaos's core to specific protocol implementations.

## Accepted for v0.2.0

Decided by the maintainer against the rubric below, from the candidates [M6](tasks/m6-review-findings.md) generated by applying that rubric to the shipped tree. **Accepted means in scope, not implemented** — each item below gets its own task and its own PR. Those tasks are [M7 — v0.2.0 implementation](tasks/m7-v0.2.0-implementation.md), which also sequences them; this section stays the record of *what* was accepted and why, not of how far along it is.

**New fault kinds ([M6-11](tasks/m6-review-findings.md#m6-11--decide-which-new-fault-kinds-if-any-enter-v02)) — all four candidates accepted:**

- **Bandwidth throttling / rate limiting.** Purely a delivery-timing model; composes as a fourth stage in the `faults.go` evaluator alongside latency. Note it interacts with the pipe bound: a throttle slower than the reader is what makes back-pressure observable, which is part of why `M6-17` was accepted alongside it.
- **Mid-stream connection reset.** The clearest gap rather than an enhancement — there is currently no way to make an established conn fail abruptly. `Partition` is the nearest thing and is deliberately a silent black hole (writes succeed, reads block to deadline), not an `ECONNRESET`. Testing "the peer dropped the connection" today means holding the server-side `net.Conn` and calling `Close` yourself, which a test of client-side reconnect logic should not have to do.
- **Packet duplication.**
- **Data corruption / bit flips.**

The last two turn on a question worth restating, because it was decided rather than assumed: real TCP hides duplication and corruption from the application entirely — the receiver dedupes, and the checksum drops corrupt segments — so an application reading a `net.Conn` never observes either. The precedent that settles it is [M0-3](tasks/m0-decisions-and-foundations.md#m0-3--decide-fault-granularity-per-write-vs-per-simulated-packet): real TCP also hides packet *loss* by retransmitting, and netchaos chose the silent-gap model anyway, precisely so tests can exercise what the application sees when delivery does not happen. That line was crossed deliberately for loss, and it extends to these two.

Adding a kind is cheap in determinism terms and that is by design: streams derive from `(masterSeed, ordinal, side, kind)`, so a new `kind` byte gets an independent stream and **cannot** perturb any existing test's latency or loss sequence. New kinds are additive, not a golden-trace break. The constraint each must respect is [M2-5](tasks/m2-determinism-and-faults.md#m2-5--fault-composition-rules)'s draw discipline: a new kind draws unconditionally on every unit past the partition gate, like the existing two.

**Surface additions:**

- **Address host:port structure ([M6-10](tasks/m6-review-findings.md#m6-10--decide-whether-addresses-should-have-a-hostport-shape)).** Addresses gain a synthesized port, so `net.SplitHostPort` succeeds against a netchaos address as it does against a real one. This is the finding that bears most directly on [01 — Vision](01-vision.md)'s adoption claim: today the cost is paid by code the adopter does not want to change, which is the specific friction netchaos exists to avoid. It is also the most disruptive of these — it changes every address string in test output and godoc examples, and makes the address→peer mapping non-trivial for the first time, so `addr.go`'s "exactly one place that relationship is defined" property has to be re-established rather than assumed. It must land before `v1.0.0`, since adding port structure afterwards breaks every address string a test prints.
- **Runtime mutation of latency and loss ([M6-13](tasks/m6-review-findings.md#m6-13--decide-on-runtime-mutation-of-latency-and-loss)).** `SetLatency`/`SetPacketLoss`, matching `Partition`/`Heal`'s live semantics. Two things this forces, and neither is optional: the per-unit read path is currently **lock-free** and gains synchronization, and the [determinism contract](04-api-design.md#determinism-contract) — which today fixes only the order in which `Dial`, `Listen`, `Partition` and `Heal` are called — must widen to cover a mid-run configuration change before any implementation starts.
- **Exported fault observability ([M6-14](tasks/m6-review-findings.md#m6-14--decide-whether-to-export-fault-observability)).** The full trace, not just counters — this closes the deferral `M2-1` recorded explicitly. The cost accepted with it: the event shape becomes public API, and [M3-3](tasks/m3-synctest-and-reproducibility.md) deliberately made the trace format high-friction to change. Whatever ships here is a compatibility surface at `v1.0.0`.
- **Configurable pipe bound and listener backlog ([M6-17](tasks/m6-review-findings.md#m6-17--decide-whether-the-pipe-bound-and-listener-backlog-become-configurable)).** `WithPipeBound` and `WithListenerBacklog`, validated by the existing `validate()` pass with the same panic-on-invalid convention. The in-package `newConnPairWithBound` constructor is the evidence the need is real: the bound already had to be varied to test back-pressure.

Every item in this section adds to the exported surface, so all of them are input to [M5-2](tasks/m5-hardening-and-ergonomics.md#m5-2--api-ergonomics-review-before-v100)'s ergonomics review rather than bypassing it — that review is weighing the surface for size in the same window.

Each is tracked as an issue, per [07 — Contributing](07-contributing.md)'s issue-first rule: **#49** (host:port), **#50** (setters), **#51** (trace export), **#52** (bound and backlog options), **#53** (the four fault kinds, as an umbrella).

**Still gated, and deliberately not decided here:** reordering ([M6-15](tasks/m6-review-findings.md#m6-15--reordering-gate-check-only)) and per-peer-pair scoping ([M6-16](tasks/m6-review-findings.md#m6-16--per-peer-pair-scoping-gate-check-only)) remain contingent on real usage evidence, which does not exist yet. **Per-direction (asymmetric) faults ([M6-12](tasks/m6-review-findings.md#m6-12--decide-on-per-direction-asymmetric-faults)) were folded into `M6-16`'s gate** rather than decided separately: per-pair and per-direction scoping are two axes of the same configuration model, and shipping one without considering the other is how an option surface becomes incoherent. When that gate opens, they are designed together.

## How to think about future scope decisions

When evaluating whether a new fault type or capability belongs in netchaos, the questions this doc's framing suggests asking:

1. Does it operate at the **connection/byte-stream layer**, or does it require understanding a higher-level protocol or a different subsystem (disk, syscalls)? If the latter, it likely doesn't belong in netchaos's core.
2. Can it be added **without requiring adopters to rewrite how their program executes** (no source translation, no custom runtime)? If not, it conflicts with the core "minimally invasive to adopt" goal from [03 — Architecture](03-architecture.md#design-goals-driving-the-architecture).
3. Is the core (v1 scope above) actually solid and shipped yet? If not, the answer to "should we add X" is "not yet" regardless of X's merits, per the explicit lesson behind this scoping approach.

Next: [07 — Contributing](07-contributing.md) covers what kind of contributions are useful at the current project stage.
