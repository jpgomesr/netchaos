# netchaos API reference

`import "github.com/jpgomesr/netchaos"` — single flat package, no
subpackages. Requires Go 1.25+ (uses `testing/synctest`). Add it with
`go get github.com/jpgomesr/netchaos@v0.2.0` (or the latest tag — check
`go list -m -versions github.com/jpgomesr/netchaos` if unsure).

This reference is self-contained: everything needed to use the library
correctly from an external project/API lives here, without needing the
netchaos repo's own `docs/` checked out.

## Full surface (v0.2.0)

```go
type Network struct{ /* unexported */ }

func NewNetwork(opts ...Option) *Network

type Option func(*networkConfig)

func WithSeed(seed int64) Option
func WithLatency(min, max time.Duration) Option
func WithPacketLoss(rate float64) Option
func WithPartition(peerA, peerB string) Option
func WithBandwidth(bytesPerSecond int) Option
func WithDuplication(rate float64) Option
func WithCorruption(rate float64) Option
func WithPipeBound(bound int) Option
func WithListenerBacklog(backlog int) Option

func (n *Network) Dial(network, addr string) (net.Conn, error)
func (n *Network) DialContext(ctx context.Context, network, addr string) (net.Conn, error)
func (n *Network) DialerFor(name string) func(network, addr string) (net.Conn, error)
func (n *Network) Listen(network, addr string) (net.Listener, error)

func (n *Network) Partition(peerA, peerB string)
func (n *Network) Heal(peerA, peerB string)
func (n *Network) Reset(peerA, peerB string)

func (n *Network) SetLatency(min, max time.Duration)
func (n *Network) SetPacketLoss(rate float64)

func (n *Network) Trace() []FaultEvent

func WithPeerName(ctx context.Context, name string) context.Context

type Side int

const (
    SideDialer Side = iota
    SideAcceptor
)

type FaultEvent struct {
    Ordinal uint64
    Side    Side
    Seq     uint64

    Partitioned bool
    Dropped     bool
    Duplicated  bool
    Corrupted   bool

    Delay         time.Duration
    Serialization time.Duration
    Effective     time.Duration
}

var ErrUnsupportedNetwork = errors.New("netchaos: unsupported network")
var ErrConnectionRefused = errors.New("netchaos: connection refused")
var ErrAddressInUse = errors.New("netchaos: address already in use")
var ErrBacklogFull = errors.New("netchaos: accept backlog full")
```

`network` in `Dial`/`DialContext`/`Listen`/`DialerFor` accepts only
`"tcp"`, `"tcp4"`, `"tcp6"` — anything else, including `"udp"`, returns
`ErrUnsupportedNetwork` wrapped in a `*net.OpError`. UDP is out of scope.

## Construction

`NewNetwork(opts...)` builds one `Network` per test scenario. It never
returns an error, and no `Option` returns an error — **invalid option
values panic at construction time** (e.g. `WithPacketLoss` outside
`[0.0, 1.0]`, `WithLatency`/`SetLatency` with `min > max`, a non-positive
`WithBandwidth`/`WithPipeBound`/`WithListenerBacklog`). This mirrors
`regexp.MustCompile`: invalid options are programmer errors in test code,
not a runtime condition to handle. Don't wrap `NewNetwork`/`Option` calls
in error-handling — there's nothing to handle. `SetLatency`/`SetPacketLoss`
(the runtime setters) panic the same way, with the same message shape.

## Fault options

