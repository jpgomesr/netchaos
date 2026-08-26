package netchaos_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jpgomesr/netchaos"
)

// acceptOne starts a background Accept on l and returns a channel that
// delivers the accepted connection. Examples run outside a testing/synctest
// bubble (Example functions have no *testing.T to hand synctest.Test), so
// this is plain goroutine synchronization rather than a bubble-aware
// pattern.
func acceptOne(l net.Listener) <-chan net.Conn {
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := l.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	return accepted
}

// Example dials a listener within a Network and exchanges one message, the
// minimal round trip every other example builds on.
func Example() {
	n := netchaos.NewNetwork()

	l, err := n.Listen("tcp", "server")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = l.Close() }()
	accepted := acceptOne(l)

	client, err := n.Dial("tcp", "server")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = client.Close() }()
	server := <-accepted
	defer func() { _ = server.Close() }()

	if _, err := client.Write([]byte("ping")); err != nil {
		fmt.Println(err)
		return
	}

	buf := make([]byte, 4)
	if _, err := io.ReadFull(server, buf); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(buf))
	// Output: ping
}

// ExampleNetwork_Dial shows the error a Dial into an address nothing is
// listening on returns: a connection-refused-shaped error, matching what a
// real closed port would produce.
func ExampleNetwork_Dial() {
	n := netchaos.NewNetwork()

	_, err := n.Dial("tcp", "nobody-listening")
	fmt.Println(errors.Is(err, netchaos.ErrConnectionRefused))
	// Output: true
}

// ExampleWithPacketLoss shows packet loss dropping some writes silently: the
// writer never sees an error, and the reader only ever observes the
// survivors, concatenated with no gap marker. A fixed seed makes which
// writes survive reproducible.
func ExampleWithPacketLoss() {
	n := netchaos.NewNetwork(netchaos.WithSeed(7), netchaos.WithPacketLoss(0.4))

	l, err := n.Listen("tcp", "server")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = l.Close() }()
	accepted := acceptOne(l)

	client, err := n.Dial("tcp", "server")
	if err != nil {
		fmt.Println(err)
		return
	}
	server := <-accepted
	defer func() { _ = server.Close() }()

	const writes = 10
	for i := 0; i < writes; i++ {
		if _, err := client.Write([]byte{'A' + byte(i)}); err != nil {
			fmt.Println(err)
			return
		}
	}
	_ = client.Close()

	survivors, err := io.ReadAll(server)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("%d of %d writes arrived: %q\n", len(survivors), writes, survivors)
	// Output: 5 of 10 writes arrived: "ACDGI"
}

// ExampleWithLatency shows latency delaying delivery of a write without
// dropping or reordering it. The delay here is a fraction of a millisecond
// so the example runs fast; netchaos does not require any particular
// magnitude.
func ExampleWithLatency() {
	n := netchaos.NewNetwork(netchaos.WithLatency(time.Millisecond, time.Millisecond))

	l, err := n.Listen("tcp", "server")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = l.Close() }()
	accepted := acceptOne(l)

	client, err := n.Dial("tcp", "server")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = client.Close() }()
	server := <-accepted
	defer func() { _ = server.Close() }()

	if _, err := client.Write([]byte("hello")); err != nil {
		fmt.Println(err)
		return
	}

	buf := make([]byte, 5)
	if _, err := io.ReadFull(server, buf); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(buf))
	// Output: hello
}

// ExampleNetwork_Partition shows a partitioned pair silently discarding
// writes, and traffic resuming once the pair is healed -- without needing a
// re-dial. WithPeerName gives the dialer a stable, partition-targetable
// identity; a dialer that never calls it can never be named by Partition.
func ExampleNetwork_Partition() {
	n := netchaos.NewNetwork()

	l, err := n.Listen("tcp", "server")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = l.Close() }()
	accepted := acceptOne(l)

	ctx := netchaos.WithPeerName(context.Background(), "client")
	client, err := n.DialContext(ctx, "tcp", "server")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = client.Close() }()
	server := <-accepted
	defer func() { _ = server.Close() }()

	n.Partition("client", "server")
	if _, err := client.Write([]byte("dropped")); err != nil {
		fmt.Println(err)
		return
	}

	n.Heal("client", "server")
	if _, err := client.Write([]byte("delivered")); err != nil {
		fmt.Println(err)
		return
	}

	buf := make([]byte, len("delivered"))
	if _, err := io.ReadFull(server, buf); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(buf))
	// Output: delivered
}

