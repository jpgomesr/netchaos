# 04 — API Design

> **PROPOSED / NOT YET IMPLEMENTED.** Everything in this document is a design sketch for the v1 API surface. Signatures, names, and shapes are all subject to change once implementation begins. Nothing described here exists in code yet.

This is a concrete elaboration of the code sample from the root [`README.md`](../README.md), scoped to the v1 fault types in [06 — Scope & Roadmap](06-scope-and-roadmap.md).

## Package shape

```go
package netchaos // import "github.com/jpgomesr/netchaos"
```

A single flat package for v1 — no subpackages. If the API grows enough to warrant it (e.g., separating fault types from the core network simulation), that's a post-v1 concern.

## `Network`

The central type. Owns the seeded RNG, the configured fault policy, and simulated topology state.

```go
// Network is a simulated, in-process network with deterministic fault
// injection. A Network is created per test scenario via NewNetwork.
type Network struct {
    // unexported fields: seeded RNG, fault policy, topology state
}

// NewNetwork constructs a Network configured by the given Options.
func NewNetwork(opts ...Option) *Network
```

## Functional options

Consistent with the idiomatic Go functional-options pattern, and with the shape already implied by the README's example (`netchaos.WithPacketLoss(0.3)`, etc.):

```go
// Option configures a Network at construction time.
type Option func(*networkConfig)

// WithSeed makes fault injection deterministic and reproducible: the same
// seed, with the same sequence of Network operations, always produces the
// same sequence of injected faults.
func WithSeed(seed int64) Option

// WithLatency delays delivery of writes by a duration drawn uniformly from
// [min, max]. Passing an equal min and max applies fixed latency.
func WithLatency(min, max time.Duration) Option

// WithPacketLoss drops writes with the given probability, in [0.0, 1.0].
// The decision is drawn from the Network's seeded RNG, so it is
// reproducible for a given seed.
func WithPacketLoss(rate float64) Option

// WithPartition marks all traffic between the named peers as dropped,
// starting immediately when the Network is constructed. Partitions
// configured this way are static for the lifetime of the Network; see
// Network.Partition / Network.Heal for dynamic control during a test.
func WithPartition(peerA, peerB string) Option
```

Open design question: whether fault options apply globally to every simulated connection in the `Network`, or can be scoped per-peer-pair (e.g., only the client→server-b link is lossy, not client→server-a). The README's example implies global application for v1; per-pair scoping is a plausible post-v1 refinement once real usage patterns are clearer.

## Dialing and listening

```go
// Dial creates a simulated connection from the calling peer to addr within
// this Network, subject to the Network's fault policy. It has the same
// signature shape as net.Dial so it can be used as a drop-in for code that
// accepts a dial function.
func (n *Network) Dial(network, addr string) (net.Conn, error)

// Listen registers a simulated listener at addr within this Network.
// Connections dialed to addr from elsewhere in the Network are delivered
// to this listener's Accept, subject to the Network's fault policy.
func (n *Network) Listen(network, addr string) (net.Listener, error)
```

The intent is for `Network.Dial` to be usable anywhere calling code accepts a `func(network, addr string) (net.Conn, error)` — e.g., as the dial function passed into an HTTP transport, a gRPC dialer, or a hand-rolled client constructor. This is the "swap a `net.Dial` call for a factory" adoption path described in [03 — Architecture](03-architecture.md#design-goals-driving-the-architecture).

## Dynamic partition control

For scenarios that need to partition and heal a link *during* a test (not just at construction time):

```go
// Partition drops all subsequent traffic between the named peers until
// Heal is called for the same pair.
func (n *Network) Partition(peerA, peerB string)

// Heal removes a previously established partition between the named peers.
func (n *Network) Heal(peerA, peerB string)
```

## Full usage sketch

Restating the README's example with the API surface above made explicit:

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
        net := netchaos.NewNetwork(
            netchaos.WithPacketLoss(0.3),
            netchaos.WithLatency(50*time.Millisecond, 150*time.Millisecond),
            netchaos.WithSeed(42), // deterministic, reproducible failures
        )

        client := myservice.NewClient(net.Dial)

        err := client.FetchWithRetry(ctx, "resource-id")

        if err != nil {
            t.Fatalf("expected retry to succeed despite packet loss, got: %v", err)
        }
    })
}
```

## Determinism contract

The proposed determinism guarantee, which the implementation must uphold once built: for a fixed seed, a fixed sequence of `Network` method calls (`Dial`, `Listen`, `Partition`, `Heal`, and I/O on the resulting connections) in a fixed order produces an identical sequence of injected faults across runs and across machines. This is the property that lets a failing test be reproduced reliably from a seed value alone — analogous to how `go test -run` plus a fixed input reproduces a deterministic unit test failure.

Next: [05 — Fault Injection](05-fault-injection.md) details the mechanics behind each option above.
