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

// enqueue offers c to this listener's accept queue on behalf of Dial. It
// never blocks: a closed listener refuses immediately, and a full backlog
// fails with ErrBacklogFull rather than waiting for room.
func (l *listener) enqueue(c *conn) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return ErrConnectionRefused
	}
	select {
	case l.incoming <- c:
		return nil
	default:
		return ErrBacklogFull
	}
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
