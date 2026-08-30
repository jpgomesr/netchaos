# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- **An already-expired deadline now fails a `Read`/`Write` that could have
  completed without blocking.** Previously the deadline was only consulted on
  the path where the operation had to wait, so a `Read` with data already
  buffered, or a `Write` that fit in the pipe's remaining space, succeeded
  even with a deadline in the past. Both now return `os.ErrDeadlineExceeded`,
  matching `net.Pipe` (which checks its deadline before touching the buffer)
  and a real `net.Conn` (whose poller rejects the operation before the
  syscall). Found by running `golang.org/x/net/nettest`'s `TestConn`
  conformance suite (`M6-5`), whose `WriteTimeout` and `PastTimeout` subtests
  both failed against `v0.1.0`.

  This is a behaviour change: code that set a past deadline and still relied
  on the operation succeeding will now see a timeout. That combination was
  not something a real `net.Conn` ever permitted, which is why this is
  recorded as a fix rather than a breaking change.

### Added

- **`net.Conn` conformance suite.** `TestConnConformance` runs
  `golang.org/x/net/nettest`'s standard `TestConn` against a fault-free
  `Network`, validating the library's central "drop-in `net.Conn`" claim
  against the same harness the standard library validates `net.Pipe` with.
  This adds `golang.org/x/net` as the module's first dependency; it is
  test-only and does not appear in a consumer's non-test build graph.
### Changed

- **Every error from `Listen`, `Dial` and `DialContext` is now a
  `*net.OpError`, uniformly** (`M6-2`). Previously the shape depended on which
  line produced the error: a refused dial was wrapped, while an unsupported
  network, an address already in use, and an already-done context were
  returned bare. Real `net.Listen`/`net.Dial` wrap all of these, and code
  under test that type-asserts to `*net.OpError` — or calls
  `Timeout()`/`Temporary()` on the result — took a different path against
  netchaos than against the standard library, which cut against the
  substitutability the library is sold on.

  **Matching with `errors.Is` is unaffected**, since `OpError` unwraps, and
  that is the comparison style `errors.go` and `docs/04-api-design.md`
  specify. Two things do change for a caller: `err.Error()` strings now carry
  the `dial`/`listen` prefix, and direct `==` comparison against a sentinel
  (for example `err == ErrAddressInUse`, which used to succeed for `Listen`)
  no longer matches. That cost was accepted deliberately while `v0.1.0` has
  no external users.

## [v0.1.0] — 2026-08-26

First release. `netchaos` provides deterministic, in-process simulated
`net.Conn`/`net.Listener` implementations for Go tests, with seeded fault
injection and full `testing/synctest` integration.

### Requirements

- **Go 1.25 or later.** This is a hard floor, not a recommendation:
  `testing/synctest`, which netchaos's virtual-time integration depends on,
  was introduced in Go 1.25 and cannot be used on an older toolchain.

### Added

- **Core simulated transport:** `Network`, `NewNetwork`, `Dial`/`DialContext`,
  `Listen`, connection deadlines, and address/peer naming (`WithPeerName`).
  `Network.Dial` has exactly the shape of `net.Dial`, so it drops into any
  code that accepts a `func(network, addr string) (net.Conn, error)`.
- **Latency injection** (`WithLatency`): delays delivery of a write by a
  duration drawn uniformly from `[min, max]`, without reordering.
- **Packet loss** (`WithPacketLoss`): drops whole `Write` calls with a given
  probability; a dropped write is a silent gap, reported to its caller as a
  full success, matching what a real socket's sender observes when a packet
  is lost downstream.
- **Network partition** (`WithPartition`, `Network.Partition`/`Heal`): drops
  all traffic between two named peers, blocking connection establishment
  and silently discarding writes on already-established connections, until
  healed — no re-dial required.
- **Deterministic, seeded fault injection** (`WithSeed`): each connection
  derives its own RNG stream from the seed, its establishment order, and
  its direction, so a fixed seed and a fixed order of
  `Dial`/`Listen`/`Partition`/`Heal` calls reproduce an identical fault
  sequence across runs and machines. The guarantee covers the *order*
  Network methods are called in, not wall-clock concurrency of connection
  establishment itself — see `WithSeed`'s godoc for the exact limit.
- **Fixed fault composition order:** when more than one fault is configured
  on the same connection direction, they are evaluated as partition, then
  packet loss, then latency, by a single evaluator. A partitioned unit
  consumes no random draws; every other configured fault draws
  unconditionally, which keeps each fault's draw index locked to the unit
  index and makes a fault trace diffable across runs.
- **Invalid option values panic at construction time:** `NewNetwork` and
  every `Option` return no error; an out-of-range value (e.g. a
  `WithPacketLoss` rate outside `[0.0, 1.0]`) makes `NewNetwork` panic,
  analogous to `regexp.MustCompile`. Validation runs once, after every
  `Option` has been applied, so a later valid option of a given kind
  rescues an earlier invalid one of the same kind.
- **`testing/synctest` integration:** every blocking path in netchaos parks
  on a channel, never a mutex, so a bubble reaches idle correctly while
  netchaos work is in flight, and injected latency costs no real
  wall-clock time inside a bubble. A `Network` must be constructed inside
  the bubble that uses it, and connections/listeners must not be shared
  across bubbles — see the package doc for the full constraint list.
  netchaos also works outside a bubble, where latency costs real time.
- **Reproducibility harness and golden traces:** a fault-trace comparison
  harness and checked-in golden traces prove the determinism contract
  holds, and demonstrate the seed-and-reproduce workflow for a failing
  test.
- **End-to-end scenario tests:** retry under packet loss, timeout/backoff
  under latency, a circuit breaker across partition and heal, and
  multi-peer failover.
- **Runnable, compiled examples** (`example_test.go`): one per headline
  feature (latency, packet loss, partition, seeding), plus the compiled,
  verified version of the README's usage snippet and the circuit-breaker
  scenario.
- Sentinel errors: `ErrUnsupportedNetwork`, `ErrConnectionRefused`,
  `ErrAddressInUse`, `ErrBacklogFull`, all matchable with `errors.Is`.
- Project scaffolding: CI (Go 1.25 and 1.26), `golangci-lint`, issue/PR
  templates, contributing/security/conduct policies.
- Design documentation ([docs/](docs/README.md)) and a task breakdown
  ([docs/tasks/](docs/tasks/README.md)) recording how v1 was designed and
  built.

### Versioning note

Tagged `v0.1.0`, not `v1.0.0`: this surface has never had external users,
and `v0.1.0` leaves room to correct an ergonomics mistake before committing
to the stricter compatibility expectations a `v1.0.0` tag implies. The API
is stable but not frozen until `v1.0.0` — see
[docs/07 — Contributing](docs/07-contributing.md).
