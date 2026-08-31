package netchaos

import (
	"context"
	"net"
	"sync"
	"time"
)

// Network is a simulated network topology: a set of named peers that can
// Dial one another and Listen for connections, subject to a configurable
// fault policy (latency, packet loss, and partition).
//
// Network intentionally has no Close method: netchaos spawns no goroutines
// of its own. The only asynchronous work is two time.AfterFunc callbacks —
// a deadline timer (deadline.go) and a per-pipe latency delivery timer
// (latency.go) — and both are owned by a single conn and stopped by
// conn.Close/pipe.close, so nothing outlives the conn that created it and
// there is nothing for a Network-wide Close to reap (see leak_test.go and
// synctest_test.go). These callbacks are also the only place a
// netchaos-internal goroutine acquires a sync.Mutex; the window is bounded
// because no lock is ever held across a blocking operation. Revisit this
// only if a resource appears that is not scoped to a single conn.
//
// A Network must be constructed inside the testing/synctest bubble that
// will use it: synctest panics if a channel or timer created inside a
// bubble is later operated on from outside it, and every conn, listener,
// and deadline timer a Network creates is reachable from goroutines running
// inside that bubble.
//
// A Network is safe for concurrent use by multiple goroutines.
type Network struct {
	seed int64

	// faultMu guards faults, which SetLatency and SetPacketLoss write and
	// the per-unit evaluator reads (M7-4). This read was lock-free until the
	// configuration became mutable; #50 accepted that cost explicitly rather
	// than as a side effect, since it is on the delivery hot path.
	faultMu sync.RWMutex
	faults  faultConfig

	partMu     sync.RWMutex
	partitions map[pairKey]struct{}
	partNotify chan struct{} // closed and replaced on every Partition/Heal

	mu          sync.Mutex
	listeners   map[string]*listener // key: peerName(addr)
	nextOrdinal uint64

	// nextListenPort is the port the next listener that named none will be
	// given (M7-1). It advances in Listen order, which the determinism
	// contract already fixes, so it needs no widening of that contract — and
	// it inherits the contract's stated limit unchanged: two goroutines
	// racing to Listen get ports in whichever order the scheduler picks.
	// Unlike an ordinal, a port is visible in RemoteAddr().String(), so that
	// pre-existing nondeterminism is newly visible in test output.
	nextListenPort int

	// pipeBound and listenerBacklog are set once, from networkConfig, in
	// NewNetwork, and read (never written again) by DialContext and Listen
	// respectively (M7-6, #52). They default to the package constants
	// defaultPipeBound and listenerBacklog when WithPipeBound /
	// WithListenerBacklog are not given, so a plain NewNetwork() behaves
	// exactly as it did before this pair of options existed.
	pipeBound       int
	listenerBacklog int

	// resetMu guards resetTargets, Network.Reset's registry of currently
	// established conns per peer pair (M7-7). Separate from partMu: reset
	// is an imperative, one-shot action on conns that exist right now, not
	// standing state a per-unit evaluator reads, so it shares no data path
	// with the partition machinery.
	resetMu      sync.Mutex
	resetTargets map[pairKey][]*conn
}

// NewNetwork returns a new, empty Network configured by opts. Options are
// applied in argument order; a later option of the same kind overrides an
// earlier one. Every Option is validated once, after all of them have been
// applied — see Option and WithSeed for what that means for invalid values
// and for the determinism contract.
//
// If WithSeed is not given, NewNetwork uses the fixed default seed 1 rather
// than a random one: netchaos's whole premise is that a test run is
// reproducible from its seed, so the common case of "just run the test"
// should be reproducible by default too. Because the default is a fixed
// constant, there is nothing to "recover" from a failing run — unlike a
// random default, which would need an accessor to report the seed it
// picked.
func NewNetwork(opts ...Option) *Network {
	cfg := networkConfig{seed: defaultSeed}
	for _, opt := range opts {
		opt(&cfg)
	}
	cfg.validate()

	pipeBound := defaultPipeBound
	if cfg.pipeBoundEnabled {
		pipeBound = cfg.pipeBound
	}
	backlog := listenerBacklog
	if cfg.listenerBacklogEnabled {
		backlog = cfg.listenerBacklog
	}

	n := &Network{
		seed: cfg.seed,
		faults: faultConfig{
			lossEnabled:      cfg.lossEnabled,
			lossRate:         cfg.lossRate,
			latencyEnabled:   cfg.latencyEnabled,
			latencyMin:       cfg.latencyMin,
			latencyMax:       cfg.latencyMax,
			bandwidthEnabled: cfg.bandwidthEnabled,
			bandwidthBPS:     cfg.bandwidthBPS,
			duplicateEnabled: cfg.duplicateEnabled,
			duplicateRate:    cfg.duplicateRate,
			corruptEnabled:   cfg.corruptEnabled,
			corruptRate:      cfg.corruptRate,
		},
		partitions:      make(map[pairKey]struct{}, len(cfg.staticPartitions)),
		partNotify:      make(chan struct{}),
		listeners:       make(map[string]*listener),
		nextListenPort:  listenPortBase,
		pipeBound:       pipeBound,
		listenerBacklog: backlog,
		resetTargets:    make(map[pairKey][]*conn),
	}
	for _, p := range cfg.staticPartitions {
		n.partitions[newPairKey(peerName(p.peerA), peerName(p.peerB))] = struct{}{}
	}
	return n
}

