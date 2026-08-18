package netchaos

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestAddrSatisfiesNetAddr(t *testing.T) {
	var _ net.Addr = (*addr)(nil)
}

func TestAddrString(t *testing.T) {
	a := &addr{network: "tcp", peer: "server-a"}
	if got, want := a.Network(), "tcp"; got != want {
		t.Fatalf("Network() = %q, want %q", got, want)
	}
	if got, want := a.String(), "server-a"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// TestPeerFromAddr exercises the single address<->peer-name function used by
// both dial resolution (here) and, later, M2-4's partition lookup. netchaos
// v1 gives each peer exactly one address (the addr string IS the peer name),
// so there is no multi-address-per-peer case to cover.
func TestPeerFromAddr(t *testing.T) {
	tests := []struct{ addr, want string }{
		{"client", "client"},
		{"server-a", "server-a"},
		{"server-b", "server-b"},
	}
	for _, tt := range tests {
		if got := peerName(tt.addr); got != tt.want {
			t.Errorf("peerName(%q) = %q, want %q", tt.addr, got, tt.want)
		}
	}
}

func TestRejectsUDP(t *testing.T) {
	err := validateNetwork("udp")
	if !errors.Is(err, ErrUnsupportedNetwork) {
		t.Fatalf("validateNetwork(\"udp\") = %v, want errors.Is ErrUnsupportedNetwork", err)
	}
}

func TestValidateNetworkAcceptsTCPVariants(t *testing.T) {
	for _, n := range []string{"tcp", "tcp4", "tcp6"} {
		if err := validateNetwork(n); err != nil {
			t.Errorf("validateNetwork(%q) = %v, want nil", n, err)
		}
	}
}

func TestValidateNetworkRejectsUnknown(t *testing.T) {
	if err := validateNetwork("carrier-pigeon"); err == nil {
		t.Fatal("validateNetwork(\"carrier-pigeon\") = nil, want an error")
	}
}

func TestPeerNameFromContextAbsent(t *testing.T) {
	if name, ok := peerNameFromContext(context.Background()); ok || name != "" {
		t.Fatalf("peerNameFromContext(no value) = (%q, %v), want (\"\", false)", name, ok)
	}
}

func TestPeerNameFromContextPresent(t *testing.T) {
	ctx := context.WithValue(context.Background(), peerNameCtxKey{}, "client")
	name, ok := peerNameFromContext(ctx)
	if !ok || name != "client" {
		t.Fatalf("peerNameFromContext(with value) = (%q, %v), want (\"client\", true)", name, ok)
	}
}
