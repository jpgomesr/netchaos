package netchaos

// M7-7 (#53, candidate 2): mid-stream connection reset. Decided by the
// maintainer, before implementation, as an imperative Network method
// mirroring Partition/Heal's shape rather than a per-unit drawn Option
// (docs/tasks/m7-v0.2.0-implementation.md#m7-7). That decision is why this
// file has no faultKind, no derived stream, and no entry in
// installFaultPolicy's evaluator: a reset is not a per-unit fault decision
// at all, it is a one-shot action against whichever connections currently
// exist for a named peer pair, the same way Close terminates one conn.

// Reset abruptly terminates every currently-established connection between
// the named peers: both ends' subsequent Read and Write calls, and any
// already in-flight on another goroutine, fail with an error satisfying
// errors.Is(err, syscall.ECONNRESET) wrapped in a *net.OpError (matching
// the uniform error shape M6-2 established). A reset connection stays
// reset -- there is no way to "un-reset" it, unlike a partition, which
// Heal reverses.
//
// This is the gap Partition cannot fill: Partition is a deliberately
// silent black hole (writes accepted and discarded, reads blocking to
// their deadline), never an ECONNRESET. Testing "the peer dropped the
// connection abruptly" previously meant holding the server-side net.Conn
// and calling Close yourself -- something a test of client-side reconnect
// logic should not have to do.
//
// Unlike Partition, Reset does not gate Dial: it has no effect on
// establishment, and no effect on a connection dialed after this call
// returns. It resets what exists at the moment it is called and nothing
// else -- a real RST invalidates existing TCP state, it does not prevent a
// fresh connection.
//
// A no-op if no connection currently exists between the named peers --
// there being nothing established to reset is not an error, the same
// no-op convention Partition and Heal already use for an unrecognized or
// not-yet-connected pair. peerA and peerB are resolved exactly as
// Partition/Heal resolve them (peerName strips a port, per M7-1), so the
// same naming caveat applies: an unnamed dialer's synthesized ephemeral-N
// identity is not one a caller can predict, so it is not practically
// targetable here either.
//
// Reset takes no random draws and cannot perturb any connection's
// loss/latency/duplication/corruption sequence -- it is evaluated nowhere
// near installFaultPolicy's per-unit evaluator.
func (n *Network) Reset(peerA, peerB string) {
	k := newPairKey(peerName(peerA), peerName(peerB))

	n.resetMu.Lock()
	targets := n.resetTargets[k]
	// Removed rather than cleared in place: a connection dialed for this
	// same pair after this call registers itself fresh (registerReset),
	// so it must not inherit a reset that predates it.
	delete(n.resetTargets, k)
	n.resetMu.Unlock()

	for _, c := range targets {
		c.triggerReset()
	}
}

// registerReset records conns as targetable by a future Network.Reset(a, b)
// call for pair, called once per established connection from DialContext.
// Deregistered by conn.Close via deregisterReset, so the registry does not
// grow unboundedly over a test that dials and closes many short-lived
// connections.
func (n *Network) registerReset(pair pairKey, conns ...*conn) {
	n.resetMu.Lock()
	defer n.resetMu.Unlock()
	n.resetTargets[pair] = append(n.resetTargets[pair], conns...)
}

// deregisterReset removes c from pair's reset registry, if still present.
// Called from conn.Close; safe to call more than once or for a c already
// removed (e.g. by an intervening Reset), which is a no-op.
func (n *Network) deregisterReset(pair pairKey, c *conn) {
	n.resetMu.Lock()
	defer n.resetMu.Unlock()
	targets := n.resetTargets[pair]
	for i, t := range targets {
		if t == c {
			n.resetTargets[pair] = append(targets[:i], targets[i+1:]...)
			return
		}
	}
}
