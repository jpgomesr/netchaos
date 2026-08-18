package netchaos

import (
	"errors"
	"io"
	"net"
	"sync"
	"testing"
)

func newTestConnPair() (client, server *conn) {
	return newConnPair(&addr{"tcp", "client"}, &addr{"tcp", "server"}, 0, "tcp")
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
	if _, err := server.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("server write after peer close = %v, want errors.Is(net.ErrClosed)", err)
	}
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

func TestConnConcurrentUse(t *testing.T) {
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
