# 06 — Scope & Roadmap

## Why scope is deliberately tight

The root README states the reasoning directly:

> To avoid the trap of "framework covering everything" — the same trap that turned a previous project into an unfinishable moving target — v1 is scoped tightly.

This is the single most important constraint on netchaos's design. A network-fault-injection library has an almost unlimited surface it *could* cover — UDP, TLS-level faults, HTTP/2 stream-level faults, disk faults, full syscall interception, protocol-aware fault injection, distributed clock skew simulation, and so on. Every one of these is individually reasonable to want. Building all of them before shipping anything is how projects stall out permanently. netchaos's v1 boundary exists specifically to avoid that failure mode, based on direct prior experience with a project that fell into it.

The operating principle: **ship a narrow, solid core first** (simulated TCP-shaped `net.Conn`/`net.Listener` with the three core faults, integrated with `testing/synctest`), and treat everything else as a deliberately deferred, revisitable decision — not a missing feature.

## v1 scope

- [ ] Simulated `net.Conn` / `net.Listener` (TCP-shaped) with pluggable fault injection
- [ ] Latency injection (fixed and ranged)
- [ ] Packet loss (probabilistic, seeded/deterministic)
- [ ] Network partition (drop all traffic between two simulated peers)
- [ ] Seeded randomness for reproducible failure scenarios
- [ ] Integration with `testing/synctest` for virtual time

Each item is covered in depth elsewhere: the connection/listener simulation and `synctest` integration in [03 — Architecture](03-architecture.md), the fault mechanics in [05 — Fault Injection](05-fault-injection.md), and the proposed configuration surface in [04 — API Design](04-api-design.md).

## Explicitly out of scope for v1

The README lists most of these directly; reordering was an open question resolved by [M0-1](tasks/m0-decisions-and-foundations.md#m0-1--resolve-whether-reordering-is-in-v1):

- **Reordering** — considered for v1 (the README's introductory prose used to list it as a fault type while the checklist above never did) and decided out. If picked up post-v1, the mechanic would be: instead of delivering queued writes to the other peer strictly in the order they were written, the fault-injection layer holds a small window of pending writes and delivers them in a seeded-random permutation — meaningful primarily for protocols/clients that assume in-order delivery over what they believe is a reliable stream, a stronger assumption than TCP itself actually guarantees is preserved end-to-end at the application level in all real-world conditions.
- **Disk fault injection** — simulating disk I/O errors, latency, or corruption. A different layer entirely from network; explicitly excluded to keep netchaos's scope to "network" as its name promises.
- **Full syscall simulation** — the approach taken by tools like gosim (see [02 — Comparison](02-comparison.md#gosim)). netchaos's in-process, no-rewrite adoption model is incompatible with also doing full syscall interception; picking one is a deliberate trade-off in favor of ease of adoption over full-system determinism.
- **UDP support** — v1 is TCP-shaped only (`net.Conn`/`net.Listener` are inherently stream-oriented). UDP's datagram semantics (no ordering guarantee, no connection state) are different enough that supporting it well would be a distinct design effort, not a small addition.
- **Protocol-level fault injection above the connection layer** — e.g., HTTP-aware faults (corrupt a specific header, truncate a response body at the framing level), gRPC-aware faults, etc. netchaos operates at the `net.Conn` byte-stream level; anything that requires understanding a specific application protocol is out of scope by design, since it would tie netchaos's core to specific protocol implementations.
- **Per-peer-pair scoping of latency and packet loss** — v1 applies both globally across every connection in the `Network`; see [M0-2](tasks/m0-decisions-and-foundations.md#m0-2--decide-fault-scoping-global-vs-per-peer-pair) and [04 — API Design](04-api-design.md#fault-scoping-global-vs-per-peer-pair).

These may be revisited once the core is solid — being out of v1 scope is not a permanent rejection, it's sequencing.

## How to think about future scope decisions

When evaluating whether a new fault type or capability belongs in netchaos, the questions this doc's framing suggests asking:

1. Does it operate at the **connection/byte-stream layer**, or does it require understanding a higher-level protocol or a different subsystem (disk, syscalls)? If the latter, it likely doesn't belong in netchaos's core.
2. Can it be added **without requiring adopters to rewrite how their program executes** (no source translation, no custom runtime)? If not, it conflicts with the core "minimally invasive to adopt" goal from [03 — Architecture](03-architecture.md#design-goals-driving-the-architecture).
3. Is the core (v1 scope above) actually solid and shipped yet? If not, the answer to "should we add X" is "not yet" regardless of X's merits, per the explicit lesson behind this scoping approach.

Next: [07 — Contributing](07-contributing.md) covers what kind of contributions are useful at the current project stage.
