package netchaos_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jpgomesr/netchaos"
)

// TestHTTPFaultIntegration exercises the standard HTTP transport against the
// faulted Network, including responses that the server writes before closing.
func TestHTTPFaultIntegration(t *testing.T) {
	for _, fault := range []string{"latency", "bandwidth", "partition-heal", "reset", "loss", "corruption"} {
		for _, keepAlive := range []bool{true, false} {
			name := fault + "/keep-alive"
			if !keepAlive {
				name = fault + "/close"
			}
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					n := netchaos.NewNetwork(integrationOptions(fault)...)
					l, err := n.Listen("tcp", "server:80")
					if err != nil {
						t.Fatal(err)
					}
					entered := make(chan struct{}, 1)
					release := make(chan struct{})
					handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						if fault == "reset" {
							entered <- struct{}{}
							<-release
						}
						_, _ = io.WriteString(w, "one")
						w.(http.Flusher).Flush()
						_, _ = io.WriteString(w, "two")
					})
					srv := &http.Server{Handler: handler}
					serveDone := make(chan struct{})
					go func() { defer close(serveDone); _ = srv.Serve(l) }()
					tr := &http.Transport{
						DisableKeepAlives: !keepAlive,
						DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
							return n.DialContext(netchaos.WithPeerName(ctx, "client"), network, addr)
						},
					}
					defer func() {
						tr.CloseIdleConnections()
						_ = srv.Close()
						<-serveDone
					}()
					if fault == "partition-heal" {
						n.Partition("client", "server")
						time.AfterFunc(5*time.Millisecond, func() { n.Heal("client", "server") })
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://server/", nil)
					if err != nil {
						t.Fatal(err)
					}
					client := &http.Client{Transport: tr}
					if fault == "reset" {
						result := make(chan error, 1)
						go func() {
							resp, err := client.Do(req)
							if err == nil {
								_ = resp.Body.Close()
							}
							result <- err
						}()
						<-entered
						n.Reset("client", "server")
						close(release)
						if err := <-result; err == nil {
							t.Fatal("HTTP request succeeded after Reset")
						}
						return
					}
					resp, err := client.Do(req)
					if fault == "loss" || fault == "corruption" {
						if err == nil {
							body, readErr := io.ReadAll(resp.Body)
							_ = resp.Body.Close()
							if readErr == nil && string(body) == "onetwo" {
								t.Fatalf("HTTP unexpectedly succeeded under %s", fault)
							}
						}
						if fault == "loss" && !errors.Is(err, context.DeadlineExceeded) {
							t.Fatalf("HTTP loss = %v, want context deadline", err)
						}
						assertIntegrationFaultEvent(t, n, fault)
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					body, err := io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					if err != nil || string(body) != "onetwo" {
						t.Fatalf("HTTP body = (%q, %v), want onetwo", body, err)
					}
					if len(resp.TransferEncoding) == 0 || resp.TransferEncoding[0] != "chunked" {
						t.Fatalf("HTTP transfer encoding = %v, want chunked", resp.TransferEncoding)
					}
				})
			})
		}
	}
}

func assertIntegrationFaultEvent(t *testing.T, n *netchaos.Network, fault string) {
	t.Helper()
	for _, event := range n.Trace() {
		if fault == "loss" && event.Dropped || fault == "corruption" && event.Corrupted {
			return
		}
	}
	t.Fatalf("no %s event recorded in trace", fault)
}

func integrationOptions(fault string) []netchaos.Option {
	switch fault {
	case "latency":
		return []netchaos.Option{netchaos.WithLatency(time.Millisecond, time.Millisecond)}
	case "bandwidth":
		return []netchaos.Option{netchaos.WithBandwidth(100_000)}
	case "loss":
		return []netchaos.Option{netchaos.WithPacketLoss(1)}
	case "corruption":
		return []netchaos.Option{netchaos.WithCorruption(1)}
	default:
		return nil
	}
}

