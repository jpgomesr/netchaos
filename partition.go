package netchaos

// pairKey identifies an unordered pair of peers: Partition("a", "b") and
// Partition("b", "a") name the same pair, so lookups and storage always go
// through newPairKey rather than comparing raw (peerA, peerB) tuples.
type pairKey struct{ a, b string }

// newPairKey builds the pairKey for x and y, independent of argument order.
func newPairKey(x, y string) pairKey {
	if x <= y {
		return pairKey{x, y}
	}
	return pairKey{y, x}
}

// WithPartition marks the named peers as partitioned from Network
// construction onward. Static for the Network's lifetime; see
// Network.Partition / Network.Heal for dynamic control during a test.
func WithPartition(peerA, peerB string) Option {
	return func(c *networkConfig) {
		c.staticPartitions = append(c.staticPartitions, newPairKey(peerName(peerA), peerName(peerB)))
	}
}

// Partition drops all subsequent traffic between the named peers until Heal
// is called for the same pair. A no-op if the pair is already partitioned,
// and a no-op (not an error) if either peer has never Dialed or Listened —
// partitioning before either side has connected is legitimate test setup
// (M0-5).
//
// Partition affects both connection establishment (a Dial naming one of
// these peers as itself, via WithPeerName, blocks until Heal or its
// context expires) and already-established connections between the pair
// (writes are accepted and silently discarded; reads block until their
// deadline).
func (n *Network) Partition(peerA, peerB string) {
	k := newPairKey(peerName(peerA), peerName(peerB))

	n.partMu.Lock()
	defer n.partMu.Unlock()
	if _, ok := n.partitions[k]; ok {
		return
	}
	n.partitions[k] = struct{}{}
	n.bumpPartNotifyLocked()
}

// Heal removes a previously established partition between the named peers,
// restoring traffic on any already-established connection between them
// without requiring a re-dial. A no-op if the pair is not currently
// partitioned (M0-5) — safe to call from a defer or test cleanup without
// tracking partition state separately.
func (n *Network) Heal(peerA, peerB string) {
	k := newPairKey(peerName(peerA), peerName(peerB))

	n.partMu.Lock()
	defer n.partMu.Unlock()
	if _, ok := n.partitions[k]; !ok {
		return
	}
	delete(n.partitions, k)
	n.bumpPartNotifyLocked()
}

// bumpPartNotifyLocked wakes every goroutine currently waiting on a
// partition-state change (a blocked Dial, or a pipe's isPartitioned check
// via checkPartition). Must be called with n.partMu held. Mirrors
// pipe.broadcastLocked and deadline.bumpLocked's "close and replace"
// generation-channel idiom.
func (n *Network) bumpPartNotifyLocked() {
	close(n.partNotify)
	n.partNotify = make(chan struct{})
}

// isPartitioned reports whether k is currently partitioned.
func (n *Network) isPartitioned(k pairKey) bool {
	n.partMu.RLock()
	defer n.partMu.RUnlock()
	_, ok := n.partitions[k]
	return ok
}

// checkPartition reports whether k is currently partitioned and the notify
// channel to wait on if so, snapshotted under the same lock as the check —
// mirroring pipe.tryRead — so no Heal between the check and the wait can
// be missed.
func (n *Network) checkPartition(k pairKey) (partitioned bool, ch <-chan struct{}) {
	n.partMu.RLock()
	defer n.partMu.RUnlock()
	_, ok := n.partitions[k]
	return ok, n.partNotify
}

// installPartition wraps p's current deliver function so that a unit bound
// for a partitioned pair is discarded before whatever fault policy (loss,
// latency) is already installed ever sees it, and consumes no random draw
// in doing so. It always wraps, even when nothing else is configured,
// since Partition/Heal can be called at any time during a test, not just
// declared up front via WithPartition.
func installPartition(p *pipe, n *Network, key pairKey) {
	inner := p.deliver
	p.deliver = func(p *pipe, data []byte) {
		if n.isPartitioned(key) {
			// Admission already accounted these bytes; since a partitioned
			// unit never reaches readable (nor any fault beneath this
			// wrapper), that accounting must be undone here, exactly as a
			// packet-loss drop does (loss.go).
			p.bufBytes -= len(data)
			if p.trace != nil {
				p.trace.record(faultEvent{partitioned: true})
			}
			p.broadcastLocked()
			return
		}
		inner(p, data)
	}
}