// Listen registers a simulated listener at laddr within n. Connections
// dialed to laddr from elsewhere in n are delivered to this listener's
// Accept. network must be "tcp", "tcp4", or "tcp6" (v1 is TCP-shaped only;
// "udp" is rejected). Listening on an address already registered by another
// open listener returns an error satisfying errors.Is(err, ErrAddressInUse).
//
// laddr may be written with or without a port. "server" and "server:0" both
// ask netchaos to synthesize one, the way ":0" does against the real stack;
// "server:8080" keeps the port the caller wrote. Either way the peer's
// identity — what Partition, Heal and a dial resolve against — is the host
// half alone, so a listener on "server:8080" is still the peer named
// "server". A malformed address is rejected with a *net.AddrError, matching
// what net.Listen produces for the same input.
//
// Addresses that name the same host collide regardless of port: a second
// Listen on "server:9090" while "server:8080" is open returns
// ErrAddressInUse. One peer, one listener — see the addr type for why
// identity deliberately stops at the host half.
func (n *Network) Listen(network, laddr string) (net.Listener, error) {
	if err := validateNetwork(network); err != nil {
		return nil, n.listenOpError(network, laddr, err)
	}
	peer, port, explicit, err := splitAddr(laddr)
	if err != nil {
		return nil, n.listenOpError(network, laddr, err)
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	if _, ok := n.listeners[peer]; ok {
		return nil, n.listenOpError(network, laddr, ErrAddressInUse)
	}
	if !explicit {
		port = n.nextListenPort
		n.nextListenPort++
	}

	l := &listener{
		n:        n,
		addr:     &addr{network: network, peer: peer, port: port},
		incoming: make(chan *conn, n.listenerBacklog),
		closedCh: make(chan struct{}),
	}
	n.listeners[peer] = l
	return l, nil
}

// deregister removes a listener's address from the topology, if still
// present. Called from listener.Close; safe to call more than once for the
// same address.
func (n *Network) deregister(a *addr) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.listeners, a.peer)
}

// Dial creates a simulated connection from the calling peer to addr within
// n. It is equivalent to DialContext(context.Background(), network, addr),
// and has exactly the shape of net.Dial so it can be used as a drop-in
// wherever calling code accepts a func(network, addr string) (net.Conn,
// error).
func (n *Network) Dial(network, addr string) (net.Conn, error) {
	return n.DialContext(context.Background(), network, addr)
}

// DialerFor returns a dial function that names itself as name, so the
// connections it creates are targetable by Network.Partition and
// Network.Heal.
//
// It exists because Dial cannot be. A dialer's identity travels on a
// context.Context (see WithPeerName), and Dial's net.Dial-shaped signature
// has no context parameter — so a Dial call could never carry a peer name,
// was never partition-targetable, and never blocked on a partition. That put
// partition, a headline feature, out of reach of the drop-in entry point the
// library's no-rewrite adoption claim rests on: a user who wanted both had to
// abandon the drop-in and rewrite their client to take a DialContext instead
// (issue #36, M5-2 finding F2).
//
// What DialerFor returns is still exactly net.Dial's shape, which is the
// point — it goes wherever Dial went:
//
//	client := myservice.NewClient(n.DialerFor("client"))
//	n.Partition("client", "server") // now actually affects that client
//
// name is a peer identity rather than an address: a port written into it is
// stripped, so DialerFor("client") and DialerFor("client:1234") name the
// same peer, the one Partition("client") targets.
//
// One behaviour to know, because it is new for anyone reaching for this
// instead of Dial: a named dialer IS subject to the dial-time partition
// check, so dialing a peer this one is partitioned from blocks until Heal.
// A partition drops the SYN, so that is what a real dial does too — but
// there is no context here to bound the wait with. Use DialContext with
// WithPeerName and a deadline if the dial must fail rather than hang.
func (n *Network) DialerFor(name string) func(network, addr string) (net.Conn, error) {
	ctx := WithPeerName(context.Background(), name)
	return func(network, addr string) (net.Conn, error) {
		return n.DialContext(ctx, network, addr)
	}
}

