package netchaos

import (
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func newTestConnPair() (client, server *conn) {
	return newConnPair(&addr{network: "tcp", peer: "client"}, &addr{network: "tcp", peer: "server"}, 0, "tcp")
}

func TestConnFullDuplex(t *testing.T) {
	client, server := newTestConnPair()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	var wg sync.WaitGroup
	wg.Add(2)

	var clientGotErr, serverGotErr error
	var clientGot, serverGot []byte

	go func() {
		defer wg.Done()
		if _, err := client.Write([]byte("client->server")); err != nil {
			clientGotErr = err
			return
		}
		buf := make([]byte, len("server->client"))
		n, err := client.Read(buf)
		clientGot, clientGotErr = buf[:n], err
	}()

	go func() {
		defer wg.Done()
		if _, err := server.Write([]byte("server->client")); err != nil {
			serverGotErr = err
			return
		}
		buf := make([]byte, len("client->server"))
		n, err := server.Read(buf)
		serverGot, serverGotErr = buf[:n], err
	}()

	wg.Wait()

	if clientGotErr != nil {
		t.Fatalf("client side: %v", clientGotErr)
	}
	if serverGotErr != nil {
		t.Fatalf("server side: %v", serverGotErr)
	}
	if string(clientGot) != "server->client" {
		t.Fatalf("client read %q, want %q", clientGot, "server->client")
	}
	if string(serverGot) != "client->server" {
		t.Fatalf("server read %q, want %q", serverGot, "client->server")
	}
}

func TestConnAddrsMirrored(t *testing.T) {
	client, server := newTestConnPair()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	if client.LocalAddr().String() != server.RemoteAddr().String() {
		t.Fatalf("client.LocalAddr() = %v, server.RemoteAddr() = %v", client.LocalAddr(), server.RemoteAddr())
	}
	if server.LocalAddr().String() != client.RemoteAddr().String() {
		t.Fatalf("server.LocalAddr() = %v, client.RemoteAddr() = %v", server.LocalAddr(), client.RemoteAddr())
	}
}

func TestConnCloseEOFsPeer(t *testing.T) {
	client, server := newTestConnPair()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	if _, err := client.Write([]byte("buffered")); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, len("buffered"))
	n, err := server.Read(buf)
	if err != nil || string(buf[:n]) != "buffered" {
		t.Fatalf("server drain read = (%d, %q, %v), want buffered data with no error", n, buf[:n], err)
	}

	n, err = server.Read(buf)
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("server read after drain = (%d, %v), want (0, io.EOF)", n, err)
	}

	// The peer's writes must also fail once this end is closed.
	if _, err := server.Write([]byte("x")); !errors.Is(err, errBrokenPipe) || errors.Is(err, net.ErrClosed) {
		t.Fatalf("server write after peer close = %v, want broken pipe without net.ErrClosed", err)
	}
}

func TestRemoteCloseWriteErrorAndLocalPrecedence(t *testing.T) {
	client, server := newTestConnPair()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("x")); !errors.Is(err, errBrokenPipe) || errors.Is(err, net.ErrClosed) {
		t.Fatalf("remote close Write = %v, want broken pipe only", err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("x")); !errors.Is(err, net.ErrClosed) || errors.Is(err, errBrokenPipe) {
		t.Fatalf("both ends closed Write = %v, want local net.ErrClosed only", err)
	}
}

func TestBlockedWriteWakesWithRemoteCloseError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, server := newConnPairWithBound(&addr{network: "tcp", peer: "client"}, &addr{network: "tcp", peer: "server"}, 0, "tcp", 1)
		defer func() { _ = client.Close(); _ = server.Close() }()
		if _, err := client.Write([]byte("a")); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { _, err := client.Write([]byte("b")); result <- err }()
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatalf("Write returned before peer Close: %v", err)
		default:
		}
		if err := server.Close(); err != nil {
			t.Fatal(err)
		}
		if err := <-result; !errors.Is(err, errBrokenPipe) || errors.Is(err, net.ErrClosed) {
			t.Fatalf("blocked Write after remote Close = %v, want broken pipe only", err)
		}
	})
}

func TestConnWriteAfterClose(t *testing.T) {
	client, server := newTestConnPair()
	defer func() { _ = server.Close() }()

	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Write after Close = %v, want errors.Is(net.ErrClosed)", err)
	}
	if _, err := client.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Read after Close = %v, want errors.Is(net.ErrClosed)", err)
	}
}

// TestConnSetDeadlineAfterClose pins issue #82: a real net.Conn's
// SetDeadline/SetReadDeadline/SetWriteDeadline return a non-nil error
// satisfying errors.Is(err, net.ErrClosed) once Close has run, matching the
// error every other post-Close operation on a conn already returns.
func TestConnSetDeadlineAfterClose(t *testing.T) {
	client, server := newTestConnPair()
	defer func() { _ = server.Close() }()

	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	if err := client.SetDeadline(deadline); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("SetDeadline after Close = %v, want errors.Is(net.ErrClosed)", err)
	}
	if err := client.SetReadDeadline(deadline); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("SetReadDeadline after Close = %v, want errors.Is(net.ErrClosed)", err)
	}
	if err := client.SetWriteDeadline(deadline); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("SetWriteDeadline after Close = %v, want errors.Is(net.ErrClosed)", err)
	}

	var opErr *net.OpError
	if err := client.SetDeadline(deadline); !errors.As(err, &opErr) || opErr.Op != "set" {
		t.Fatalf("SetDeadline after Close = %v, want a *net.OpError with Op %q", err, "set")
	}
}

func TestConnConcurrentUse(_ *testing.T) {
	client, server := newTestConnPair()

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if _, err := client.Write([]byte("x")); err != nil {
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		buf := make([]byte, 16)
		for {
			if _, err := server.Read(buf); err != nil {
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		_ = client.Close()
	}()

	wg.Wait()
	_ = server.Close()
}
