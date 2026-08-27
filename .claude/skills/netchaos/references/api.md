# netchaos API reference

`import "github.com/jpgomesr/netchaos"` — single flat package, no subpackages.
Requires Go 1.25+ (uses `testing/synctest`).

## Full v1 surface

```go
type Network struct{ /* unexported */ }

func NewNetwork(opts ...Option) *Network

type Option func(*networkConfig)

func WithSeed(seed int64) Option
func WithLatency(min, max time.Duration) Option
func WithPacketLoss(rate float64) Option
func WithPartition(peerA, peerB string) Option

func (n *Network) Dial(network, addr string) (net.Conn, error)
func (n *Network) DialContext(ctx context.Context, network, addr string) (net.Conn, error)
func (n *Network) Listen(network, addr string) (net.Listener, error)

func (n *Network) Partition(peerA, peerB string)
func (n *Network) Heal(peerA, peerB string)

func WithPeerName(ctx context.Context, name string) context.Context

var ErrUnsupportedNetwork = errors.New("netchaos: unsupported network")
var ErrConnectionRefused = errors.New("netchaos: connection refused")
var ErrAddressInUse = errors.New("netchaos: address already in use")
var ErrBacklogFull = errors.New("netchaos: accept backlog full")
```

`network` in `Dial`/`DialContext`/`Listen` accepts only `"tcp"`, `"tcp4"`,
`"tcp6"` — anything else, including `"udp"`, returns `ErrUnsupportedNetwork`
wrapped in a `*net.OpError`. UDP is out of scope for v1.

## Construction

`NewNetwork(opts...)` builds one `Network` per test scenario. It never
returns an error, and no `Option` returns an error — **invalid option
values panic at construction time** (e.g. `WithPacketLoss` outside
`[0.0, 1.0]`, or `WithLatency` with `min > max`). This is deliberate
(mirrors `regexp.MustCompile`): invalid options are programmer errors in
test code, not a runtime condition to handle. Don't wrap `NewNetwork` in
error-handling — there's nothing to handle.

## Fault options

- **`WithLatency(min, max)`** — delays each `Write` by a duration drawn
  uniformly from `[min, max]`. Equal `min`/`max` gives fixed latency.
  Applies **globally**, to every connection on this `Network`.
