# 02 — Comparison

This space already has strong, well-established tools. netchaos fills a specific gap between them rather than competing head-on. This doc expands the comparison table from the root README into a full breakdown, tool by tool.

## Summary table

| | Where it runs | What it tests | Setup cost |
|---|---|---|---|
| **[Toxiproxy](https://github.com/Shopify/toxiproxy)** (Shopify) | External process, real TCP sockets | Your whole binary against degraded *real* network | Low, but requires infrastructure (proxy + real network) running |
| **[gosim](https://github.com/jellevandenhooff/gosim)** | Source-translated program, custom runtime | Full-program determinism: network, disk, goroutine scheduling | High — rewrites how your entire program executes |
| **netchaos** | In-process, plain library | Your business logic against simulated network faults, inside a normal `go test` | Minimal — swap a `net.Dial` call for a factory |

## Toxiproxy

**What it is:** A TCP proxy you run as a separate process. Your service connects through it instead of connecting directly to its dependency; Toxiproxy then applies "toxics" (latency, bandwidth limits, connection resets) to real traffic flowing through real sockets.

**Where it shines:** Integration and end-to-end tests where you want your *actual running binary*, with its *actual network stack*, to experience degraded conditions. It proves the whole system — not just one code path — survives a bad network.

**Where netchaos differs:**
- Toxiproxy requires standing up and tearing down a process (or container) alongside your test. netchaos requires nothing beyond `go get`.
- Toxiproxy's faults happen on real sockets in real time; a latency toxic of 200ms genuinely costs your test 200ms of wall-clock time. netchaos faults happen against simulated connections, and — when composed with `testing/synctest` — cost no real wall-clock time at all.
- Toxiproxy tests reproducibility depends on your infrastructure and OS scheduler; netchaos is seeded and deterministic by design.

**When to pick Toxiproxy instead:** You want to validate your service's behavior end-to-end, including things netchaos explicitly doesn't touch — actual OS-level socket behavior, real TLS handshakes over a degraded link, or multi-service integration tests where several real binaries talk to each other through the proxy.

## gosim

**What it is:** A framework that source-translates a Go program into a version that runs on a custom deterministic runtime, simulating not just the network but disk I/O, goroutine scheduling, and other sources of nondeterminism. The goal is to get bit-for-bit reproducible execution of an entire distributed system for testing purposes (in the spirit of FoundationDB-style deterministic simulation testing).

**Where it shines:** Whole-system deterministic simulation — finding rare interleaving bugs, testing multi-node distributed protocols under adversarial scheduling, achieving the kind of exhaustive reproducibility that unit tests alone can't reach.

**Where netchaos differs:**
- gosim requires rewriting how your program executes (source translation, custom runtime). netchaos requires swapping a `net.Dial` call for a factory function — no rewrite, no custom runtime, no changes to how the rest of your program runs.
- gosim's scope is the entire program (disk, syscalls, scheduling). netchaos's scope is exactly one layer: the network connection. This is a deliberate boundary — see [06 — Scope & Roadmap](06-scope-and-roadmap.md) for why.
- gosim is suited to testing distributed *systems* (multiple simulated nodes/processes). netchaos is suited to testing a single component's *resilience logic* against a network that misbehaves.

**When to pick gosim instead:** You're building a distributed system (e.g., a consensus protocol, a distributed database) and need to find scheduling/ordering bugs across multiple simulated nodes, not just verify that one client's retry logic handles a flaky connection correctly.

## Chaos Mesh / Litmus

**What they are:** Production-grade chaos engineering platforms for Kubernetes. They inject failures — pod kills, network delay, packet loss, disk pressure — into a *running production or staging environment* to validate real-world resilience and observability.

**Where they shine:** Validating that an entire deployed system — infrastructure, orchestration, monitoring, alerting, and application code together — survives real failure conditions, with real blast radius and real operational consequences to learn from.

**Where netchaos differs:**
- Chaos Mesh/Litmus operate at runtime against real infrastructure, with real (if controlled) blast radius. netchaos is dev-time only: it runs inside `go test`, with no daemon, no proxy, no Kubernetes involved, and zero blast radius since nothing real is affected.
- They test operational readiness of a whole deployed system. netchaos tests whether a specific piece of Go code — a retry policy, a timeout, a circuit breaker — behaves correctly under simulated conditions, at the unit-test level.

**When to pick them instead:** You need to validate production readiness — whether your monitoring catches a real outage, whether your runbooks work, whether your system degrades gracefully at the infrastructure level. This is a different question than "does my client library retry correctly," and netchaos is not built to answer it.

## Antithesis

**What it is:** A deterministic hypervisor-based simulation platform that runs your entire application (network, disk, arbitrary nondeterminism) inside a controlled, replayable environment, exploring the state space to find bugs autonomously.

**Where it shines:** Deep, autonomous exploration of a whole system's behavior space, with perfect replayability of any discovered bug — well beyond what any hand-written test can achieve.

**Where netchaos differs:** Antithesis is a platform-level investment (whole-system simulation, autonomous exploration) with corresponding setup and operational cost. netchaos is a narrow, single-purpose library you import for one class of unit test: does my code react correctly to a bad network connection. There's no meaningful setup cost and no exploration engine — you write the specific fault scenario you want to assert against.

**When to pick Antithesis instead:** You want autonomous, exhaustive bug-finding across your whole system's nondeterminism surface, and you're willing to invest in the platform to get it.

## Decision guide

| If you want to... | Use |
|---|---|
| Unit test that your retry/timeout/circuit-breaker logic reacts correctly to a bad network, in milliseconds, deterministically | **netchaos** |
| Prove your whole running binary survives degraded real network conditions | **Toxiproxy** |
| Find scheduling/ordering bugs across a distributed system under deterministic simulation | **gosim** |
| Validate production readiness of a deployed system (infra, monitoring, runbooks) | **Chaos Mesh / Litmus** |
| Autonomously explore a whole system's bug surface with perfect replay | **Antithesis** |

Next: [03 — Architecture](03-architecture.md) covers how netchaos is structured internally to deliver on this narrow goal.