func integrationDial(t *testing.T, n *netchaos.Network, fault string) net.Conn {
	t.Helper()
	if fault == "partition-heal" {
		n.Partition("client", "server")
		time.AfterFunc(5*time.Millisecond, func() { n.Heal("client", "server") })
	}
	ctx, cancel := context.WithTimeout(netchaos.WithPeerName(context.Background(), "client"), 5*time.Second)
	defer cancel()
	c, err := n.DialContext(ctx, "tcp", "server")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if fault == "reset" {
		n.Reset("client", "server")
	}
	return c
}

func integrationCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"server"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func TestTLSFaultIntegration(t *testing.T) {
	cert := integrationCertificate(t)
	for _, fault := range []string{"latency", "bandwidth", "partition-heal", "reset", "loss", "corruption"} {
		t.Run(fault, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				n := netchaos.NewNetwork(integrationOptions(fault)...)
				l, err := n.Listen("tcp", "server")
				if err != nil {
					t.Fatal(err)
				}
				serverDone := make(chan struct{})
				go func() {
					defer close(serverDone)
					raw, err := l.Accept()
					if err != nil {
						return
					}
					s := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{cert}})
					defer func() { _ = s.Close() }()
					_ = s.SetDeadline(time.Now().Add(5 * time.Second))
					if err := s.Handshake(); err != nil {
						return
					}
					var request [4]byte
					if _, err := io.ReadFull(s, request[:]); err == nil && string(request[:]) == "ping" {
						_, _ = s.Write([]byte("pong"))
					}
				}()
				raw := integrationDial(t, n, fault)
				c := tls.Client(raw, &tls.Config{ServerName: "server", InsecureSkipVerify: true}) //nolint:gosec // generated test certificate
				defer func() {
					_ = c.Close()
					_ = l.Close()
					<-serverDone
				}()
				err = c.Handshake()
				if fault == "reset" || fault == "loss" || fault == "corruption" {
					if err == nil {
						t.Fatalf("TLS handshake succeeded under %s", fault)
					}
					if fault == "loss" && !errors.Is(err, os.ErrDeadlineExceeded) {
						t.Fatalf("TLS loss = %v, want deadline", err)
					}
					if fault == "loss" || fault == "corruption" {
						assertIntegrationFaultEvent(t, n, fault)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := c.Write([]byte("ping")); err != nil {
					t.Fatal(err)
				}
				var response [4]byte
				if _, err := io.ReadFull(c, response[:]); err != nil || string(response[:]) != "pong" {
					t.Fatalf("TLS echo = (%q, %v), want pong", response, err)
				}
			})
		})
	}
}

type integrationEcho struct{}

func (*integrationEcho) Echo(request string, reply *string) error {
	*reply = request
	return nil
}

func TestRPCFaultIntegration(t *testing.T) {
	for _, fault := range []string{"latency", "bandwidth", "partition-heal", "reset", "loss", "corruption"} {
		t.Run(fault, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				n := netchaos.NewNetwork(integrationOptions(fault)...)
				l, err := n.Listen("tcp", "server")
				if err != nil {
					t.Fatal(err)
				}
				server := rpc.NewServer()
				if err := server.RegisterName("Echo", new(integrationEcho)); err != nil {
					t.Fatal(err)
				}
				serverDone := make(chan struct{})
				go func() {
					defer close(serverDone)
					c, err := l.Accept()
					if err == nil {
						server.ServeConn(c)
					}
				}()
				raw := integrationDial(t, n, fault)
				client := rpc.NewClient(raw)
				defer func() {
					_ = client.Close()
					_ = l.Close()
					<-serverDone
				}()
				var reply string
				err = client.Call("Echo.Echo", "ping", &reply)
				if fault == "reset" || fault == "loss" || fault == "corruption" {
					if err == nil {
						t.Fatalf("RPC call succeeded under %s with reply %q", fault, reply)
					}
					if fault == "loss" && !errors.Is(err, os.ErrDeadlineExceeded) {
						t.Fatalf("RPC loss = %v, want deadline", err)
					}
					if fault == "loss" || fault == "corruption" {
						assertIntegrationFaultEvent(t, n, fault)
					}
					return
				}
				if err != nil || reply != "ping" {
					t.Fatalf("RPC echo = (%q, %v), want ping", reply, err)
				}
			})
		})
	}
}
