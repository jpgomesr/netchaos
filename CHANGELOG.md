# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **`WithPipeBound(bound int)` and `WithListenerBacklog(backlog int)`**
  (`M7-6`, closes #52) — the per-connection-direction buffer bound
  (`defaultPipeBound`, 64 KiB) and the accept-queue bound (`listenerBacklog`,
  128) are now configurable rather than fixed constants reachable only from
  in-package tests. `WithPipeBound` decides when a `Write` blocks on
  back-pressure; `WithListenerBacklog` decides when a `Dial` fails
  immediately with `ErrBacklogFull` instead of when the fixed default would.

  Both validate as positive, panicking on an invalid value the same way
  every other `Option` does, and both leave a default in place when not
  given — a plain `NewNetwork()` behaves exactly as it did before this pair
  existed. `M6-17` (#52) recommended landing this after `WithBandwidth`
  (`M7-5`) specifically so the bound has a real back-pressure scenario to
  test against — a throttle slower than the reader — rather than a synthetic
  one, and the acceptance test does exactly that.

- **`WithDuplication(rate float64)`** (`M7-8`, part of #53) — admits a
  delivered `Write` unit a second time, with the given probability, drawn
  from its own stream (`kindDuplicate`) independent of loss and latency.
  Real TCP hides duplication from the application, but `M0-3` already chose
  to model packet loss as a visible silent gap despite real TCP hiding that
  too (by retransmitting), precisely so a test can exercise what the
  application sees when the transport misbehaves; duplication is accepted
  on the same reasoning.

  The duplicate is delivered with the **same** release timing already
  computed for the original — whatever `WithLatency`/`WithBandwidth`
  decided applies to both copies, never an independently drawn delay for
  the second one. A dropped unit is never duplicated, but duplication's
  coin flip is still drawn for it, per the draw discipline.

  **Accounting:** the duplicate counts against the pipe's buffer bound like
  any other delivered bytes, and is an independent byte slice — mutating
  one copy can never affect the other, which matters once a future
  corruption fault can mutate a delivered payload in place.

  **Trace format:** `faultEvent` gained a `duplicated` field, following
  `M7-5`'s answer — emitted in a scenario's golden trace only when that
  scenario declares it, so every golden trace that predates this change
  stays byte-identical.

- **`WithBandwidth(bytesPerSecond int)`** (`M7-5`, closes #53) — throttles
  delivery to a configured rate, applied per connection direction. Modelled
  as a serialization clock, not a flat per-write delay: a direction tracks
  when its link finishes transmitting everything admitted so far, so
  back-to-back writes on a slow link queue behind each other instead of each
  drawing an independent delay — the shape of delay that produces sustained
  back-pressure once the throttle is slower than the reader. Composes
  additively with `WithLatency`'s propagation delay, evaluated as a new
  fourth stage in the fixed evaluation order: **partition, then packet loss,
  then bandwidth, then latency**.

  **Deterministic, not drawn.** Unlike every other fault, the throttle's
  delay is a function of a unit's size and the configured rate — there is
  nothing random about it, so it has no `faultKind` byte and no derived
  stream. This is a change from `M7-5`'s own task text, which specified a
  drawing kind; a deterministic delay has no draw index that could ever
  misalign, so it cannot perturb the loss/latency sequence regardless of
  whether it is configured, and reserving a draw it would never use bought
  nothing.

  No `SetBandwidth`: unlike `SetLatency`/`SetPacketLoss`, the rate is
  construction-time only — `#50` named only latency and packet loss for
  runtime mutation.

  **Trace format:** `faultEvent` gained a `serialized` field, emitted in a
  scenario's golden trace only when that scenario declares it, so every
  golden trace that predates this change stays byte-identical. `M7-7`
  through `M7-9` inherit the same per-configured-kind answer.

- **`Network.SetLatency` and `Network.SetPacketLoss`** (`M7-4`, closes #50) —
  latency and loss are now changeable mid-test, with the same live semantics
  `Partition`/`Heal` already had: **a change reaches connections that already
  exist**, not only subsequent dials. "Healthy, then degraded, then healthy"
  no longer means building a second `Network`, which meant new connections and
  a reset of every ordinal.

  Both validate exactly as their `With*` counterparts do, and panic on an
  invalid value naming the offending call — the same programmer-error
  convention `NewNetwork` uses.

  **The determinism contract widened first, in a separate change** (`M7-3`),
  because settling it afterwards would have meant the implementation had
  already picked the answer. The setters are now ordered `Network` calls
  alongside `Dial`, `Listen`, `Partition` and `Heal`. What the contract
  deliberately does *not* fix is a setter's order against in-flight I/O on
  another goroutine: which unit first sees a new value is the scheduler's
  choice, not the seed's, so sequence the calls a test depends on — write,
  then set, then write.

  **Draw discipline is unchanged.** Every configured fault still draws
  unconditionally on every unit past the partition gate, so `SetLatency(0, 0)`
  changes the value a draw produces, not whether it happens. Disabling a fault
  mid-run saves no draws and re-enabling it does not resume a paused stream.

  One internal cost, accepted knowingly by #50 rather than as a side effect:
  the per-unit fault read was lock-free and now takes a read lock, since the
  configuration it reads can change underneath it.

- **`Network.DialerFor(name)`** (`M7-2`, closes #36) — returns a
  `net.Dial`-shaped dial function that names itself, so the connections it
  creates are targetable by `Partition` and `Heal`:

  ```go
  client := myservice.NewClient(n.DialerFor("client"))
  n.Partition("client", "server") // now actually affects that client
  ```

  `Dial` cannot be targetable and never will be: a dialer's identity travels
  on a `context.Context` (`WithPeerName`), and `Dial`'s `net.Dial`-shaped
  signature has no context parameter. That put partition — a headline feature
  — out of reach of the drop-in entry point the library's no-rewrite adoption
  claim rests on, since a user who wanted both had to rewrite their client to
  take a `DialContext`. `DialerFor` closes that without changing `Dial`.

  `WithPeerName` and `DialerFor` are complements, not alternatives: the former
  for a dial that has a context anyway (and can therefore bound how long it
  waits on a partition), the latter for code that wants a plain dial function.
  **A `DialerFor` dialer does block on a partition until `Heal`**, with no
  context to bound the wait — correct, since a partition drops the SYN, but
  new for anyone who reached for `Dial` precisely because it never blocked.

- **`net.Conn` conformance suite.** `TestConnConformance` runs
  `golang.org/x/net/nettest`'s standard `TestConn` against a fault-free
  `Network`, validating the library's central "drop-in `net.Conn`" claim
  against the same harness the standard library validates `net.Pipe` with.
  This adds `golang.org/x/net` as the module's first dependency; it is
  test-only and does not appear in a consumer's non-test build graph.

### Changed

- **Addresses now have a `host:port` shape** (`M7-1`, closes #49 and #37).
  `net.SplitHostPort(conn.RemoteAddr().String())` succeeds against netchaos as
  it does against the real stack, so code under test that logs, labels a
  metric, or allow-lists on the host half of a remote address no longer takes
  a different path than it would against `net`. `Listen` also gained the
  `:0` ephemeral-port form it was missing.

  **The host half is the identity and the port half is presentation.** Nothing
  in netchaos resolves, matches, or partitions on a port, which is what lets
  addresses gain structure without invalidating anything already written:
  `Partition("server")` still reaches a connection dialed to `"server:8080"`,
  and `Listen("tcp", "server")` still registers the peer named `server` — it
  simply reports itself as `server:8000` now. An explicit port is honoured;
  `"server"` and `"server:0"` both ask netchaos to synthesize one; a malformed
  address is rejected with a `*net.AddrError`, matching what `net.Listen` and
  `net.Dial` produce for the same input.

  **This is a breaking change to every address string a test prints**, which is
  why it lands now: `M6-10` accepted it on the condition that it precede
  `v1.0.0`, since making it afterwards would break the same strings with real
  users attached. Two visible consequences beyond the shape itself — an
  unnamed dialer is now `ephemeral-N` rather than `ephemeral:N` (the old name
  parsed as host `ephemeral` plus port `N`, collapsing every unnamed dialer in
  a `Network` onto one identity), and ports are assigned in `Listen`/`Dial`
  order, so goroutines racing to `Listen` produce ports in scheduler order —
  pre-existing nondeterminism the determinism contract already documents, now
  visible in test output.

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
