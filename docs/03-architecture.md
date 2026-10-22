# 03 — Architecture

> **Status: implemented.** The transport, fault-injection layer, and `testing/synctest` integration described below are all built and tested; this doc describes the structure of the shipped code, not a proposal.

## Design goals driving the architecture

1. **In-process.** No external process, proxy, daemon, or OS-level network manipulation (no netem, no iptables). Everything happens inside the Go process running the test.
2. **Interface-compatible.** Code under test must be able to use netchaos's simulated connections through the standard `net.Conn` / `net.Listener` interfaces, without knowing it's not talking to a real socket.
3. **Deterministic.** Given the same seed and the same fixed order of `Dial`/`Listen`/`Partition`/`Heal`/`SetLatency`/`SetPacketLoss`/`SetDuplication`/`SetCorruption` calls, netchaos produces the same sequence of faults on each connection, every time, on every machine — see the [determinism contract](04-api-design.md#determinism-contract) for the derivation model and its limits under concurrent, unordered calls.
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
 │ conn A  │ ───▶  latency / loss / etc.       ───▶  │ conn B  │
 │         │ ◀───  (seeded, deterministic)     ◀───  │         │
 └─────────┘                                          └─────────┘
```

Each simulated connection satisfies `net.Conn` (`Read`, `Write`, `Close`, `LocalAddr`, `RemoteAddr`, deadlines). Internally, writes on one end don't go straight to the other end's read buffer — they pass through the fault-injection layer first, which may delay them, drop them, or block them entirely depending on the `Network`'s current policy.

### Simulated `net.Listener`

To support server-side code (`net.Listener.Accept()`), the `Network` also needs to simulate listening sockets: a registration of an address within the simulated network, and a queue of incoming simulated connections that `Accept()` pulls from. This lets test code exercise both a simulated client dialing out and a simulated server accepting connections, all within the same `Network`, without any real listening socket being opened.

### Fault-injection layer

This is where the fault categories from [05 — Fault Injection](05-fault-injection.md) are applied — latency, packet loss, bandwidth throttling, data corruption, packet duplication, and partition; reordering was considered and [deferred out of v1](05-fault-injection.md#reordering-deferred-not-in-v1):

- **Latency** — delays delivery of a write to the other end by some duration (fixed or ranged), drawn per `Write` call from the connection's derived RNG stream.
- **Packet loss** — probabilistically drops a `Write` call in its entirety instead of delivering it, using the connection's derived RNG stream to decide per write.
- **Bandwidth throttling** ([M7-5](tasks/m7-v0.2.0-implementation.md#m7-5--fault-kind-bandwidth-throttling), v0.2.0) — delays delivery in proportion to a write's size and the configured rate, computed deterministically rather than drawn from a stream: unlike the other five, it has nothing random to draw, so enabling it cannot perturb any other fault's sequence.
- **Data corruption** ([M7-9](tasks/m7-v0.2.0-implementation.md#m7-9--fault-kind-data-corruption), v0.2.0) — flips a single bit, chosen uniformly at random, in a delivered write's content, with the given probability, drawn from the connection's derived RNG stream.
- **Packet duplication** ([M7-8](tasks/m7-v0.2.0-implementation.md#m7-8--fault-kind-packet-duplication), v0.2.0) — admits a delivered write a second time, with the given probability, drawn from the connection's derived RNG stream; the duplicate reuses the original's delivery timing rather than drawing its own.
- **Partition** — when two simulated peers are partitioned, all traffic between them is dropped (typically indefinitely, until the partition is healed), rather than probabilistically, and consumes no random draws.

Latency, packet loss, bandwidth, corruption, and duplication apply globally to every connection the `Network` simulates; partition is scoped to the specific peer pair named in `WithPartition`/`Partition`/`Heal` — a deliberate asymmetry, see [04 — API Design](04-api-design.md#fault-scoping-global-vs-per-peer-pair).

Each connection's random draws come from its own stream, derived from `(masterSeed, connectionOrdinal, direction, faultKind)` rather than from one `rand.Rand` shared across the `Network`. This is what keeps a full test run reproducible end to end without making one connection's fault sequence depend on how the Go scheduler happened to interleave it with unrelated connections — see the [determinism contract](04-api-design.md#determinism-contract) for the full model. Bandwidth has no `faultKind` byte and no derived stream of its own, precisely because it draws nothing.

When more than one fault is configured on the same connection direction, there is exactly **one** evaluation point per unit, not six hooks chained or overwriting one another — a unit is checked against partition, then loss, then bandwidth, then latency, then corruption, then duplication, in that fixed order, with the draw discipline (which faults draw unconditionally vs. not at all) spelled out in the [determinism contract](04-api-design.md#determinism-contract).

Mid-stream connection reset ([M7-7](tasks/m7-v0.2.0-implementation.md#m7-7--fault-kind-mid-stream-connection-reset), v0.2.0, `Network.Reset`) is not part of this per-unit evaluator at all: unlike the six faults above, it is not a drawn, per-`Write` decision but a one-shot imperative action against whichever connections currently exist for a named peer pair, mirroring `Partition`/`Heal`'s shape rather than an `Option`. It takes no random draws and cannot perturb any connection's fault sequence.

### Concurrency model

`Network`, its connections and its listeners support concurrent callers. The lock order is acyclic:

| Held first | May acquire | Why |
|---|---|---|
| `listener.mu` | `Network.mu` | `listener.Close` deregisters its address. `DialContext` releases `Network.mu` before calling `listener.reserve`, then takes `Network.mu` again on its own to assign an ordinal. |
| `pipe.mu` | `Network.partMu`, `Network.faultMu`, `traceRecorder.mu` | A read checks partition state before returning EOF; the write evaluator reads live fault settings and records a decision while admitting a unit. |
| `Network.traceMu` | `traceRecorder.mu` | `Trace` snapshots each direction's recorder while holding the registry lock. |
| `conn.closeOnce` | `Network.resetMu`, `pipe.mu`, `deadline.mu` | `Close` deregisters the connection, closes its outgoing pipe, aborts its incoming pipe, then stops its deadlines. `closeOnce` serializes this sequence; it is not a mutex. |

`Network.resetMu` does not remain held while `Reset` aborts pipes, and `Network.mu` does not remain held while reserving a listener slot. No lock is held across a channel receive, a wait for a deadline or partition heal, or another blocking operation. `Accept`, `Read`, `Write` and a partitioned dial wait on channels after releasing their locks, so they can be woken by another goroutine and can become durably blocked inside `testing/synctest`.

Three generation channels provide wake-ups: `pipe.notify` for readable data, buffer room and close; `Network.partNotify` for partition changes; and `deadline.gen` for deadline changes or timer firing. Under the channel's lock, a state change closes the current channel and replaces it. Pipe and partition waiters snapshot the channel with their state check under one lock; deadline waiters snapshot the channel before checking expiry. After waking, all re-check state. The signal means “re-check”, so an old or concurrent signal cannot by itself imply that a deadline expired or a partition healed.

Each deadline owns at most one `time.AfterFunc` timer. Setting a new deadline stops the old timer, replaces the generation channel and arms a new timer when needed; `conn.Close` stops both deadline timers. A callback only wakes waiters, which check the stored deadline against `time.Now`, so a callback racing a reset deadline cannot impose an obsolete expiry. Each pipe owns at most one delivery timer for the earliest pending unit. Appending behind that unit leaves the timer armed; delivery re-arms it for the next unit. Closing the writer keeps accepted units and their timer until delivery and EOF. Closing the reader or calling `Reset` aborts the pipe, stops its timer and discards pending units. A delivery callback that runs after abort checks the closed state under `pipe.mu` and returns without releasing data.

### Composing with `testing/synctest`

`testing/synctest` already virtualizes time within a goroutine bubble: `time.Sleep`, timers, and context deadlines advance instantly as far as wall-clock time is concerned, while still behaving correctly relative to each other. netchaos's latency injection is designed to ride on top of this rather than duplicate it — when a simulated write needs to be delayed, netchaos uses the standard time APIs (e.g., timers) that `testing/synctest` already knows how to virtualize, rather than inventing its own clock abstraction.

This means: run a test inside `synctest.Test`, configure a `Network` with `WithLatency(50*time.Millisecond, 150*time.Millisecond)`, and the test can exercise a client that waits on that latency — without the test actually taking 50–150ms of real time to run. This is the same value proposition `testing/synctest` offers for `time.Sleep`-based code, extended to network-latency-based code.

### Single-process, multi-peer topology

Because everything happens in-process, a `Network` can simulate multiple named peers (e.g., `"client"`, `"server-a"`, `"server-b"`) all within a single `go test` process. This is what makes partition testing meaningful: a `Network` can track partition state between specific pairs of peers, and dialing/listening happens against simulated addresses within that topology rather than real host:port pairs.

Next: [04 — API Design](04-api-design.md) turns this into the concrete, shipped Go API.