// DialContext creates a simulated connection from the calling peer to addr
// within n. The dial is aborted, returning ctx.Err(), if ctx is cancelled
// before the connection is established.
//
// If the calling peer named itself via WithPeerName and that name is
// partitioned from addr, DialContext blocks until the partition is healed
// or ctx is done — a partition drops the SYN, so a real dial into a
// partitioned peer hangs the same way. A dialer that never calls
// WithPeerName is never partition-targetable (see WithPeerName), so it
// never blocks here regardless of any partition's state.
//
// A dial that never establishes does not consume a connection ordinal —
// ordinals, and therefore RNG streams (see the determinism contract on
// WithSeed), are assigned in the order dials complete, not the order they're
// attempted. This holds for every way a dial can fail, not just for a dial
// blocked on a partition and then cancelled: the accept-queue slot is
// claimed first, so a dial refused with ErrConnectionRefused or ErrBacklogFull
// returns before an ordinal is taken and leaves the sequence unshifted for
// every connection after it. What the contract does not cover is a dial that
// races a partition against cancellation; give it a context whose deadline
// is what should determine the outcome if that matters to a test.
//
// Establishment is otherwise entirely synchronous: there is no blocking
// step between validating the request and handing the new connection to the
// target listener. A full listener backlog fails immediately with
// ErrBacklogFull rather than waiting for room.
func (n *Network) DialContext(ctx context.Context, network, dialAddr string) (net.Conn, error) {
	// Checked once, up front, rather than folded into the enqueue select
	// below: with a ready default case present, Go picks pseudo-randomly
	// among ready cases, so a cancelled-but-racing dial could still enqueue
	// if ctx.Done() were just another case in that same select.
	select {
	case <-ctx.Done():
		return nil, n.dialOpError(network, dialAddr, ctx.Err())
	default:
	}

	if err := validateNetwork(network); err != nil {
		return nil, n.dialOpError(network, dialAddr, err)
	}
	peer, _, _, err := splitAddr(dialAddr)
	if err != nil {
		return nil, n.dialOpError(network, dialAddr, err)
	}

	localName, named := peerNameFromContext(ctx)
	if named {
		// A peer name is an identity, not an address, but callers reach for
		// the same strings for both. Strip a port here so
		// WithPeerName(ctx, "client:1234") targets the same peer
		// Partition("client") does.
		localName = peerName(localName)
	}
	if named && localName != "" {
		if err := n.waitUnpartitioned(ctx, newPairKey(localName, peer)); err != nil {
			return nil, n.dialOpError(network, dialAddr, err)
		}
	}

	n.mu.Lock()
	l, ok := n.listeners[peer]
	// Unlocked here, and deliberately not deferred: l.reserve below takes
	// l.mu, while listener.Close takes l.mu and then n.mu through
	// n.deregister. Holding n.mu across a call that acquires l.mu is the one
	// thing that would close that into a lock cycle. n.mu is taken again
	// below, on its own, for the ordinal.
	n.mu.Unlock()
	if !ok {
		return nil, n.dialOpError(network, dialAddr, ErrConnectionRefused)
	}

	// Claim the accept-queue slot before taking an ordinal, not after. Both
	// remaining ways to fail — a full backlog, and a listener closed since
	// the lookup — are decided here, so past this point establishment cannot
	// fail and the ordinal below is only ever spent on a connection that
	// establishes. That is what makes the determinism contract's "a dial
	// that never establishes never burns one" true of every path rather than
	// just the lookup (M6-1).
	if err := l.reserve(); err != nil {
		return nil, n.dialOpError(network, dialAddr, err)
	}

	n.mu.Lock()
	ordinal := n.nextOrdinal
	n.nextOrdinal++
	n.mu.Unlock()

	if !named || localName == "" {
		localName = ephemeralPeerName(ordinal)
	}

	// The remote address takes the listener's port rather than re-deriving
	// one: the two ends must agree on what the server's address is, and the
	// listener is the only thing that knows which port it was given.
	local := &addr{network: network, peer: localName, port: ephemeralPort(ordinal)}
	remote := &addr{network: network, peer: peer, port: l.addr.port}

	client, server := newConnPairWithSeed(local, remote, ordinal, network, n.pipeBound, n.seed)

	// A single composed evaluator per direction (M2-5) — the only place
	// fault policy is evaluated per unit, in the fixed order documented on
	// installFaultPolicy: partition, then loss, then bandwidth, then latency.
	// No configuration is copied in here. The evaluator reads it from n on
	// every unit, which is what lets SetLatency/SetPacketLoss reach this
	// connection after it is established (M7-4).
	fp := faultPolicy{
		network: n,
		pair:    newPairKey(localName, peer),
	}
	installFaultPolicy(client.writePipe, fp)
	installFaultPolicy(server.writePipe, fp)

	// Registered for Network.Reset (M7-7) under the same pair the fault
	// policy above uses, so a reset targets exactly the connections a
	// partition targeting the same names would. nw/pair let Close
	// deregister without n needing to track conn lifetime itself.
	client.nw, client.pair = n, fp.pair
	server.nw, server.pair = n, fp.pair
	n.registerReset(fp.pair, client, server)

	l.fill(server)
	return client, nil
}

