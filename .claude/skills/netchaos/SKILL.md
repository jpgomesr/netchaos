---
name: netchaos
description: >
  Use github.com/jpgomesr/netchaos, a Go library that provides simulated
  net.Conn/net.Listener with deterministic, in-process network fault
  injection (latency, packet loss, partition) for go test — no proxy, no
  daemon, no real sockets. Read this BEFORE writing or reviewing Go test
  code that imports netchaos, calls NewNetwork/Dial/DialContext/Listen/
  Partition/Heal, or needs to simulate flaky/partitioned network conditions
  to test retry, timeout, backoff, or circuit-breaker logic. Also read it
  when the user asks "how do I test network failures/retries in Go" and no
  provider is already chosen.
---

# netchaos

Deterministic network fault injection for Go, in-process. Swap a `net.Dial`
call for `network.Dial` and get reproducible latency/packet-loss/partition
faults, driven by a seeded RNG — no external process.

Full API reference, gotchas, and the determinism contract: `references/api.md`.
Read it before writing any non-trivial netchaos test — the semantics
(silent drops, blocking dial-into-partition, draw order) are load-bearing
and easy to get wrong from the type signatures alone.

## Quick orientation

- Package: `netchaos`, import path `github.com/jpgomesr/netchaos`. Go 1.25+
  (uses `testing/synctest`).
- One type: `*Network`, built once per test scenario via `NewNetwork(opts...)`.
- Three fault kinds: `WithLatency`, `WithPacketLoss` (both **global**, apply
  to every connection), `WithPartition`/`Network.Partition`/`Network.Heal`
  (**pair-scoped**, named by peer identity).
- `network.Dial` / `network.DialContext` have the exact shape stdlib and
  gRPC/HTTP dialers expect (`func(network, addr string) (net.Conn, error)`),
  so hand them straight to `http.Transport.DialContext`, a gRPC
  `WithContextDialer`, or a hand-rolled client constructor.
- Always pair with `testing/synctest` so injected latency/timeouts don't
  cost real wall-clock time in the test suite.
- Always set `WithSeed(n)` for reproducible fault sequences; without it,
  failures aren't reliably reproducible across runs.

## Minimal pattern

```go
func TestRetryOnPacketLoss(t *testing.T) {
    synctest.Test(t, func(t *testing.T) {
        network := netchaos.NewNetwork(
            netchaos.WithPacketLoss(0.3),
            netchaos.WithLatency(50*time.Millisecond, 150*time.Millisecond),
            netchaos.WithSeed(42),
        )

        client := myservice.NewClient(network.Dial) // swap in the dial func

        got, err := client.FetchWithRetry("resource-id")
        // assert on client-level behavior (retry succeeded, timed out, etc.)
    })
}
```

## Before writing a test, check `references/api.md` for

- How to target a specific dialer with `Partition`/`Heal` (`WithPeerName` —
  without it, dialers get an unpartitionable synthesized identity).
- Why `Dial` (not `DialContext`) can hang forever against a partitioned
  peer, and when that's actually what you want to assert.
- Why a dropped write reports `n=len(p), nil` (silent gap, not an error) —
  don't assert an error return for packet loss.
- The four sentinel errors (`ErrUnsupportedNetwork`, `ErrConnectionRefused`,
  `ErrAddressInUse`, `ErrBacklogFull`) and when each fires — match with
  `errors.Is`.
- Why invalid option values (rate outside `[0,1]`, `min > max`) **panic**
  at `NewNetwork`, not return an error.
- The determinism contract's limit: concurrent, *unordered* `Dial` calls
  race for connection ordinals — dial sequentially before starting
  concurrent I/O if a test needs reproducible fault assignment.
