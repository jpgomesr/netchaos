# M1 — Core simulated transport

> See the [task index](README.md) for the milestone map and conventions.

**Covers v1 checklist item:** *Simulated `net.Conn` / `net.Listener` (TCP-shaped) with pluggable fault injection* ([06 — Scope & Roadmap](../06-scope-and-roadmap.md)).

**What this milestone is:** the substrate. A correct in-memory, full-duplex, deadline-honouring byte-stream transport plus the `Network` topology that hands out connections — with the fault-injection hook present but **no faults implemented**. Faults arrive in [M2](m2-determinism-and-faults.md).

**Two things to get right before writing code:**

1. **`net.Pipe()` is the correctness reference, not a base to build on.** [03 — Architecture](../03-architecture.md) calls netchaos's connections "conceptually similar to `net.Pipe()`", which reads as though `net.Pipe` can be wrapped. It cannot: `net.Pipe` is *synchronous and unbuffered* — a `Write` blocks until a reader consumes it. Latency injection requires accepting a write, holding it, and releasing it later, which unbuffered synchronous handoff cannot express. M1-1 builds a buffered pipe; `net.Pipe`'s documented semantics are the reference for what "correct `net.Conn`" means.

2. **`testing/synctest`'s durable-blocking rules constrain the blocking primitive.** Per the `testing/synctest` package documentation, a goroutine is "durably blocked" — and therefore lets a bubble advance virtual time — when it blocks on a channel created *inside the bubble*, on `sync.Cond.Wait`, on `sync.WaitGroup.Wait`, or on `time.Sleep`. **Locking a `sync.Mutex` is not durably blocking.** A `Read` that parks on a mutex will stall the bubble instead of letting virtual time advance, which breaks [M3](m3-synctest-and-reproducibility.md) at its foundation. Mutexes are fine for short critical sections that always make progress; they are not fine as the primitive a blocked `Read` waits on. Relatedly, a channel or timer created inside a bubble panics if operated on from outside it — so a `Network` must be constructed inside the `synctest.Test` bubble that uses it, which is a documentation obligation for M4-1.

---

### M1-1 — Buffered in-memory full-duplex pipe

**Status:** todo
**Roadmap item:** *Simulated `net.Conn` / `net.Listener` (TCP-shaped) with pluggable fault injection*
**Depends on:** M0-3 (the fault unit determines what the queue holds), M0-6
**Blocks:** M1-2, M2-2, M2-3, M2-4

**Objective**
Build the delivery primitive: a one-directional buffered byte-stream channel where a producer can enqueue data and a consumer reads it in order, with a hook point between enqueue and delivery where the fault layer will later sit. Two of these back-to-back make a full-duplex connection.

**Scope**
- A `pipe` type: enqueue (from `Write`), dequeue (to `Read`), close from either end.
- Blocking on the read side when empty, implemented with a bubble-safe primitive (channel or `sync.Cond`) — see constraint 2 above.
- A bounded buffer, so an unread connection eventually applies back-pressure to the writer rather than growing without limit. Pick and document the bound; TCP has a receive window, and a simulated transport with none will let a test consume unbounded memory instead of exercising the flow-control path it is meant to test.
- Byte-stream semantics, not message semantics: a `Read` with a short buffer returns a partial payload and leaves the rest queued; a `Read` may coalesce data from several writes. This is what makes it TCP-shaped rather than datagram-shaped.
- A delivery hook — a single point where a queued unit can be delayed, dropped, or blocked before it becomes readable. M1 leaves it a pass-through.
- The unit held in the queue follows M0-3 (whole writes vs. simulated packets).
- Out of scope: any actual fault behaviour; `net.Conn` interface satisfaction (M1-2).

**Files**
- `pipe.go` (new)
- `pipe_test.go` (new)

