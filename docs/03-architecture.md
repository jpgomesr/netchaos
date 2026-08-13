# 03 — Architecture

> **Status: design-stage.** No implementation exists yet. This document describes the intended internal structure so implementation work has a coherent design to follow — it is not a description of existing code.

## Design goals driving the architecture

1. **In-process.** No external process, proxy, daemon, or OS-level network manipulation (no netem, no iptables). Everything happens inside the Go process running the test.
2. **Interface-compatible.** Code under test must be able to use netchaos's simulated connections through the standard `net.Conn` / `net.Listener` interfaces, without knowing it's not talking to a real socket.
3. **Deterministic.** Given the same seed and the same sequence of operations, netchaos produces the same sequence of faults, every time, on every machine.
4. **Composable with virtual time.** When run inside `testing/synctest`, fault injection that involves delays (latency) should consume virtual time, not real wall-clock time.
5. **Minimally invasive to adopt.** Swapping real dialing for simulated dialing should be a small, local change — typically replacing a `net.Dial` call (or whatever dependency-injection point the codebase already uses to obtain a `net.Conn`) with a call into netchaos.

## Conceptual components

### `Network`

The `Network` is the simulated environment: it owns the seeded random source, the configured fault-injection policy, and any simulated topology state (e.g., which simulated peers are currently partitioned from each other). A test constructs one `Network` per scenario it wants to simulate — see [04 — API Design](04-api-design.md) for the proposed construction API.

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

This is where the four fault categories from [05 — Fault Injection](05-fault-injection.md) are applied:

- **Latency** — delays delivery of a write to the other end by some duration (fixed or ranged), drawn from the `Network`'s seeded RNG.
- **Packet loss** — probabilistically drops a write instead of delivering it, using the seeded RNG to decide per write (or per simulated packet, depending on how granular the v1 model ends up being).
- **Partition** — when two simulated peers are partitioned, all traffic between them is dropped (typically indefinitely, until the partition is healed), rather than probabilistically.
- **Reordering** — see the [open question](05-fault-injection.md#reordering-open-question); if implemented, this would allow queued writes to be delivered out of the order they were written.

Each of these is driven from the same seeded random source owned by the `Network`, which is what makes a full test run reproducible end to end: the same seed always produces the same sequence of injected faults, regardless of which machine or CI runner executes the test.

### Composing with `testing/synctest`

`testing/synctest` already virtualizes time within a goroutine bubble: `time.Sleep`, timers, and context deadlines advance instantly as far as wall-clock time is concerned, while still behaving correctly relative to each other. netchaos's latency injection is designed to ride on top of this rather than duplicate it — when a simulated write needs to be delayed, netchaos uses the standard time APIs (e.g., timers) that `testing/synctest` already knows how to virtualize, rather than inventing its own clock abstraction.

This means: run a test inside `synctest.Test`, configure a `Network` with `WithLatency(50*time.Millisecond, 150*time.Millisecond)`, and the test can exercise a client that waits on that latency — without the test actually taking 50–150ms of real time to run. This is the same value proposition `testing/synctest` offers for `time.Sleep`-based code, extended to network-latency-based code.

### Single-process, multi-peer topology

Because everything happens in-process, a `Network` can simulate multiple named peers (e.g., `"client"`, `"server-a"`, `"server-b"`) all within a single `go test` process. This is what makes partition testing meaningful: a `Network` can track partition state between specific pairs of peers, and dialing/listening happens against simulated addresses within that topology rather than real host:port pairs.

Next: [04 — API Design](04-api-design.md) turns this into a concrete proposed Go API.
