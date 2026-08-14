# 01 — Vision

## The one-line pitch

**Deterministic network fault injection for Go, in-process, no infrastructure required.**

`netchaos` provides simulated `net.Conn` and `net.Listener` implementations with deterministic fault injection — latency, packet loss, and partitions. It's designed to be imported directly into `go test`: no external process, no proxy, no daemon.

## The `testing/synctest` analogy

The core design philosophy is borrowed directly from the Go standard library's `testing/synctest` package:

- `testing/synctest` virtualizes **time**. It lets a test simulate hours of `time.Sleep`, timers, and context deadlines in real milliseconds, deterministically, with no wall-clock waiting.
- `netchaos` virtualizes **network**. It lets a test simulate a lossy, high-latency, partitioned network in real milliseconds, deterministically, with no real sockets, no external proxy, and no infrastructure.

Same philosophy, one layer up the stack: deterministic, in-process, zero extra infrastructure — the way the standard library would do it if it covered this layer too. This is also *why* netchaos is designed to compose naturally with `testing/synctest` rather than duplicate its virtual-time machinery — see [03 — Architecture](03-architecture.md#composing-with-testingsynctest).

## The narrow goal

Distributed systems code is full of logic that only executes under bad network conditions: retry loops, exponential backoff, timeout handling, circuit breakers, connection pooling with health checks. This logic is:

- Hard to exercise with a normal unit test (the network is always "fine" locally).
- Hard to exercise reliably with real degraded infrastructure (flaky, slow, environment-dependent, not reproducible bit-for-bit).
- Rarely covered at all, because standing up the infrastructure to test it costs more than most teams are willing to pay for a single test.

The goal of netchaos is narrow and specific: let you write a **unit test** that proves your retry logic, timeout handling, or circuit breaker reacts correctly to a bad network — in milliseconds, deterministically, reproducibly, on any CI runner, with zero setup.

Not "test the whole system under chaos." Not "guarantee full-program determinism." Just: make the *network-facing edge case* of your business logic testable the same way you'd test any other branch of your code.

## What this is

netchaos is:

- A **Go library**, imported like any other dependency — not a binary, not a sidecar, not a service.
- A source of **simulated `net.Conn` / `net.Listener`** values that satisfy the standard `net` interfaces, so code written against `net.Conn` doesn't need to know it's talking to a simulation.
- **Deterministic and seeded** — the same seed produces the same sequence of faults, every run, on every machine.
- Scoped to the **connection layer** — it fails packets, adds latency, and cuts connections; it does not simulate disks, syscalls, or goroutine scheduling.

## What this is not

netchaos deliberately does **not** try to replace the tools that already do these adjacent jobs well. See [02 — Comparison](02-comparison.md) for the detailed breakdown, but at a glance:

- **Not chaos engineering for production.** Tools like Chaos Mesh or Toxiproxy-in-prod operate at runtime, against real infrastructure, with real blast radius. netchaos is dev-time only: it runs inside `go test`, with no daemon, no proxy, no Kubernetes involved.
- **Not full-program determinism.** Projects like gosim (or Antithesis) aim to make an entire program deterministic — disk, syscalls, scheduling, everything. netchaos intentionally scopes to the network layer only. That's a deliberate boundary, not a hidden limitation: it's what makes incremental adoption in an existing codebase possible without rewriting anything.
- **Not an integration-testing tool.** Toxiproxy proves your service survives real degraded network conditions while actually running. netchaos proves your logic — retries, timeouts, backoff — behaves correctly under a simulated scenario, without spending real wall-clock time or standing up any external dependency.

## Who it's for

Go developers building distributed services (gRPC, HTTP between microservices, message queues) who want to unit test resilience behavior — retry policies, timeouts, backoff, circuit breakers — without pulling in heavyweight test infrastructure.

## Who it's not for

- SREs doing chaos engineering in production → look at [Chaos Mesh](https://chaos-mesh.org/) or [Litmus](https://litmuschaos.io/)
- Teams needing bit-for-bit determinism across an entire program → look at [gosim](https://github.com/jellevandenhooff/gosim) or [Antithesis](https://antithesis.com/)
- QA needing end-to-end / load testing against a real running service → look at [Toxiproxy](https://github.com/Shopify/toxiproxy)

Next: [02 — Comparison](02-comparison.md) covers each of these alternatives in depth.