**Acceptance criteria**
- [ ] Data written on one side is read on the other, byte-for-byte, in order.
- [ ] A read on an empty, open pipe blocks; it unblocks when data arrives.
- [ ] A read on an empty, closed pipe returns `io.EOF`.
- [ ] A write to a closed pipe returns an error (`io.ErrClosedPipe`, matching `net.Pipe`).
- [ ] Closing while a read is blocked unblocks that read.
- [ ] A short read buffer returns a partial payload; the remainder is returned by the next read.
- [ ] Writes larger than the buffer bound block until space is available, rather than growing the buffer.
- [ ] Double `Close` is safe and does not panic.
- [ ] A blocked read is **durably blocking** inside a `synctest.Test` bubble — verified by a test that blocks a reader, calls `synctest.Wait()`, and observes the bubble reach idle rather than deadlock-panicking.
- [ ] `go test -race` is clean under concurrent readers and writers.

**Tests**
- `TestPipeRoundTrip`, `TestPipePartialRead`, `TestPipeCoalescedReads`
- `TestPipeBlocksWhenEmpty`, `TestPipeCloseUnblocksReader`
- `TestPipeEOFAfterClose`, `TestPipeWriteAfterClose`, `TestPipeDoubleClose`
- `TestPipeBackPressure` — fill the bound, assert the next write blocks
- `TestPipeDurablyBlockingInBubble` — inside `synctest.Test`, per the acceptance criterion above
- `TestPipeConcurrent` — N writers / N readers under `-race`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M1-2 — `conn` implementing `net.Conn`

**Status:** todo
**Roadmap item:** *Simulated `net.Conn` / `net.Listener` (TCP-shaped) with pluggable fault injection*
**Depends on:** M1-1, M1-4, M0-5
**Blocks:** M1-3, M1-7, M2-2, M2-3

