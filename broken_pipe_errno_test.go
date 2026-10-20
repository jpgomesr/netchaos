//go:build !plan9

package netchaos

import (
	"errors"
	"net"
	"syscall"
	"testing"
)

func TestRemoteCloseMatchesSyscallEPIPE(t *testing.T) {
	client, server := newTestConnPair()
	defer func() { _ = client.Close() }()
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := client.Write([]byte("x"))
	var opErr *net.OpError
	if !errors.As(err, &opErr) || !errors.Is(err, syscall.EPIPE) || errors.Is(err, net.ErrClosed) {
		t.Fatalf("remote-close Write = %v, want *net.OpError matching EPIPE only", err)
	}
}
