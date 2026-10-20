package netchaos

import (
	"fmt"
	"net"
	"net/netip"
)

// canonicalPeer resolves a name or literal IP within this Network. n.mu
// protects the address book; it never consults DNS.
func (n *Network) canonicalPeer(host string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.canonicalPeerLocked(host)
}

func (n *Network) canonicalPeerLocked(host string) string {
	ip, ok := parseLiteralIP(host)
	if !ok {
		return host
	}
	key := ip.String()
	if owner, found := n.ipOwners[key]; found {
		return owner
	}
	n.ipOwners[key] = key // reserve a literal before synthetic allocation
	return key
}

// peerIPLocked assigns a stable address for a name in the requested family.
// Literal addresses retain their own bits; the synthetic ranges are local to
// this Network and skip every literal or synthetic address already known.
func (n *Network) peerIPLocked(peer string, family int) (net.IP, error) {
	if ip, literal := parseLiteralIP(peer); literal {
		if (ip.Is4() && family != 4) || (ip.Is6() && family != 6) {
			return nil, fmt.Errorf("address family mismatch: %s", peer)
		}
		n.ipOwners[ip.String()] = peer
		return net.IP(ip.AsSlice()), nil
	}
	if n.peerIPs[peer] == nil {
		n.peerIPs[peer] = make(map[int]net.IP)
	}
	if ip := n.peerIPs[peer][family]; ip != nil {
		return append(net.IP(nil), ip...), nil
	}
	for {
		var candidate netip.Addr
		if family == 4 {
			// 198.18.0.0/15 is reserved for benchmark networks.
			v := n.nextIPv4
			n.nextIPv4++
			if v >= 1<<17 {
				return nil, fmt.Errorf("synthetic IPv4 space exhausted")
			}
			candidate = netip.AddrFrom4([4]byte{198, byte(18 + v>>16), byte(v >> 8), byte(v)})
		} else {
			v := n.nextIPv6
			n.nextIPv6++
			if v == ^uint64(0) {
				return nil, fmt.Errorf("synthetic IPv6 space exhausted")
			}
			candidate = netip.AddrFrom16([16]byte{0xfd, 0, 0, 0, 0, 0, 0, 0, byte(v >> 56), byte(v >> 48), byte(v >> 40), byte(v >> 32), byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
		}
		if _, occupied := n.ipOwners[candidate.String()]; occupied {
			continue
		}
		ip := net.IP(candidate.AsSlice())
		n.ipOwners[candidate.String()] = peer
		n.peerIPs[peer][family] = ip
		return append(net.IP(nil), ip...), nil
	}
}

func addressFamily(network string, host string) int {
	if network == "tcp6" {
		return 6
	}
	if network == "tcp4" {
		return 4
	}
	if ip, ok := parseLiteralIP(host); ok && ip.Is6() {
		return 6
	}
	return 4
}
