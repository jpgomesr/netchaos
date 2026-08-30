package netchaos

import (
	"net"
	"sync"
)

// listenerBacklog is the accept-queue bound, analogous to a conventional
// listen(2) backlog default. A Dial that finds the queue full fails with
// ErrBacklogFull rather than blocking, so establishment stays a single
// non-blocking step end to end (see Network.Dial in netchaos.go).
const listenerBacklog = 128

// listener is netchaos's simulated net.Listener: an address registration in
// a Network's topology plus a bounded queue of pending inbound connections
// that Accept pulls from.
//
// A listener is safe for concurrent use by multiple goroutines.
type listener struct {
	n        *Network
	addr     *addr
	incoming chan *conn

	mu       sync.Mutex
	closed   bool
	closedCh chan struct{}
	reserved int // slots claimed by an in-flight Dial but not yet filled
}

var _ net.Listener = (*listener)(nil)

// Accept implements net.Listener.
func (l *listener) Accept() (net.Conn, error) {
	select {
	case c := <-l.incoming:
		return c, nil
	case <-l.closedCh:
		return nil, l.opError("accept", net.ErrClosed)
	}
}

// reserve claims one accept-queue slot for an in-flight Dial that does not
// yet have a *conn to put in it. It never blocks: a closed listener refuses
// immediately, and a full backlog fails with ErrBacklogFull rather than
// waiting for room.
//
// Splitting the claim from the hand-off is what lets Dial assign a
// connection ordinal only to a connection that actually establishes: every
// way establishment can still fail is checked here, so a successful reserve
// is the point the dial commits (M6-1, and the determinism contract's "a
// dial that never establishes never burns one" in docs/04-api-design.md).
//
// A successful reserve must be matched by exactly one fill.
func (l *listener) reserve() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return ErrConnectionRefused
	}
	if len(l.incoming)+l.reserved >= cap(l.incoming) {
		return ErrBacklogFull
	}
	l.reserved++
	return nil
}

// fill hands c to a slot claimed by an earlier reserve. It cannot block and
// cannot fail: reserve already counted this conn against the backlog, so the
// send has room by construction.
//
// A listener closed in the window between the reserve and this call closes c
// instead of queueing it — the same thing Close does to connections already
// sitting in the queue that nobody accepted. The dialer keeps a live conn
// whose peer is already closed, so its next Read/Write reports a closed
// connection. That is a state a dialer could always reach (Close races an
// accepted-but-unread conn the same way); reserve widens the window rather
// than introducing a new outcome, and it is the price of never handing an
// ordinal to a dial that then fails.
func (l *listener) fill(c *conn) {
	l.mu.Lock()
	l.reserved--
	if l.closed {
		l.mu.Unlock()
		_ = c.Close()
		return
	}
	l.incoming <- c
	l.mu.Unlock()
}

// enqueue reserves and fills in a single step. Dial deliberately does not use
// it — it needs the two halves apart, so that the ordinal it assigns between
// them is only ever given to a connection that establishes — but the combined
// form is what tests exercising the capacity bound want.
func (l *listener) enqueue(c *conn) error {
	if err := l.reserve(); err != nil {
		return err
	}
	l.fill(c)
	return nil
}

// Close implements net.Listener. It is idempotent, deregisters the address
// (making it available for a future Listen), unblocks any in-flight Accept
// with an error satisfying errors.Is(err, net.ErrClosed), and closes every
// connection still sitting in the accept queue that nobody Accepted.
func (l *listener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	close(l.closedCh)
	l.n.deregister(l.addr)
	l.mu.Unlock()

	for {
		select {
		case c := <-l.incoming:
			_ = c.Close()
		default:
			return nil
		}
	}
}

// Addr implements net.Listener.
func (l *listener) Addr() net.Addr { return l.addr }

func (l *listener) opError(op string, err error) error {
	return &net.OpError{Op: op, Net: l.addr.network, Addr: l.addr, Err: err}
}
