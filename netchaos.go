package netchaos

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

// Network is a simulated network topology: a set of named peers that can
// Dial one another and Listen for connections, subject to a fault policy
// (added in M2 — latency, packet loss, and partition are not yet
// implemented).
//
// Network intentionally has no Close method in M1: every internal
// goroutine netchaos spawns (only deadline.go's time.AfterFunc callbacks)
// terminates on the close of the conn that owns it, so there is nothing for
// a Network-wide Close to reap yet (see leak_test.go). Revisit this once a
// milestone introduces a resource not scoped to a single conn — M2-2's
// latency delivery timers are the likely trigger.
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

	latencyEnabled         bool
	latencyMin, latencyMax time.Duration

	lossEnabled bool
	lossRate    float64

	partMu     sync.RWMutex
	partitions map[pairKey]struct{}
	partNotify chan struct{} // closed and replaced on every Partition/Heal

	mu          sync.Mutex
	listeners   map[string]*listener // key: peerName(addr)
	nextOrdinal uint64
}

// NewNetwork returns a new, empty Network configured by opts. Options are
// applied in argument order; a later option of the same kind overrides an
// earlier one.
//
// If WithSeed is not given, NewNetwork uses a fixed, documented default
// seed (defaultSeed) rather than a random one: netchaos's whole premise is
// that a test run is reproducible from its seed, so the common case of "just
// run the test" should be reproducible by default too. Because the default
// is a fixed constant, there is nothing to "recover" from a failing run —
// unlike a random default, which would need an accessor to report the seed
// it picked.
func NewNetwork(opts ...Option) *Network {
	cfg := networkConfig{seed: defaultSeed}
	for _, opt := range opts {
		opt(&cfg)
	}
	n := &Network{
		seed:           cfg.seed,
		latencyEnabled: cfg.latencyEnabled,
		latencyMin:     cfg.latencyMin,
		latencyMax:     cfg.latencyMax,
		lossEnabled:    cfg.lossEnabled,
		lossRate:       cfg.lossRate,
		partitions:     make(map[pairKey]struct{}, len(cfg.staticPartitions)),
		partNotify:     make(chan struct{}),
		listeners:      make(map[string]*listener),
	}
	for _, k := range cfg.staticPartitions {
		n.partitions[k] = struct{}{}
	}
	return n
}

// Listen registers a simulated listener at addr within n. Connections
// dialed to addr from elsewhere in n are delivered to this listener's
// Accept. network must be "tcp", "tcp4", or "tcp6" (v1 is TCP-shaped only;
// "udp" is rejected). Listening on an address already registered by another
// open listener returns an error satisfying errors.Is(err, ErrAddressInUse).
func (n *Network) Listen(network, laddr string) (net.Listener, error) {
	if err := validateNetwork(network); err != nil {
		return nil, err
	}
	peer := peerName(laddr)

	n.mu.Lock()
	defer n.mu.Unlock()

	if _, ok := n.listeners[peer]; ok {
		return nil, ErrAddressInUse
	}

	l := &listener{
		n:        n,
		addr:     &addr{network: network, peer: peer},
		incoming: make(chan *conn, listenerBacklog),
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
// Establishment is otherwise entirely synchronous — there is no blocking
// step between validating the request and enqueuing the new connection on
// the target listener (enqueue itself never blocks; a full backlog fails
// immediately with ErrBacklogFull rather than waiting for room) — so
// cancellation is checked once, up front, rather than raced against
// enqueuing in the same select. Folding ctx.Done() into that select instead
// would be a real bug: with a ready default case present, Go picks
// pseudo-randomly among ready cases, so a cancelled-but-racing dial could
// still enqueue.
//
// The partition wait happens before the connection ordinal is assigned, so
// a dial that never establishes (blocked, then cancelled) does not burn an
// ordinal — ordinal order tracks the order dials complete, not the order
// they're attempted.
func (n *Network) DialContext(ctx context.Context, network, dialAddr string) (net.Conn, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if err := validateNetwork(network); err != nil {
		return nil, err
	}
	peer := peerName(dialAddr)

	localName, named := peerNameFromContext(ctx)
	if named && localName != "" {
		if err := n.waitUnpartitioned(ctx, newPairKey(localName, peer)); err != nil {
			return nil, err
		}
	}

	n.mu.Lock()
	l, ok := n.listeners[peer]
	if !ok {
		n.mu.Unlock()
		return nil, n.dialOpError(network, dialAddr, ErrConnectionRefused)
	}
	ordinal := n.nextOrdinal
	n.nextOrdinal++
	n.mu.Unlock()

	if !named || localName == "" {
		localName = fmt.Sprintf("ephemeral:%d", ordinal)
	}

	client, server := newConnPairWithSeed(&addr{network: network, peer: localName}, &addr{network: network, peer: peer}, ordinal, network, defaultPipeBound, n.seed)

	if n.latencyEnabled {
		installLatency(client.writePipe, n.latencyMin, n.latencyMax)
		installLatency(server.writePipe, n.latencyMin, n.latencyMax)
	}
	if n.lossEnabled {
		installLoss(client.writePipe, n.lossRate)
		installLoss(server.writePipe, n.lossRate)
	}
	// Partition wraps whatever was installed above so it always gates
	// delivery first, regardless of what else is configured, and is always
	// installed (unlike latency/loss) since Partition/Heal can be called at
	// any point during a test, not just declared via WithPartition.
	pair := newPairKey(localName, peer)
	installPartition(client.writePipe, n, pair)
	installPartition(server.writePipe, n, pair)

	if err := l.enqueue(server); err != nil {
		return nil, n.dialOpError(network, dialAddr, err)
	}
	return client, nil
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

func (n *Network) dialOpError(network, dialAddr string, err error) error {
	return &net.OpError{Op: "dial", Net: network, Addr: &addr{network: network, peer: dialAddr}, Err: err}
}
