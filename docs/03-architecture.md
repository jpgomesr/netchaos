# 03 — Architecture

> **Status: implemented.** The transport, fault-injection layer, and `testing/synctest` integration described below are all built and tested; this doc describes the structure of the shipped code, not a proposal.

## Design goals driving the architecture

1. **In-process.** No external process, proxy, daemon, or OS-level network manipulation (no netem, no iptables). Everything happens inside the Go process running the test.
2. **Interface-compatible.** Code under test must be able to use netchaos's simulated connections through the standard `net.Conn` / `net.Listener` interfaces, without knowing it's not talking to a real socket.
3. **Deterministic.** Given the same seed and the same fixed order of `Dial`/`Listen`/`Partition`/`Heal` calls, netchaos produces the same sequence of faults on each connection, every time, on every machine — see the [determinism contract](04-api-design.md#determinism-contract) for the derivation model and its limits under concurrent, unordered calls.
4. **Composable with virtual time.** When run inside `testing/synctest`, fault injection that involves delays (latency) should consume virtual time, not real wall-clock time.
5. **Minimally invasive to adopt.** Swapping real dialing for simulated dialing should be a small, local change — typically replacing a `net.Dial` call (or whatever dependency-injection point the codebase already uses to obtain a `net.Conn`) with a call into netchaos.

## Conceptual components

### `Network`

The `Network` is the simulated environment: it owns the seeded random source, the configured fault-injection policy, and any simulated topology state (e.g., which simulated peers are currently partitioned from each other). A test constructs one `Network` per scenario it wants to simulate — see [04 — API Design](04-api-design.md) for the construction API.

### Simulated `net.Conn` pairs

At the core, netchaos needs an in-memory, full-duplex, byte-stream connection — conceptually similar to `net.Pipe()`, which the Go standard library already provides as a synchronous, in-memory `net.Conn` pair with no real networking underneath. netchaos's simulated connections extend that idea by inserting a **fault-injection layer** between the two ends:

```
   caller A                                              caller B
      │                                                     │
      │  Write/Read (net.Conn interface)                    │
      ▼                                                     ▼
 ┌─────────┐        fault injection layer            ┌─────────┐
 │ conn A  │ ───▶  latency / loss / partition  ───▶  │ conn B  │
 │         │ ◀───  (seeded, deterministic)     ◀───  │         │
 └─────────┘                                          └─────────┘
```

Each simulated connection satisfies `net.Conn` (`Read`, `Write`, `Close`, `LocalAddr`, `RemoteAddr`, deadlines). Internally, writes on one end don't go straight to the other end's read buffer — they pass through the fault-injection layer first, which may delay them, drop them, or block them entirely depending on the `Network`'s current policy.

### Simulated `net.Listener`

To support server-side code (`net.Listener.Accept()`), the `Network` also needs to simulate listening sockets: a registration of an address within the simulated network, and a queue of incoming simulated connections that `Accept()` pulls from. This lets test code exercise both a simulated client dialing out and a simulated server accepting connections, all within the same `Network`, without any real listening socket being opened.

### Fault-injection layer

This is where the three fault categories from [05 — Fault Injection](05-fault-injection.md) are applied — latency, packet loss, and partition; reordering was considered and [deferred out of v1](05-fault-injection.md#reordering-deferred-not-in-v1):

- **Latency** — delays delivery of a write to the other end by some duration (fixed or ranged), drawn per `Write` call from the connection's derived RNG stream.
- **Packet loss** — probabilistically drops a `Write` call in its entirety instead of delivering it, using the connection's derived RNG stream to decide per write.
- **Partition** — when two simulated peers are partitioned, all traffic between them is dropped (typically indefinitely, until the partition is healed), rather than probabilistically, and consumes no random draws.

Latency and packet loss apply globally to every connection the `Network` simulates; partition is scoped to the specific peer pair named in `WithPartition`/`Partition`/`Heal` — a deliberate asymmetry, see [04 — API Design](04-api-design.md#fault-scoping-global-vs-per-peer-pair).

Each connection's random draws come from its own stream, derived from `(masterSeed, connectionOrdinal, direction, faultKind)` rather than from one `rand.Rand` shared across the `Network`. This is what keeps a full test run reproducible end to end without making one connection's fault sequence depend on how the Go scheduler happened to interleave it with unrelated connections — see the [determinism contract](04-api-design.md#determinism-contract) for the full model.

When more than one fault is configured on the same connection direction, there is exactly **one** evaluation point per unit, not three hooks chained or overwriting one another — a unit is checked against partition, then loss, then latency, in that fixed order, with the draw discipline (which faults draw unconditionally vs. not at all) spelled out in the [determinism contract](04-api-design.md#determinism-contract).

### Composing with `testing/synctest`

`testing/synctest` already virtualizes time within a goroutine bubble: `time.Sleep`, timers, and context deadlines advance instantly as far as wall-clock time is concerned, while still behaving correctly relative to each other. netchaos's latency injection is designed to ride on top of this rather than duplicate it — when a simulated write needs to be delayed, netchaos uses the standard time APIs (e.g., timers) that `testing/synctest` already knows how to virtualize, rather than inventing its own clock abstraction.

This means: run a test inside `synctest.Test`, configure a `Network` with `WithLatency(50*time.Millisecond, 150*time.Millisecond)`, and the test can exercise a client that waits on that latency — without the test actually taking 50–150ms of real time to run. This is the same value proposition `testing/synctest` offers for `time.Sleep`-based code, extended to network-latency-based code.

### Single-process, multi-peer topology

Because everything happens in-process, a `Network` can simulate multiple named peers (e.g., `"client"`, `"server-a"`, `"server-b"`) all within a single `go test` process. This is what makes partition testing meaningful: a `Network` can track partition state between specific pairs of peers, and dialing/listening happens against simulated addresses within that topology rather than real host:port pairs.

Next: [04 — API Design](04-api-design.md) turns this into the concrete, shipped Go API.