// ExampleWithSeed shows that the same seed reproduces the exact same fault
// sequence: running the same scenario twice with WithSeed(42) produces
// identical packet-loss outcomes both times.
func ExampleWithSeed() {
	run := func() (string, error) {
		n := netchaos.NewNetwork(netchaos.WithSeed(42), netchaos.WithPacketLoss(0.5))

		l, err := n.Listen("tcp", "server")
		if err != nil {
			return "", err
		}
		defer func() { _ = l.Close() }()
		accepted := acceptOne(l)

		client, err := n.Dial("tcp", "server")
		if err != nil {
			return "", err
		}
		server := <-accepted
		defer func() { _ = server.Close() }()

		const writes = 8
		for i := 0; i < writes; i++ {
			if _, err := client.Write([]byte{'A' + byte(i)}); err != nil {
				return "", err
			}
		}
		_ = client.Close()

		survivors, err := io.ReadAll(server)
		return string(survivors), err
	}

	first, err := run()
	if err != nil {
		fmt.Println(err)
		return
	}
	second, err := run()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(first == second)
	// Output: true
}

// TestReadmeUsageSnippet is the compiled, verified version of the README's
// usage snippet: a client retries a request until it gets a response,
// despite packet loss and latency, inside a testing/synctest bubble so the
// retries cost no real wall-clock time. Example functions have no
// *testing.T, so they cannot enter a bubble -- this lives as a Test
// function precisely so the README's synctest.Test shape stays intact and
// compiled, rather than being flattened into something bubble-free.
func TestReadmeUsageSnippet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		network := netchaos.NewNetwork(
			netchaos.WithPacketLoss(0.3),
			netchaos.WithLatency(50*time.Millisecond, 150*time.Millisecond),
			netchaos.WithSeed(42), // deterministic, reproducible failures
		)

		l, err := network.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()

		go func() {
			server, err := l.Accept()
			if err != nil {
				return
			}
			defer func() { _ = server.Close() }()
			// Echo every request it receives back to the client.
			buf := make([]byte, 64)
			for {
				n, err := server.Read(buf)
				if err != nil {
					return
				}
				if _, err := server.Write(buf[:n]); err != nil {
					return
				}
			}
		}()

		client, err := network.Dial("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = client.Close() }()

		got, err := fetchWithRetry(client, []byte("resource-id"))
		if err != nil {
			t.Fatalf("expected retry to succeed despite packet loss, got: %v", err)
		}
		if string(got) != "resource-id" {
			t.Fatalf("got %q, want %q", got, "resource-id")
		}
	})
}

// TestCircuitBreakerAcrossPartitionAndHeal adapts the M3-4 scenario suite's
// most persuasive case: a circuit breaker opens when its downstream
// partitions, and recovers -- without a re-dial -- once the partition
// heals. It lives as a Test function, not an Example, for the same reason
// TestReadmeUsageSnippet does: it needs a *testing.T to enter a
// testing/synctest bubble, so the breaker's deadline-based probing costs no
// real wall-clock time.
func TestCircuitBreakerAcrossPartitionAndHeal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := netchaos.NewNetwork(netchaos.WithSeed(1))

		l, err := n.Listen("tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()
		accepted := acceptOne(l)

		ctx := netchaos.WithPeerName(context.Background(), "client")
		client, err := n.DialContext(ctx, "tcp", "server")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = client.Close() }()
		server := <-accepted
		defer func() { _ = server.Close() }()

		// A trivial echo server: bounce every ping straight back.
		go func() {
			buf := make([]byte, 4)
			for {
				nr, err := server.Read(buf)
				if err != nil {
					return
				}
				if _, err := server.Write(buf[:nr]); err != nil {
					return
				}
			}
		}()

		ping := func() error {
			if err := client.SetDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
				return err
			}
			if _, err := client.Write([]byte("ping")); err != nil {
				return err
			}
			buf := make([]byte, 4)
			_, err := client.Read(buf)
			return err
		}

		// A minimal breaker: any failed ping opens it; a call while open is
		// refused without touching the network.
		open := false
		call := func() error {
			if open {
				return errors.New("circuit breaker open")
			}
			if err := ping(); err != nil {
				open = true
				return err
			}
			return nil
		}

		if err := call(); err != nil {
			t.Fatalf("initial ping failed: %v", err)
		}

		n.Partition("client", "server")
		if err := call(); err == nil {
			t.Fatal("ping succeeded while partitioned, want a deadline failure")
		}
		if !open {
			t.Fatal("breaker did not open after a partitioned ping failed")
		}

		n.Heal("client", "server")
		if err := ping(); err != nil { // probe: bypass the open breaker directly
			t.Fatalf("probe ping after Heal failed: %v (breaker should recover without a re-dial)", err)
		}
	})
}

// fetchWithRetry writes payload and waits for an echoed response, retrying
// on a read timeout. It stands in for a real client's retry policy -- the
// thing this package exists to let a test exercise against a lossy,
// latent, simulated network.
func fetchWithRetry(conn net.Conn, payload []byte) ([]byte, error) {
	const maxAttempts = 20
	buf := make([]byte, len(payload))
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if _, err := conn.Write(payload); err != nil {
			return nil, err
		}
		if err := conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
			return nil, err
		}
		n, err := io.ReadFull(conn, buf)
		if err == nil {
			return buf[:n], nil
		}
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("exhausted %d attempts", maxAttempts)
}
