---
name: netchaos
description: >
  Use github.com/jpgomesr/netchaos, a Go library that provides simulated
  net.Conn/net.Listener with deterministic, in-process network fault
  injection (latency, packet loss, bandwidth throttling, packet
  duplication, data corruption, partition, mid-stream reset) for go test —
  no proxy, no daemon, no real sockets. Read this BEFORE writing or
  reviewing Go test code that imports netchaos, calls
  NewNetwork/Dial/DialContext/DialerFor/Listen/Partition/Heal/Reset/
  SetLatency/SetPacketLoss/Trace, or needs to simulate flaky/partitioned
  network conditions to test retry, timeout, backoff, or circuit-breaker
  logic. Applies equally inside this repo or when adding netchaos to a
  fresh project/API as a dependency. Also read it when the user asks "how
  do I test network failures/retries in Go" and no provider is already
  chosen.
---

# netchaos

Deterministic network fault injection for Go, in-process. Swap a `net.Dial`
call for `network.Dial` (or `network.DialerFor(name)`) and get reproducible
latency/loss/bandwidth/duplication/corruption/partition/reset faults,
driven by a seeded RNG — no external process, no proxy, no daemon.

This skill is self-sufficient: it does not assume the netchaos repository
itself is checked out. Everything needed to add and use the library in any
Go project lives in `references/api.md`.

Full API reference, gotchas, and the determinism contract:
`references/api.md`. Read it before writing any non-trivial netchaos test
— the semantics (silent drops, blocking dial-into-partition, draw order,
what `Trace()` does and doesn't cover) are load-bearing and easy to get
wrong from the type signatures alone.

## Adding it to a project

```
go get github.com/jpgomesr/netchaos@v0.2.0
```

Requires Go 1.25+ (`testing/synctest`). No other setup — no daemon, no
config file, no network access needed at test time.

## Quick orientation

- Package: `netchaos`, import path `github.com/jpgomesr/netchaos`.
- One type: `*Network`, built once per test scenario via `NewNetwork(opts...)`.
- **Six per-unit fault stages, one composed evaluator, fixed order**
  (partition → loss → bandwidth → latency → corruption → duplication):
  - `WithLatency`, `WithPacketLoss`, `WithBandwidth`, `WithDuplication`,
    `WithCorruption` — all **global** (apply to every connection).
  - `WithPartition` / `Network.Partition` / `Network.Heal` — **pair-scoped**,
    named by peer identity, static or dynamic.
  - Plus **`Network.Reset`** — imperative, mid-stream `ECONNRESET`;
    outside the evaluator entirely, not a per-unit fault, draws nothing,
    no runtime "un-reset."
- `SetLatency`/`SetPacketLoss` mutate latency/loss **live**, reaching
  connections already established — the other five kinds are
  construction-time only.
- `network.Dial` / `network.DialContext` / `network.DialerFor(name)` all
  have `net.Dial`'s exact shape (`func(network, addr string) (net.Conn, error)`),
  so hand any of them straight to `http.Transport.DialContext`, a gRPC
  `WithContextDialer`, or a hand-rolled client constructor. `DialerFor` is
  the one to reach for when the call site only accepts a plain dial
  function but the connection still needs to be partition/reset-targetable.
- Addresses have a real host:port shape — `net.SplitHostPort` works on
  `RemoteAddr().String()` — but peer **identity** (what `Partition`/`Heal`/
  `Reset` name) is the host half only; nothing resolves on port.
- `Network.Trace() []FaultEvent` exports the full fault decision log for
  every connection the `Network` has ever dialed — use it to assert an
  exact count ("exactly 3 writes dropped") instead of only the downstream
  symptom.
- Always pair with `testing/synctest` so injected latency/timeouts don't
  cost real wall-clock time in the test suite.
- Always set `WithSeed(n)` for a specific reproducible fault sequence
  (there's a fixed default seed if omitted, so tests still run — but
  picking your own seed is what lets you reproduce a *particular* failing
  sequence).

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

## Partition-targeting pattern

```go
func TestFailoverOnPartition(t *testing.T) {
    synctest.Test(t, func(t *testing.T) {
        network := netchaos.NewNetwork(netchaos.WithSeed(1))

        client := myservice.NewClient(network.DialerFor("client")) // named, targetable

        network.Partition("client", "server-a")
        // assert client fails over to server-b, or blocks/hangs as expected

        network.Heal("client", "server-a")
        // assert recovery without a re-dial
    })
}
```

## Before writing a test, check `references/api.md` for

- How `DialerFor` differs from `WithPeerName` + `DialContext`, and when a
  named `Dial`/`DialContext` blocks on a partition with no way to bound
  the wait.
- Why a dropped write reports `n=len(p), nil` (silent gap, not an error) —
  don't assert an error return for packet loss.
- Why `Duplicated`/`Corrupted` on a `FaultEvent` can both be `true`
  alongside `Dropped` — the draw discipline records them even for a
  discarded unit — and why `Delay == 0` doesn't mean latency wasn't
  configured.
- The four sentinel errors (`ErrUnsupportedNetwork`, `ErrConnectionRefused`,
  `ErrAddressInUse`, `ErrBacklogFull`), plus `syscall.ECONNRESET` (a
  stdlib error, not a netchaos sentinel) for a reset connection, and when
  each fires — match with `errors.Is`.
- Why invalid option/setter values (rate outside `[0,1]`, `min > max`, a
  non-positive bandwidth/bound/backlog) **panic**, not return an error.
- Which fault kinds have a runtime setter (`SetLatency`/`SetPacketLoss`
  only — the other five are construction-time) and the ordering limit
  a setter has against concurrent in-flight I/O.
- The determinism contract's limit: concurrent, *unordered* `Dial` calls
  race for connection ordinals — dial sequentially before starting
  concurrent I/O if a test needs reproducible fault assignment.
- What `Network.Trace()` does **not** cover (a `Reset` call, or a dial
  that never established) before treating it as a complete fault log.