**Objective**
Wrap two M1-1 pipes into a full-duplex type satisfying `net.Conn`, so code under test can use it through the standard interface without knowing it is not a real socket — design goal 2 in [03 — Architecture](../03-architecture.md#design-goals-driving-the-architecture).

**Scope**
- `Read`, `Write`, `Close`, `LocalAddr`, `RemoteAddr`. Deadlines are M1-3.
- Pair construction: given two peer addresses, produce the two connection ends with their pipes crossed (A's write pipe is B's read pipe and vice versa).
- `Close` semantics: closing one end causes the peer's pending reads to drain then return `io.EOF`, and the peer's writes to fail. Decide and document whether close is immediate or drains queued-but-undelivered data — once M2-2 exists, a close with latency-delayed writes in flight has a real answer to give, and the choice must be deliberate.
- `Write` returns `(len(p), nil)` on success, per `io.Writer`'s contract that a short write must return an error.
- Compile-time interface assertions: `var _ net.Conn = (*conn)(nil)`, and the M0-5 dial-signature assertion.
- Out of scope: deadlines, faults, the `Network` that creates conns.

**Files**
- `conn.go` (new)
- `conn_test.go` (new)
- `api_test.go` (new) — compile-time assertions from M0-5

**Acceptance criteria**
- [ ] `var _ net.Conn = (*conn)(nil)` compiles.
- [ ] Full-duplex: both ends can write and read simultaneously without interfering.
- [ ] `LocalAddr`/`RemoteAddr` return the M1-4 addresses, mirrored between the two ends.
- [ ] Closing one end makes the peer's reads return `io.EOF` after draining readable data.
- [ ] Writing to a closed conn returns an error satisfying `errors.Is(err, net.ErrClosed)`.
- [ ] `Write` never reports a short write without an error.
- [ ] Close-with-data-in-flight behaviour is documented in the type's godoc, not just implied.
- [ ] `-race` clean with concurrent `Read`/`Write`/`Close` on both ends.

**Tests**
- `TestConnSatisfiesNetConn` (compile-time assertion plus a smoke round-trip)
- `TestConnFullDuplex`, `TestConnAddrsMirrored`
- `TestConnCloseEOFsPeer`, `TestConnWriteAfterClose`
- `TestConnConcurrentUse` under `-race`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M1-3 — Deadlines and concurrent-use safety

**Status:** todo
**Roadmap item:** *Simulated `net.Conn` / `net.Listener` (TCP-shaped) with pluggable fault injection*
**Depends on:** M1-2
**Blocks:** M2-2, M3-2

**Objective**
Implement the deadline half of `net.Conn` properly. This is a task rather than a bullet on M1-2 because deadlines are where hand-rolled `net.Conn` implementations most often diverge from the real thing — and because netchaos exists to test timeout logic, so a wrong deadline implementation invalidates the library's primary use case.

**Scope**
- `SetDeadline`, `SetReadDeadline`, `SetWriteDeadline`, each per `net.Conn`'s documented contract.
- A deadline in the past cancels **in-flight** calls, not just future ones. A blocked `Read` must return when the deadline is set to a past time from another goroutine.
- Timeout errors must satisfy `net.Error` with `Timeout() == true`. Use `os.ErrDeadlineExceeded`, which is what the standard library returns and what `errors.Is` checks in real code will look for.
- A zero-value `time.Time` clears the deadline.
- After a timeout the connection remains usable — a timed-out `Read` is not a fatal error, and a subsequent `Read` with a fresh deadline must succeed. This is the behaviour retry loops depend on.
- Deadlines use standard `time` primitives (timers), so `synctest` virtualizes them — the same rule [03 — Architecture](../03-architecture.md#composing-with-testingsynctest) sets for latency.
- State the concurrency guarantee explicitly in godoc: a `conn` is safe for concurrent use by multiple goroutines, as `net.Conn` requires.
- Out of scope: interaction with injected latency (covered in M2-2's criteria).

**Files**
- `deadline.go` (new) — deadline timer helper, reusable by both directions
- `conn.go` — wire into `Read`/`Write`
- `deadline_test.go` (new)

**Acceptance criteria**
- [ ] A `Read` past its deadline returns an error where `errors.Is(err, os.ErrDeadlineExceeded)` is true and the error satisfies `net.Error` with `Timeout() == true`.
- [ ] Same for `Write`.
- [ ] Setting a past deadline unblocks an already-blocked `Read` from another goroutine.
- [ ] A zero `time.Time` clears a previously set deadline.
- [ ] `SetDeadline` sets both directions; the per-direction setters affect only their own.
- [ ] After a timeout, a fresh deadline plus a new `Read` succeeds — the conn is not poisoned.
- [ ] Deadlines advance on virtual time inside a `synctest` bubble, not wall-clock time.
- [ ] `-race` clean when deadlines are set concurrently with I/O.

**Tests**
- `TestReadDeadlineExceeded`, `TestWriteDeadlineExceeded` — assert both `errors.Is` and the `net.Error` type assertion
- `TestDeadlineUnblocksInFlightRead`
- `TestZeroTimeClearsDeadline`, `TestSetDeadlineAffectsBothDirections`
- `TestConnUsableAfterTimeout`
- `TestDeadlineUsesVirtualTime` — inside `synctest.Test`, assert a 30s deadline elapses with no real delay
- `TestDeadlineRace` — concurrent `SetDeadline` and `Read` under `-race`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M1-4 — Simulated addresses and the peer naming model

**Status:** todo
**Roadmap item:** *Simulated `net.Conn` / `net.Listener` (TCP-shaped) with pluggable fault injection*
**Depends on:** M0-5
**Blocks:** M1-2, M1-5, M1-6, M2-4

**Objective**
Define what an address *is* inside a simulated network. [03 — Architecture](../03-architecture.md#single-process-multi-peer-topology) describes named peers — `"client"`, `"server-a"`, `"server-b"` — while [04 — API Design](../04-api-design.md#dialing-and-listening) uses `net.Dial`-shaped `(network, addr string)` arguments. Reconcile the two, since M2-4's partition lookup keys off peer identity.

**Scope**
- A `net.Addr` implementation: `Network() string` and `String() string`.
- Decide how a peer *name* relates to a dial *address*: is `addr` the peer name directly, is it `host:port` where host is the peer name, or does a peer own several addresses? Partition is defined between *peers*, so if one peer can hold several listening addresses, the partition lookup must resolve address → peer.
- Decide what the `network` argument accepts. [06 — Scope & Roadmap](../06-scope-and-roadmap.md#explicitly-out-of-scope-for-v1) puts UDP out of scope, so `"udp"` should be rejected with a clear error rather than silently treated as TCP. Decide the treatment of `"tcp4"`/`"tcp6"`.
- Decide whether the *dialing* peer has an identity, and how it is established — a partition is between two peers, so a dialer with no identity cannot be partitioned. This is the subtlest part of the task.
- Out of scope: registration and lookup (M1-6).

**Files**
- `addr.go` (new)
- `addr_test.go` (new)

**Acceptance criteria**
- [ ] `var _ net.Addr = (*addr)(nil)` compiles.
- [ ] The address ↔ peer-name relationship is documented and implemented as one function, used by both dial resolution and partition lookup — no second, divergent copy of the rule.
- [ ] The dialing peer's identity is defined, with the mechanism written down.
- [ ] `Network()` returns a stable, documented string.
- [ ] `"udp"` is rejected with an error naming it as out of scope for v1.
- [ ] `String()` output is stable and useful in test failure messages.

**Tests**
- `TestAddrSatisfiesNetAddr`, `TestAddrString`
- `TestPeerFromAddr` — including any multi-address-per-peer case
- `TestRejectsUDP`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M1-5 — `Network` skeleton and option plumbing

**Status:** todo
**Roadmap item:** *Simulated `net.Conn` / `net.Listener` (TCP-shaped) with pluggable fault injection*
**Depends on:** M0-5, M1-4
**Blocks:** M1-6, M1-7, M2-1

**Objective**
Build the `Network` type and the functional-options mechanism it is constructed with, per [04 — API Design](../04-api-design.md#functional-options) — the container that later holds the seeded RNG, the fault policy, and the topology.

**Scope**
- `type Network struct` with unexported fields; `func NewNetwork(opts ...Option) *Network`.
- `type Option func(*networkConfig)` and the unexported `networkConfig` the options mutate.
- `WithSeed(seed int64) Option`, storing the seed. The RNG it feeds is M2-1; here it is stored and unused.
- A default seed when `WithSeed` is not supplied. Decide deliberately: a fixed default makes every test reproducible by default but hides seed-sensitivity; a random default surfaces seed-sensitivity but makes failures non-reproducible unless the seed is reported. If random, the seed must be retrievable so a failing run can be replayed — that is the whole point of the feature.
- Fields for the fault options and topology, left unpopulated for M2.
- Concurrency: `Network` methods are called from multiple goroutines, so the topology state needs a lock from the start. Keep critical sections short and non-blocking (see the durable-blocking constraint at the top of this file).
- Out of scope: `Dial`, `Listen`, any fault option.

**Files**
- `netchaos.go` (new) — `Network`, `NewNetwork`
- `options.go` (new) — `Option`, `networkConfig`, `WithSeed`
- `netchaos_test.go`, `options_test.go` (new)

**Acceptance criteria**
- [ ] `NewNetwork()` with no options returns a usable `*Network`.
- [ ] Options are applied in argument order; a later option of the same kind overrides an earlier one.
- [ ] `WithSeed` stores the seed and it is observable (via the replay mechanism decided above).
- [ ] The default-seed decision is implemented and documented in `NewNetwork`'s godoc.
- [ ] If the default seed is random, there is a documented way to recover it from a failing run.
- [ ] `-race` clean with concurrent method calls.

**Tests**
- `TestNewNetworkDefaults`, `TestOptionOrderPrecedence`
- `TestWithSeedStored`, `TestDefaultSeedRecoverable`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M1-6 — `Network.Listen` and the simulated listener

**Status:** todo
**Roadmap item:** *Simulated `net.Conn` / `net.Listener` (TCP-shaped) with pluggable fault injection*
**Depends on:** M1-4, M1-5, M0-5
**Blocks:** M1-7, M1-8

**Objective**
Implement `Network.Listen(network, addr string) (net.Listener, error)` and the listener behind it — the address registry plus the accept queue described in [03 — Architecture](../03-architecture.md#simulated-netlistener).

**Scope**
- Register an address in the `Network`'s topology; reject a duplicate registration with an "address already in use"-shaped error, as a real listener would.
- `listener` type satisfying `net.Listener`: `Accept() (net.Conn, error)`, `Close() error`, `Addr() net.Addr`.
- A queue of pending incoming connections that `Accept` pulls from, blocking when empty on a bubble-safe primitive (constraint 2 at the top of this file — `Accept` is the single most likely place for a test to sit blocked while virtual time should be advancing).
- Decide the queue bound and what happens when it is full — real listeners have a backlog and drop or refuse beyond it.
- `Close` deregisters the address, unblocks a blocked `Accept` with an error satisfying `errors.Is(err, net.ErrClosed)`, and makes subsequent `Accept` calls fail immediately.
- Out of scope: `Dial` (M1-7), faults on the accept path (M2-4 decides whether a partition blocks connection establishment or only data).

**Files**
- `listener.go` (new)
- `netchaos.go` — `Listen`, registry
- `listener_test.go` (new)

**Acceptance criteria**
- [ ] `var _ net.Listener = (*listener)(nil)` compiles.
- [ ] `Listen` on a free address succeeds; on a taken address it returns a descriptive error.
- [ ] `Accept` on an empty queue blocks and is durably blocking inside a `synctest` bubble.
- [ ] `Close` unblocks a blocked `Accept` with an `errors.Is(err, net.ErrClosed)` error.
- [ ] `Accept` after `Close` returns immediately with the same error.
- [ ] `Addr()` returns the registered address.
- [ ] Closing a listener releases the address for re-registration.
- [ ] Backlog-full behaviour is implemented and documented.
- [ ] `-race` clean with concurrent `Accept` and `Close`.

**Tests**
- `TestListenRegistersAddr`, `TestListenDuplicateAddr`
- `TestAcceptBlocksWhenEmpty` (inside `synctest.Test`), `TestCloseUnblocksAccept`
- `TestAcceptAfterClose`, `TestCloseReleasesAddr`, `TestBacklogFull`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M1-7 — `Network.Dial` and connection establishment

**Status:** todo
**Roadmap item:** *Simulated `net.Conn` / `net.Listener` (TCP-shaped) with pluggable fault injection*
**Depends on:** M1-2, M1-6, M0-5
**Blocks:** M1-8, M2-4

**Objective**
Implement `Network.Dial(network, addr string) (net.Conn, error)` — resolve the address to a registered listener, create the M1-2 connection pair, hand one end to the dialer and enqueue the other for `Accept`. This closes the loop that makes a `Network` usable end to end.

**Scope**
- Address resolution via the M1-4 rule; error when no listener is registered, shaped like a real connection-refused (`errors.Is(err, syscall.ECONNREFUSED)` is the closest analogue — decide whether to reach for `syscall` or define a package-level `ErrConnectionRefused`, noting the `syscall` route differs across platforms).
- Create the connection pair; assign the connection ordinal that M2-1's per-connection RNG derivation depends on — assignment must be deterministic in `Dial` order, since the determinism contract in [04](../04-api-design.md#determinism-contract) fixes call order but not goroutine scheduling.
- Enqueue the server end on the listener's accept queue and return the client end.
- Ensure `Network.Dial` is directly assignable to `func(network, addr string) (net.Conn, error)` — that assignability is the whole adoption story in [03 — Architecture](../03-architecture.md#design-goals-driving-the-architecture) and [04](../04-api-design.md#dialing-and-listening).
- If M0-5 decided `DialContext` ships in v1, implement it here: honour context cancellation during establishment and return `ctx.Err()`.
- Out of scope: partition affecting dial (M2-4).

**Files**
- `netchaos.go` — `Dial`, and `DialContext` if in scope
- `dial_test.go` (new)
- `api_test.go` — the assignability assertion

**Acceptance criteria**
- [ ] `Dial` to a registered address returns a working `net.Conn`, and its peer surfaces from `Accept`.
- [ ] Data written by the dialer is read by the accepted end and vice versa.
- [ ] `Dial` to an unregistered address returns a descriptive, matchable error.
- [ ] `var _ func(string, string) (net.Conn, error) = n.Dial` compiles.
- [ ] `Dial` to a `"udp"` network is rejected per M1-4.
- [ ] Connection ordinals are assigned in `Dial` order and are stable across runs for the same call sequence.
- [ ] `LocalAddr`/`RemoteAddr` are correct and mirrored on both ends.
- [ ] If `DialContext` is in scope: a cancelled context returns `ctx.Err()` and leaks no goroutine or queue entry.
- [ ] `-race` clean with concurrent dials to the same listener.

**Tests**
- `TestDialAcceptRoundTrip`, `TestDialUnregisteredAddr`
- `TestDialAssignableAsDialFunc`
- `TestDialOrdinalsDeterministic` — same call sequence twice, compare ordinals
- `TestConcurrentDials` under `-race`
- `TestDialContextCancelled` (if in scope)
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`

---

### M1-8 — Lifecycle and error semantics

**Status:** todo
**Roadmap item:** *Simulated `net.Conn` / `net.Listener` (TCP-shaped) with pluggable fault injection*
**Depends on:** M1-6, M1-7
**Blocks:** M2-4

**Objective**
Make the edge cases behave like a real network stack, and make every error matchable with `errors.Is`. Code under test contains error-handling branches — that is usually *why* it is under test — so an error the caller cannot classify makes the branch untestable.

**Scope**
- Audit every error the package returns; give each a package-level sentinel or a well-known standard-library error, and document which. Candidates: connection refused, address in use, use of closed connection/listener, deadline exceeded, invalid option, unknown peer.
- Decide whether `Network` itself needs a `Close`, and if so what it does to open conns and listeners. [04 — API Design](../04-api-design.md) does not propose one; a test that leaks goroutines across bubble exit will fail `synctest.Test`, which waits for all bubble goroutines to exit — so if any internal goroutines outlive their conn, a `Close` is not optional.
- Goroutine-leak audit: every internal goroutine must terminate on the close of the thing that owns it. Add a leak check to the test suite.
- Behaviour when the last reference to a conn/listener is dropped without `Close` — document it rather than relying on finalizers (`runtime.AddCleanup` and finalizers run *outside* any bubble, per the `testing/synctest` docs, so they cannot be part of the design).
- Out of scope: fault-related errors (M2-6).

**Files**
- `errors.go` (new) — sentinels and their godoc
- `netchaos.go`, `conn.go`, `listener.go` — replace ad-hoc errors
- `errors_test.go`, `leak_test.go` (new)

**Acceptance criteria**
- [ ] Every returned error is matchable with `errors.Is` against a documented sentinel or standard error.
- [ ] Sentinels are listed together in `errors.go` with godoc explaining when each occurs.
- [ ] The `Network.Close` question is decided, implemented if yes, and documented either way.
- [ ] No goroutine outlives the conn, listener, or `Network` that owns it — asserted by a leak test.
- [ ] A test that creates a `Network`, dials, writes, and closes runs to completion inside `synctest.Test` without the bubble reporting live goroutines.
- [ ] Using a conn or listener after `Close` returns `net.ErrClosed`, never a panic.

**Tests**
- `TestErrorSentinels` — table-driven over each failure mode, asserting `errors.Is`
- `TestNoGoroutineLeaks` — goroutine count before/after a full dial-write-close cycle
- `TestBubbleCleanExit` — a full scenario inside `synctest.Test`
- `TestUseAfterCloseNoPanic`
- Verify: `go build ./... && go vet ./... && gofmt -l . && go test -race ./... && golangci-lint run`
