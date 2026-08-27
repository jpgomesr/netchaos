# netchaos

**Deterministic network fault injection for Go, in-process, no infrastructure required.**

> `testing/synctest` virtualizes time. `netchaos` virtualizes network. Same philosophy: deterministic, in-process, zero extra infrastructure — the way the standard library would do it if it covered this layer too.

[![Go Reference](https://pkg.go.dev/badge/github.com/jpgomesr/netchaos.svg)](https://pkg.go.dev/github.com/jpgomesr/netchaos)
[![golangci-lint](https://github.com/jpgomesr/netchaos/actions/workflows/golangci-lint.yml/badge.svg)](https://github.com/jpgomesr/netchaos/actions/workflows/golangci-lint.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

> **Status: `v0.1.0` released.** v1 is implemented, tested, and documented. The API is stable but not frozen until `v1.0.0` — see [docs/07 — Contributing](docs/07-contributing.md). Requires Go 1.25+ (`testing/synctest`).

**Full design documentation:** [`docs/`](docs/README.md)

---

## What this is

`netchaos` is a Go library that provides simulated `net.Conn` and `net.Listener` implementations with deterministic fault injection: latency, packet loss, and partitions. It's designed to be imported directly into `go test`, with no external process, no proxy, and no daemon — and to compose naturally with Go's own `testing/synctest` for virtual time.

The goal is narrow and specific: let you write a **unit test** that proves your retry logic, timeout handling, or circuit breaker reacts correctly to a bad network — in milliseconds, deterministically, reproducibly, on any CI runner, with zero setup.

## What this is not

This space already has strong, well-established tools — and `netchaos` deliberately does not try to replace any of them. It fills a specific gap between two different approaches:

| | Where it runs | What it tests | Setup cost |
|---|---|---|---|
| **[Toxiproxy](https://github.com/Shopify/toxiproxy)** (Shopify) | External process, real TCP sockets | Your whole binary against degraded *real* network | Low, but requires infrastructure (proxy + real network) running |
| **[gosim](https://github.com/jellevandenhooff/gosim)** | Source-translated program, custom runtime | Full-program determinism: network, disk, goroutine scheduling | High — rewrites how your entire program executes |
| **netchaos** | In-process, plain library | Your business logic against simulated network faults, inside a normal `go test` | Minimal — swap a `net.Dial` call for a factory |

Concretely:

- **Not chaos engineering for production.** Tools like Chaos Mesh or Toxiproxy-in-prod operate at runtime, against real infrastructure, with real blast radius. `netchaos` is dev-time only: it runs inside `go test`, with no daemon, no proxy, no Kubernetes involved.
- **Not full-program determinism.** Projects like gosim (or Antithesis) aim to make an entire program deterministic — disk, syscalls, scheduling, everything. `netchaos` intentionally scopes to the network layer only. That's a deliberate boundary, not a hidden limitation: it's what makes incremental adoption in an existing codebase possible without rewriting anything.
- **Not an integration-testing tool.** Toxiproxy proves your service survives real degraded network conditions while actually running. `netchaos` proves your logic — retries, timeouts, backoff — behaves correctly under a simulated scenario, without spending real wall-clock time or standing up any external dependency.

## Who it's for

Go developers building distributed services (gRPC, HTTP between microservices, message queues) who want to unit test resilience behavior — retry policies, timeouts, backoff, circuit breakers — without pulling in heavyweight test infrastructure.

## Who it's not for

- SREs doing chaos engineering in production → look at [Chaos Mesh](https://chaos-mesh.org/) or [Litmus](https://litmuschaos.io/)
- Teams needing bit-for-bit determinism across an entire program → look at [gosim](https://github.com/jellevandenhooff/gosim) or [Antithesis](https://antithesis.com/)
- QA needing end-to-end / load testing against a real running service → look at [Toxiproxy](https://github.com/Shopify/toxiproxy)

## Usage

```go
package myservice_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/jpgomesr/netchaos"
)

func TestRetryOnPacketLoss(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		network := netchaos.NewNetwork(
			netchaos.WithPacketLoss(0.3),
			netchaos.WithLatency(50*time.Millisecond, 150*time.Millisecond),
			netchaos.WithSeed(42), // deterministic, reproducible failures
		)

		client := myservice.NewClient(network.Dial)

		got, err := client.FetchWithRetry("resource-id")
		if err != nil {
			t.Fatalf("expected retry to succeed despite packet loss, got: %v", err)
		}
		if got != "resource-id" {
			t.Fatalf("got %q, want %q", got, "resource-id")
		}
	})
}
```

`myservice.NewClient`/`FetchWithRetry` stand in for your own client and its retry policy — the only netchaos-specific line is `myservice.NewClient(network.Dial)`, handing your client a `func(network, addr string) (net.Conn, error)` it can dial through. A fully self-contained, compiled version of this same scenario (a hand-rolled client and server in place of `myservice`) lives as `TestReadmeUsageSnippet` in [`example_test.go`](example_test.go), along with runnable examples for each headline feature.

For a full, standalone project wired up with netchaos — not just an inline snippet — see [jpgomesr/netchaos-example](https://github.com/jpgomesr/netchaos-example).

## Scope for v1 (deliberately minimal)

To avoid the trap of "framework covering everything" — the same trap that turned a previous project into an unfinishable moving target — v1 is scoped tightly:

- [x] Simulated `net.Conn` / `net.Listener` (TCP-shaped) with pluggable fault injection
- [x] Latency injection (fixed and ranged)
- [x] Packet loss (probabilistic, seeded/deterministic)
- [x] Network partition (drop all traffic between two simulated peers)
- [x] Seeded randomness for reproducible failure scenarios
- [x] Integration with `testing/synctest` for virtual time

Explicitly out of scope for v1: reordering, disk fault injection, full syscall simulation, UDP support, protocol-level fault injection above the connection layer, and per-peer-pair scoping of latency/loss. Reordering and per-pair scoping are genuinely open for post-v1 consideration; the rest are excluded on a design principle, not sequencing — see [docs/06 — Scope & Roadmap](docs/06-scope-and-roadmap.md#explicitly-out-of-scope-for-v1) for which is which.

## Installation

Requires **Go 1.25 or later** — `testing/synctest`, which netchaos's virtual-time integration depends on, was introduced in Go 1.25 and cannot be used on an older toolchain.

```
go get github.com/jpgomesr/netchaos@v0.1.0
```

## Contributing

netchaos v1 is implemented, tested, and documented. Bug reports and implementation PRs are welcome; see [docs/07 — Contributing](docs/07-contributing.md) for what's most useful to contribute right now, and note the API is stable but not frozen until `v1.0.0`.

## License

[MIT](LICENSE)