// faultConfig returns the Network's current mutable fault settings. Called
// once per delivered unit by the evaluator in faults.go, so it copies the
// whole value out under one read lock rather than exposing the fields.
func (n *Network) faultConfig() faultConfig {
	n.faultMu.RLock()
	defer n.faultMu.RUnlock()
	return n.faults
}

// SetLatency changes the latency applied to every connection in n, including
// connections already established — the same live semantics Partition and
// Heal have. It is what makes "healthy, then degraded, then healthy"
// expressible without building a second Network, which would mean new
// connections and a reset of every ordinal.
//
// The arguments mean exactly what WithLatency's do, and are validated the
// same way: min and max must be non-negative and min must not exceed max, or
// the call panics naming the offending value. SetLatency(0, 0) is an
// explicit fixed-zero delay rather than "off" — it still draws, per the draw
// discipline below.
//
// Determinism: a setter is an ordered Network call, joining Dial, Listen,
// Partition and Heal in the guarantee on WithSeed. What the contract does
// not fix is a setter's order against in-flight I/O on another goroutine —
// if one goroutine writes in a loop while another calls SetLatency, which
// unit first sees the new value is the scheduler's choice, not the seed's.
// Sequence the calls a test depends on: write, then set, then write.
//
// Draw discipline is unchanged. Latency still draws on every unit past the
// partition gate, so changing the range changes the value a draw produces,
// never whether the draw happens — a stream advances one value per unit
// regardless of what any setter did.
func (n *Network) SetLatency(min, max time.Duration) {
	validateLatencyRange(min, max)

	n.faultMu.Lock()
	defer n.faultMu.Unlock()
	n.faults.latencyEnabled = true
	n.faults.latencyMin = min
	n.faults.latencyMax = max
}

// SetPacketLoss changes the packet-loss rate applied to every connection in
// n, including connections already established — the same live semantics
// Partition and Heal have.
//
// rate means exactly what WithPacketLoss's does and is validated the same
// way: outside [0.0, 1.0], including NaN and ±Inf, panics naming the
// offending value. SetPacketLoss(0.0) is an explicit always-deliver policy
// rather than "off"; it still draws.
//
// The same determinism note as SetLatency applies: the setter is an ordered
// Network call, its order against concurrent in-flight I/O is not fixed by
// the contract, and the draw discipline does not change.
func (n *Network) SetPacketLoss(rate float64) {
	validateLossRate(rate)

	n.faultMu.Lock()
	defer n.faultMu.Unlock()
	n.faults.lossEnabled = true
	n.faults.lossRate = rate
}

// waitUnpartitioned blocks until k is not partitioned or ctx is done,
// re-checking after every partition-state change (a single wakeup doesn't
// necessarily mean k itself was healed).
func (n *Network) waitUnpartitioned(ctx context.Context, k pairKey) error {
	for {
		partitioned, ch := n.checkPartition(k)
		if !partitioned {
			return nil
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// dialOpError and listenOpError wrap every error leaving DialContext and
// Listen respectively, so the shape a caller sees is the same one real
// net.Dial and net.Listen produce (M6-2). Wrapping is uniform rather than
// per-site: the previous split — refusals wrapped, bad networks and
// address-in-use bare — followed no rule anyone could state, and code under
// test that type-asserts to *net.OpError or calls Timeout()/Temporary()
// behaved differently against netchaos than against the standard library.
//
// Every sentinel stays matchable with errors.Is, since OpError unwraps. Only
// direct == comparison against a sentinel stops working, which is why this
// was a decision rather than a cleanup; errors.go documents the sentinels as
// errors.Is targets.
func (n *Network) dialOpError(network, dialAddr string, err error) error {
	return &net.OpError{Op: "dial", Net: network, Addr: errAddr(network, dialAddr), Err: err}
}

func (n *Network) listenOpError(network, laddr string, err error) error {
	return &net.OpError{Op: "listen", Net: network, Addr: errAddr(network, laddr), Err: err}
}