The six per-unit kinds below (everything except `Network.Reset`, covered
separately in [Mid-stream connection reset](#mid-stream-connection-reset))
share one evaluator (`installFaultPolicy`) with a **fixed evaluation
order** — see [Fault composition and draw discipline](#fault-composition-and-draw-discipline)
below. All are **global** except `WithPartition`, which is pair-scoped.

- **`WithLatency(min, max)`** — delays delivery by a duration drawn
  uniformly from `[min, max]`. Equal `min`/`max` gives fixed latency
  (still drawn, not skipped — see the draw discipline).
- **`WithPacketLoss(rate)`** — drops whole `Write` calls with probability
  `rate` (`[0.0, 1.0]`). A dropped write is a **silent gap**: it reports
  `n=len(p), nil` to the caller (never a short count or error) and the
  bytes never reach the peer.
- **`WithBandwidth(bytesPerSecond)`** — throttles delivery to a rate,
  applied **per connection direction** (a full-duplex conn is throttled
  each way independently). Models a serialization clock, not a flat
  per-unit delay: back-to-back writes on a slow link queue behind each
  other (`busyUntil`), so a throttle can produce *sustained* back-pressure
  once it's slower than the reader. **Draws nothing** — a deterministic
  function of unit size and rate, so it can never perturb the loss/
  latency/duplication/corruption sequence. Composes **additively** with
  `WithLatency`: total per-unit delay is serialization time + latency
  draw, never one superseding the other. No runtime setter exists for
  this — it's construction-time only.
- **`WithDuplication(rate)`** — admits a delivered `Write` unit a second
  time, with probability `rate`, drawn from its own stream. The duplicate
  reuses the **same** release timing (bandwidth + latency) already
  computed for the original, and carries whatever `WithCorruption`
  already did to the first copy — never independently delayed or
  independently corrupted. Counts against the pipe buffer bound like any
  other delivered bytes. No runtime setter.
- **`WithCorruption(rate)`** — flips a single bit, chosen uniformly at
  random, in a delivered unit's content, with probability `rate`. Length
  is never affected, and the caller's original buffer is never mutated
  (`conn.Write` copies before the pipe sees it). A zero-length write still
  draws the decision but has no bit to flip. No runtime setter.
- **`WithPartition(peerA, peerB)`** — marks traffic between two named
  peers as dropped from construction onward. Static for the `Network`'s
  lifetime; use `Network.Partition`/`Network.Heal` to change it mid-test.
  **Pair-scoped**, not global. Draws **nothing** — partition is
  deterministic by nature, so partitioning one pair can never perturb any
  other connection's fault sequence.
- **`WithPipeBound(bound)`** / **`WithListenerBacklog(backlog)`** —
  structural bounds, not faults. `WithPipeBound` sets the per-connection-
  direction buffer size that decides when a `Write` blocks on
  back-pressure (default 64 KiB). `WithListenerBacklog` sets the accept
  queue size that decides when `Dial` fails immediately with
  `ErrBacklogFull` (default 128). Both leave the package default in place
  when not given, and both panic on a non-positive value. Back-pressure
  from a configured `WithPipeBound` is easiest to *exercise* through a
  `WithBandwidth`-throttled reader — otherwise a fast in-process reader
  rarely lets the buffer fill.

Fault unit = one `Write` call, not a simulated packet — no segmentation
layer. A single large `Write` is delayed/dropped/corrupted/duplicated as a
whole, never split.

## Dialing and listening

- **`Dial(network, addr)`** ≡ `DialContext(context.Background(), network, addr)`.
  Exactly `net.Dial`'s shape — drop it into any `func(network, addr string) (net.Conn, error)`
  slot (a `net.Dialer`-shaped constructor, etc.). An unnamed `Dial` gets a
  synthesized `ephemeral-N:port` identity that can never be targeted by
  `Partition`/`Heal`/`Reset`.
- **`DialContext(ctx, network, addr)`** — aborts with `ctx.Err()` if `ctx`
  is cancelled before the simulated connection establishes. Prefer this
  over `Dial` whenever the peer might be partitioned, since `Dial` has no
  way to time out.
- **`DialerFor(name string) func(network, addr string) (net.Conn, error)`**
  — the fix for "I want partition-targeting but my client constructor only
  takes a `net.Dial`-shaped function." Returns a closure with the exact
  `net.Dial` shape, pre-named so connections it creates are targetable by
  `Partition`/`Heal`/`Reset`:
  ```go
  client := myservice.NewClient(network.DialerFor("client"))
  network.Partition("client", "server") // now actually affects that client
  ```
  A `DialerFor` dialer **is** subject to the dial-time partition check
  (it blocks against a partitioned peer, same as a `WithPeerName`-named
  `DialContext`), but has no context to bound the wait — use
  `DialContext` + `WithPeerName` instead if the dial must fail rather
  than hang.
- **`Listen(network, addr)`** registers a listener; `Dial`s to `addr` from
  elsewhere in the same `Network` are delivered to its `Accept`.
  `addr` may be written with or without a port — see
  [Address shape](#address-shape).
- Dialing an address nothing has `Listen`ed on returns an error shaped
  like `*net.OpError` wrapping `ErrConnectionRefused` — same failure shape
  code would hit against a real closed port.
- A full accept backlog returns `ErrBacklogFull`; a second `Listen` on an
  address already registered (same host, any port) returns `ErrAddressInUse`.

**Naming a dialer for partition/reset targeting** — two equivalent ways:

```go
// 1. WithPeerName on a context passed to DialContext:
conn, err := network.DialContext(
    netchaos.WithPeerName(ctx, "client"), "tcp", "server-a",
)

// 2. DialerFor, when the call site only accepts a plain dial function:
dial := network.DialerFor("client")
conn, err := dial("tcp", "server-a")
```

Without one of these, a dialer gets a synthesized `ephemeral-N` identity
that can never be named in a `Partition`/`Heal`/`Reset` call — it's
un-targetable. `WithPeerName(ctx, name)` strips a `:port` suffix from
`name` before using it, so `WithPeerName(ctx, "client:1234")` and
`WithPeerName(ctx, "client")` target the same peer identity.

## Address shape

A netchaos address has a host and a port, and the two do different jobs:

- **Host = identity.** What `Dial`/`Listen` resolve against and what
  `Partition`/`Heal`/`Reset` name.
- **Port = presentation.** Exists so `net.SplitHostPort(conn.RemoteAddr().String())`
  succeeds, matching a real `net.Conn`. **Nothing resolves, matches, or
  partitions on a port** — `Partition("server")` reaches a connection
  dialed to `"server:8080"`.

| Written | Peer identity | `Addr().String()` |
|---|---|---|
| `Listen("tcp", "server")` | `server` | `server:8000` (synthesized) |
| `Listen("tcp", "server:0")` | `server` | `server:8001` (synthesized, `:0` form) |
| `Listen("tcp", "server:8080")` | `server` | `server:8080` (honoured) |
| `Dial("tcp", "server:8080")` | dials peer `server` | local `ephemeral-0:32768` |

Two listeners naming the same host collide regardless of port (one peer,
one listener). A malformed address returns `*net.AddrError`, matching
`net.Listen`/`net.Dial`. Ports are synthesized in `Listen`/`Dial` order —
the same order the determinism contract already fixes — so two goroutines
racing to `Listen` concurrently get ports in whichever order the scheduler
picks; that's a pre-existing nondeterminism made newly *visible* (a port
shows up in `RemoteAddr().String()`, unlike a bare connection ordinal).

## Dynamic partition control

```go
func (n *Network) Partition(peerA, peerB string) // drop traffic between the pair
func (n *Network) Heal(peerA, peerB string)       // restore it
```

Pairs are unordered — `Partition("a","b")` and `Partition("b","a")` name
the same pair; either order heals the other.

**Effect on connection establishment:** `Dial`/`DialContext` **blocks**
while the target pair is partitioned, returning only once healed (or
`ctx.Err()` if the context is done first) — mirrors a dropped SYN. Only a
dialer that named itself (`WithPeerName` or `DialerFor`) can be blocked
this way; an unnamed dialer never matches a `Partition` call, so it never
blocks on this check. The wait happens *before* the connection's ordinal
is assigned, so a dial that blocks and is then cancelled never shifts any
other connection's fault sequence.

**Consequence:** `network.Dial(...)` (uses `context.Background()`) into a
partitioned peer **hangs forever** — no timeout, no error. If a test
dials a possibly-partitioned peer, use `DialContext` with a deadline, or
this is exactly the hang the test wants to assert (wrap it in its own
`t.Fatal`-style timeout).

**Effect on already-established connections:** writes into a partitioned
pair are accepted and silently discarded (same silent-gap model as packet
loss); reads block until their deadline. `Heal` restores traffic without a
re-dial. Data written while partitioned is **discarded**, not queued for
delivery on `Heal`.

**No-op cases (no error):**
- `Partition` on peers that have never `Dial`ed/`Listen`ed — legitimate
  "start partitioned" setup.
- `Heal` with no partition in effect for that pair — idempotent, safe to
  call unconditionally from `defer`/cleanup.

## Mid-stream connection reset

```go
func (n *Network) Reset(peerA, peerB string)
```

Closes the gap `Partition` deliberately leaves: `Partition` is a silent
black hole, never an `ECONNRESET`. `Reset` abruptly terminates every
**currently-established** connection between the named peers — both
ends' subsequent `Read`/`Write`, and any already in-flight on another
goroutine, fail with an error satisfying `errors.Is(err, syscall.ECONNRESET)`,
wrapped in a `*net.OpError`. A reset connection **stays reset** — there is
no "un-reset."

Three ways this differs from `Partition`, all deliberate:

1. **No effect on `Dial`.** Doesn't gate establishment; only touches
   connections that already exist.
2. **Does not persist.** Acts once, on what's live *at the moment it's
   called*. A connection dialed for the same pair afterward is unaffected
   — the real-RST analogy (an RST invalidates existing state, it doesn't
   block a fresh connection).
3. **No-op if nothing is currently established** between the named peers
   — same convention as `Partition`/`Heal`.

Naming resolves exactly as `Partition`/`Heal` do — an unnamed dialer's
`ephemeral-N` identity isn't practically targetable here either.

**What it's for:** testing reconnect/retry logic against an abrupt
failure — does a client detect `ECONNRESET` and reconnect (vs. treating it
like a timeout), does a connection pool evict a reset connection instead
of handing it out again.

## Runtime fault mutation

```go
func (n *Network) SetLatency(min, max time.Duration)
func (n *Network) SetPacketLoss(rate float64)
```

Same live semantics as `Partition`/`Heal`: **a change applies to
already-established connections**, not just subsequent dials — the whole
point is "healthy, then degraded, then healthy" on one live connection
without rebuilding the `Network`. Panics on invalid values, same shape as
the equivalent `Option`.

Draw discipline is unchanged by a setter: every configured fault still
draws unconditionally on every unit past the partition gate. Two
consequences easy to get wrong:

- `SetLatency(0, 0)` is an **explicit fixed-zero delay**, not "off" — it
  still draws. Disabling a fault mid-run does not save draws, and
  re-enabling it does not resume a paused stream.
- Enabling a fault kind that was never configured at construction time
  **does** begin drawing from that kind's stream mid-run — this shifts
  nothing else, since kinds are independent by derivation.

**No setter exists for `WithBandwidth`, `WithDuplication`, or
`WithCorruption`** — those are construction-time only. Only latency and
packet loss are mutable at runtime.

**Ordering limit:** the determinism contract fixes a setter's order
*relative to other `Network` calls*, not relative to in-flight I/O on
another goroutine. If one goroutine writes in a loop while another calls
`SetPacketLoss`, which unit first sees the new rate is the scheduler's
choice, not the seed's — sequence explicitly if it matters: write, then
set, then write.

## Full fault trace export

```go
func (n *Network) Trace() []FaultEvent

type Side int
const (
    SideDialer Side = iota
    SideAcceptor
)

type FaultEvent struct {
    Ordinal uint64        // which Dial-order connection this event belongs to
    Side    Side          // which end produced it
    Seq     uint64        // position within this direction's own trace, from 0

    Partitioned bool
    Dropped     bool
    Duplicated  bool // NOT mutually exclusive with Dropped/Corrupted
    Corrupted   bool // NOT mutually exclusive with Dropped/Duplicated

    Delay         time.Duration // drawn from the latency stream
    Serialization time.Duration // link-busy contribution under WithBandwidth
    Effective     time.Duration // delay applied AFTER serialization finished
}
```

`Trace()` returns **every** fault decision recorded across every
connection `n` has ever dialed, in `(Ordinal, Side, Seq)` order. This is
what makes assertions like *"exactly three writes were dropped"* possible
from outside the package — previously only the downstream consequence (a
short read) was observable. The returned slice is a copy; mutating it, or
a later `Trace()` call, never affects the other.

**Reading the fields correctly:**

- `Partitioned`, `Dropped`, `Duplicated`, `Corrupted` are **independent,
  not one-of-many** — the draw discipline records `Duplicated`/`Corrupted`
  even for a unit `Dropped` already discarded, so more than one can be
  `true` on the same event. `Partitioned` is the one exception: a
  partitioned unit draws nothing, so every other field on that event is
  its zero value.
- **`Delay == 0` is ambiguous by itself** — it does not distinguish
  "latency not configured" from "configured, drew exactly zero"
  (`SetLatency(0, 0)`/`WithLatency(0, 0)` still draws). Don't infer "no
  draw happened" from a zero `Delay`.
- **`Effective` is relative to when serialization finished**, not the
  total delay from `Write`. A unit's full delay from write to delivery is
  `Serialization + Effective`.
- **On a `Dropped` event, `Serialization` and `Effective` are always
  zero** — loss short-circuits the evaluator before either is computed.
  `Delay` may still be non-zero on a dropped event (drawn, then
  discarded) — it describes what was drawn, not a delivery that happened.

**What `Trace()` does NOT cover:**
- `Network.Reset` — an imperative action, not a per-unit decision. No event.
- A dial that never established (refused, blocked-then-cancelled, full
  backlog) — no pipes were ever created, so nothing is traced.

**Retention:** a connection's trace stays in `Trace()`'s output for the
`Network`'s own lifetime, **even after the connection has been closed** —
the common `defer c.Close()` case. This is unlike `Reset`'s internal
target registry, which is pruned on `Close`; `Trace()` deliberately isn't,
because reading a trace after the connection producing it has already
closed is the ordinary case, not an edge case.

## Determinism contract

`WithSeed(seed)` seeds a **per-connection stream derivation**, not one
shared `rand.Rand`. Each connection direction's RNG stream derives from
`(masterSeed, connectionOrdinal, direction, faultKind)`:

- `connectionOrdinal` — assigned in the order connections **establish**
  (not the order `Dial` is *called*). A `Dial` blocked on a partition
  hasn't established yet, so it hasn't consumed an ordinal. A dial that
  fails on any path (refused, backlog full, cancelled) never burns one.
- `direction` — a connection's two directions (dialer→acceptor,
  acceptor→dialer) draw from independent streams.
- `faultKind` — latency, packet-loss, duplication, and corruption draws
  are each their own independent stream, so adding one option never
  shifts another's sequence. Bandwidth and `Reset` draw **nothing** (no
  stream at all), so enabling/calling them can never perturb any other
  fault's sequence either.

**Guarantee:** for a fixed seed and a fixed *order* of `Dial`, `Listen`,
`Partition`, `Heal`, `SetLatency`, `SetPacketLoss` calls, every
connection's fault sequence is identical across runs and machines. This
is what makes a failing seed reproducible.

**Limit:** the guarantee covers *call order*, not wall-clock concurrency.
Two goroutines racing to `Dial` concurrently get ordinals in whichever
order the scheduler picks — dial sequentially before starting concurrent
I/O if a test needs reproducible per-connection fault assignment.
Concurrent I/O on already-established connections is fully deterministic
per-connection regardless. A setter (`SetLatency`/`SetPacketLoss`) racing
in-flight I/O on another goroutine has the same limit — see
[Runtime fault mutation](#runtime-fault-mutation).

### Fault composition and draw discipline

When multiple faults apply to the same connection direction, one
evaluator decides the outcome per unit, in exactly this fixed order:

**partition → packet loss → bandwidth → latency → corruption → duplication**

- **Partition** short-circuits before any draw — zero stream consumption,
  so partitioning one pair never perturbs any other connection.
- **Packet loss** is the next gate: a dropped unit never reaches the
  link (no serialization cost), is never corrupted (nothing to flip a
  bit in), and is never duplicated (nothing to copy) — but see the draw
  rule below, it still draws.
- **Bandwidth** computes a deterministic delay from unit size and rate —
  draws nothing, so its position in the order doesn't interact with the
  draw discipline at all.
- **Latency** adds a drawn delay on top of whatever bandwidth produced.
- **Corruption** happens before duplication specifically so a duplicated
  unit's second copy carries whatever corruption already did to the
  first, not an independently corrupted copy.

**The draw discipline — the part most likely to surprise:** a unit that
clears the partition gate draws from **every configured, drawing**
fault's stream **unconditionally**, regardless of what an earlier fault
in the order already decided. A unit packet loss drops still draws (and
discards) a latency duration, and still draws corruption's and
duplication's coin flips even though nothing is left to corrupt or
duplicate. This keeps every drawing fault's draw index equal to the unit
index on that direction, independent of what any other fault decided —
which is what makes `Trace()` (or a golden trace file) diffable
position-for-position across runs. Bandwidth and `Reset` are outside this
rule entirely, since neither has a stream to draw from in the first
place.

## Errors — match with `errors.Is`

| Sentinel | Returned by | When |
|---|---|---|
| `ErrUnsupportedNetwork` | `Dial`/`DialContext`/`DialerFor`/`Listen` | `network` arg isn't `"tcp"`/`"tcp4"`/`"tcp6"` |
| `ErrConnectionRefused` | `Dial`/`DialContext`/`DialerFor` | no `Listen`er registered for `addr` |
| `ErrAddressInUse` | `Listen` | `addr` already has an open listener (same host, any port) |
| `ErrBacklogFull` | `Dial`/`DialContext`/`DialerFor` | target listener's accept queue is full |

Every error is a `*net.OpError` (uniform shape across `Listen`/`Dial`/
`DialContext`) — `errors.As` for the wrapper, `errors.Is` for the
sentinel; don't compare with `==`. Closed-conn/listener use and
deadline-exceeded reuse stdlib errors (`net.ErrClosed`,
`os.ErrDeadlineExceeded`) rather than netchaos sentinels — check for
those the same way you would against a real `net.Conn`. A reset
connection's errors satisfy `errors.Is(err, syscall.ECONNRESET)`.

## Common mistakes to avoid

- Asserting an `error` return for a packet-loss drop — it's `n=len(p), nil`.
  Assert on the *application-level* symptom (retry triggered, response
  never arrived) instead.
- Calling `Partition`/`Heal`/`Reset` on a peer name that was never named
  via `WithPeerName` or `DialerFor` on the dialer side — silently never
  matches; the dial won't block, traffic won't drop, nothing resets,
  because the peer identity doesn't exist as far as that call is concerned.
- Using `network.Dial` (not `DialContext`) against a peer that might be
  partitioned, then wondering why the test hangs — `Dial` has no deadline.
- Forgetting `WithSeed` — the test still runs (default seed `1`, not
  random), but a deliberately-varied seed is what makes a specific failing
  sequence reproducible on purpose.
- Wrapping `NewNetwork`/`Option`/`SetLatency`/`SetPacketLoss` calls in
  `if err != nil` — none of them return errors; invalid values panic.
- Running latency/timeout/reset-heavy netchaos tests outside
  `synctest.Test` — they'll work, but burn real wall-clock time instead
  of virtual time.
- Reading `FaultEvent.Delay == 0` as "latency wasn't configured" — it
  isn't; see [Full fault trace export](#full-fault-trace-export).
- Expecting `Trace()` to show a `Network.Reset` — resets aren't per-unit
  decisions and produce no trace event.
- Expecting a `WithLatency`/`WithPacketLoss`/`WithBandwidth`/
  `WithDuplication`/`WithCorruption` variant scoped to one peer pair — it
  doesn't exist as of `v0.2.0`; only `WithPartition` is pair-scoped, and
  all five other faults are global to the `Network`.
