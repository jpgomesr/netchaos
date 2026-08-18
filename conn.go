package netchaos

import (
	"net"
	"os"
	"sync"
	"time"
)

// connSide records which end of a connection pair a conn is. M2-1 derives
// each direction's RNG stream partly from this; M1 only plumbs the field.
type connSide int

const (
	sideDialer connSide = iota
	sideAcceptor
)

// conn is netchaos's simulated net.Conn: two crossed pipes (see pipe.go),
// one per direction, wrapped with addresses, a closed flag, and (from M1-3
// onward) deadlines.
//
// Close semantics: closing one end of a connection tears down both
// directions for that end — there is no half-close (no CloseRead/CloseWrite,
// matching net.Pipe). Concretely, Close (1) marks this end closed, which
// makes this end's own subsequent Read/Write fail immediately with an error
// satisfying errors.Is(err, net.ErrClosed); (2) closes the pipe this end
// writes to (the peer's read pipe), so the peer's already-buffered data
// still drains normally and its Read only returns io.EOF once that data is
// exhausted; (3) closes the pipe this end reads from (the peer's write
// pipe), so the peer's subsequent writes fail the same way this end's do.
//
// A conn is safe for concurrent use by multiple goroutines, as net.Conn
// requires.
type conn struct {
	local, remote *addr
	network       string
	ordinal       uint64   // M2-1 dependency, unused in M1
	side          connSide // M2-1 dependency, unused in M1

	readPipe, writePipe *pipe

	closeOnce sync.Once
	closed    chan struct{}

	rd, wd *deadline
}

var _ net.Conn = (*conn)(nil)

// newConnPair builds the two ends of a full-duplex simulated connection:
// clientAddr's writes are readable by serverAddr's end and vice versa. Both
// ends share the same ordinal (M0-4's determinism contract assigns one
// ordinal per connection, not per end) with opposite sides.
func newConnPair(clientAddr, serverAddr *addr, ordinal uint64, network string) (client, server *conn) {
	return newConnPairWithBound(clientAddr, serverAddr, ordinal, network, defaultPipeBound)
}

// newConnPairWithBound is newConnPair with an explicit pipe buffer bound,
// used directly by tests that need to force back-pressure at a size smaller
// than defaultPipeBound. Fault streams are derived from defaultSeed, since
// callers of this helper are exercising transport behaviour, not fault
// determinism.
func newConnPairWithBound(clientAddr, serverAddr *addr, ordinal uint64, network string, bound int) (client, server *conn) {
	return newConnPairWithSeed(clientAddr, serverAddr, ordinal, network, bound, defaultSeed)
}

// newConnPairWithSeed is newConnPairWithBound with an explicit master seed,
// used by Network.DialContext to derive each pipe's fault streams (M0-4)
// from the Network's own configured seed instead of the default.
func newConnPairWithSeed(clientAddr, serverAddr *addr, ordinal uint64, network string, bound int, seed int64) (client, server *conn) {
	clientToServer := newPipe(bound)
	serverToClient := newPipe(bound)

	clientToServer.loss = deriveStream(seed, ordinal, sideDialer, kindLoss)
	clientToServer.latency = deriveStream(seed, ordinal, sideDialer, kindLatency)
	clientToServer.trace = &traceRecorder{}

	serverToClient.loss = deriveStream(seed, ordinal, sideAcceptor, kindLoss)
	serverToClient.latency = deriveStream(seed, ordinal, sideAcceptor, kindLatency)
	serverToClient.trace = &traceRecorder{}

	client = &conn{
		local:     clientAddr,
		remote:    serverAddr,
		network:   network,
		ordinal:   ordinal,
		side:      sideDialer,
		writePipe: clientToServer,
		readPipe:  serverToClient,
		closed:    make(chan struct{}),
		rd:        newDeadline(),
		wd:        newDeadline(),
	}
	server = &conn{
		local:     serverAddr,
		remote:    clientAddr,
		network:   network,
		ordinal:   ordinal,
		side:      sideAcceptor,
		writePipe: serverToClient,
		readPipe:  clientToServer,
		closed:    make(chan struct{}),
		rd:        newDeadline(),
		wd:        newDeadline(),
	}
	return client, server
}

func (c *conn) opError(op string, err error) error {
	return &net.OpError{Op: op, Net: c.network, Source: c.local, Addr: c.remote, Err: err}
}

// Read implements net.Conn. See the package-level pipe documentation for
// coalescing/partial-read behaviour.
func (c *conn) Read(b []byte) (int, error) {
	for {
		select {
		case <-c.closed:
			return 0, c.opError("read", net.ErrClosed)
		default:
		}

		dch := c.rd.channel() // snapshot before checking expired/tryRead, so no wakeup is missed
		n, ch, err := c.readPipe.tryRead(b)
		if ch == nil {
			return n, err // err is nil or io.EOF, returned bare per net.Pipe/stdlib convention
		}
		if c.rd.expired() {
			return 0, c.opError("read", os.ErrDeadlineExceeded)
		}

		select {
		case <-ch:
		case <-dch:
		case <-c.closed:
		}
	}
}

// Write implements net.Conn. It never reports a short write without a
// non-nil error, per io.Writer's contract.
func (c *conn) Write(b []byte) (int, error) {
	// io.Writer callers may reuse b once Write returns; the pipe retains
	// queued data beyond this call, so it needs its own copy.
	data := append([]byte(nil), b...)

	for {
		select {
		case <-c.closed:
			return 0, c.opError("write", net.ErrClosed)
		default:
		}

		dch := c.wd.channel()
		n, ch, err := c.writePipe.tryWrite(data)
		if ch == nil {
			if err != nil {
				// The only error tryWrite reports is the pipe having been
				// closed (by this end's own Close, or because it is the
				// peer's read pipe and the peer closed). Either way, from
				// this net.Conn's perspective that is "closed connection."
				return 0, c.opError("write", net.ErrClosed)
			}
			return n, nil
		}
		if c.wd.expired() {
			return 0, c.opError("write", os.ErrDeadlineExceeded)
		}

		select {
		case <-ch:
		case <-dch:
		case <-c.closed:
		}
	}
}

// Close implements net.Conn. It is idempotent and always returns nil.
func (c *conn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.writePipe.close()
		_ = c.readPipe.close()
		c.rd.stop()
		c.wd.stop()
	})
	return nil
}

func (c *conn) LocalAddr() net.Addr  { return c.local }
func (c *conn) RemoteAddr() net.Addr { return c.remote }

// SetDeadline sets both the read and write deadlines. SetReadDeadline and
// SetWriteDeadline set only their own direction. A zero time.Time clears a
// previously set deadline. A deadline already in the past cancels an
// in-flight Read/Write on another goroutine, not just future calls. After a
// timeout the conn remains usable: a fresh deadline plus a new Read/Write
// succeeds normally. Deadlines are implemented with standard time.Timer, so
// testing/synctest virtualizes them.
func (c *conn) SetDeadline(t time.Time) error {
	c.rd.set(t)
	c.wd.set(t)
	return nil
}

func (c *conn) SetReadDeadline(t time.Time) error {
	c.rd.set(t)
	return nil
}

func (c *conn) SetWriteDeadline(t time.Time) error {
	c.wd.set(t)
	return nil
}