- **`WithPacketLoss(rate)`** — drops whole `Write` calls with probability
  `rate` (`[0.0, 1.0]`). A dropped write is a **silent gap**: it reports
  `n=len(p), nil` to the caller (never a short count or error — that's the
  only value consistent with `io.Writer`'s contract for a silent drop) and
  the bytes never reach the peer. Applies **globally**.
- **`WithPartition(peerA, peerB)`** — marks traffic between two named peers
  as dropped from construction onward. Static for the `Network`'s lifetime;
  use `Network.Partition`/`Network.Heal` to change it mid-test. **Pair-scoped**,
  not global — unlike latency/loss.

Latency and packet loss are global by design (v1 models whole-network
conditions); partition is inherently relational, hence pair-scoped. Don't
expect a `WithLatency`/`WithPacketLoss` variant scoped to one peer pair —
it doesn't exist in v1.

Fault unit = one `Write` call, not a simulated packet — no segmentation
layer. A single large `Write` is delayed/dropped as a whole.

## Dialing and listening

- **`Dial(network, addr)`** ≡ `DialContext(context.Background(), network, addr)`.
- **`DialContext(ctx, network, addr)`** — aborts with `ctx.Err()` if `ctx`
  is cancelled before the simulated connection establishes. Prefer this
  over `Dial` whenever the peer might be partitioned, since `Dial` has no
  way to time out.
- **`Listen(network, addr)`** registers a listener; `Dial`s to `addr` from
  elsewhere in the same `Network` are delivered to its `Accept`.
- Dialing an address nothing has `Listen`ed on returns an error shaped
  like `*net.OpError` wrapping `ErrConnectionRefused` — same failure shape
  code would hit against a real closed port.
- A full accept backlog returns `ErrBacklogFull`; a second `Listen` on an
  address already registered (and not yet closed) returns `ErrAddressInUse`.

**Naming a dialer for partition targeting** — `WithPeerName(ctx, name)`
attaches an identity to a context passed into `DialContext`:

```go
conn, err := network.DialContext(
    netchaos.WithPeerName(ctx, "client"), "tcp", "server-a",
)
```

Without it, a dialer gets a synthesized `ephemeral:N` identity that can
never be named in a `Partition`/`Heal` call — it's un-targetable. If a test
needs `network.Partition("client", "server-a")` to affect a specific
dialer, that dialer **must** use `WithPeerName` first.

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
dialer that named itself via `WithPeerName` can be blocked this way; an
unnamed (`ephemeral:N`) dialer never matches a `Partition` call made before
its dial completes, so it never blocks on this check.

**Consequence:** `network.Dial(...)` (which uses `context.Background()`)
into a partitioned peer **hangs forever** — no timeout, no error. If a test
dials a possibly-partitioned peer, use `DialContext` with a deadline, or
this is exactly the hang the test wants to assert (with a `t.Fatal`-style
timeout around it).

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

## Determinism contract

`WithSeed(seed)` seeds a **per-connection stream derivation**, not one
shared `rand.Rand`. Each connection's RNG stream derives from
`(masterSeed, connectionOrdinal, direction, faultKind)`:

- `connectionOrdinal` — assigned in the order connections **establish**
  (not the order `Dial` is *called*). A `Dial` blocked on a partition
  hasn't established yet, so it hasn't consumed an ordinal.
- `direction` — a connection's two directions (A→B, B→A) draw from
  independent streams.
- `faultKind` — latency and packet-loss draws are independent streams, so
  adding one option doesn't shift the other's sequence.

**Guarantee:** for a fixed seed and a fixed *order* of `Dial`/`Listen`/
`Partition`/`Heal` calls, every connection's fault sequence is identical
across runs and machines. This is what makes a failing seed reproducible.

**Limit:** the guarantee covers *call order*, not wall-clock concurrency of
already-established I/O. If a test races goroutines to call `Dial`
concurrently, which one gets which ordinal is nondeterministic — dial
sequentially before starting concurrent I/O if the test needs reproducible
per-connection fault assignment. Concurrent I/O on already-established
connections is fully deterministic per-connection regardless.

**Composition order when multiple faults apply to the same
connection+direction:** always evaluated **partition → packet loss →
latency**, by one evaluator.
- Partition short-circuits before any RNG draw (zero stream consumption) —
  a partitioned unit costs nothing to any other fault's sequence.
- A unit that clears partition draws from **every configured fault's
  stream unconditionally** — e.g. a unit that packet-loss drops still
  draws (and discards) a latency duration. This keeps each fault's draw
  index equal to the unit index, independent of the other faults' outcomes.

## Errors — match with `errors.Is`

| Sentinel | Returned by | When |
|---|---|---|
| `ErrUnsupportedNetwork` | `Dial`/`DialContext`/`Listen` | `network` arg isn't `"tcp"`/`"tcp4"`/`"tcp6"` |
| `ErrConnectionRefused` | `Dial`/`DialContext` | no `Listen`er registered for `addr` |
| `ErrAddressInUse` | `Listen` | `addr` already has an open listener |
| `ErrBacklogFull` | `Dial` | target listener's accept queue is full |

Closed-conn/listener use and deadline-exceeded reuse stdlib errors
(`net.ErrClosed`, `os.ErrDeadlineExceeded`) rather than netchaos sentinels
— check for those the same way you would against a real `net.Conn`.

## Common mistakes to avoid

- Asserting an `error` return for a packet-loss drop — it's `n=len(p), nil`.
  Assert on the *application-level* symptom (retry triggered, response
  never arrived) instead.
- Calling `Partition`/`Heal` on a peer name that was never passed to
  `WithPeerName` on the dialer side — silently never matches; the dial
  won't block and traffic won't drop, because the peer identity doesn't
  exist.
- Using `network.Dial` (not `DialContext`) against a peer that might be
  partitioned, then wondering why the test hangs — `Dial` has no deadline.
- Forgetting `WithSeed` — the test still runs, but a failing fault sequence
  isn't reproducible for debugging.
- Wrapping `NewNetwork`/`Option` calls in `if err != nil` — they don't
  return errors; invalid values panic instead.
- Running latency/timeout-heavy netchaos tests outside `synctest.Test` —
  they'll work, but burn real wall-clock time instead of virtual time.
