package engine

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/anacrolix/torrent"
)

// binding pins sockets to the addresses of one network interface, normally a
// VPN tunnel such as Mullvad's wg0-mullvad. A socket bound to an address only
// works while that address exists, so if the tunnel goes down traffic stops
// instead of leaking out through the regular connection.
type binding struct {
	iface  string
	v4, v6 net.IP // nil when the interface has no address of that family
}

func bindInterface(name string) (*binding, error) {
	v4, v6, err := interfaceAddrs(name)
	if err != nil {
		return nil, err
	}
	return &binding{iface: name, v4: v4, v6: v6}, nil
}

func interfaceAddrs(name string) (v4, v6 net.IP, err error) {
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return nil, nil, fmt.Errorf("VPN interface %q not found, is the VPN connected?", name)
	}
	if ifi.Flags&net.FlagUp == 0 {
		return nil, nil, fmt.Errorf("VPN interface %q is down", name)
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return nil, nil, err
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLinkLocalUnicast() {
			continue
		}
		if ip4 := ipn.IP.To4(); ip4 != nil {
			if v4 == nil {
				v4 = ip4
			}
		} else if v6 == nil {
			v6 = ipn.IP
		}
	}
	if v4 == nil && v6 == nil {
		return nil, nil, fmt.Errorf("VPN interface %q has no IP address", name)
	}
	return v4, v6, nil
}

func (b *binding) String() string {
	var addrs []string
	for _, ip := range []net.IP{b.v4, b.v6} {
		if ip != nil {
			addrs = append(addrs, ip.String())
		}
	}
	return b.iface + " (" + strings.Join(addrs, ", ") + ")"
}

// check reports whether the interface is still up with the addresses the
// sockets were bound to.
func (b *binding) check() error {
	v4, v6, err := interfaceAddrs(b.iface)
	if err != nil {
		return err
	}
	if !v4.Equal(b.v4) || !v6.Equal(b.v6) {
		return fmt.Errorf("VPN interface %q changed address, restart unified", b.iface)
	}
	return nil
}

// local picks the source address for a network such as "tcp4" or "udp".
// Unqualified networks prefer IPv4.
func (b *binding) local(network string) net.IP {
	switch {
	case strings.HasSuffix(network, "6"):
		return b.v6
	case strings.HasSuffix(network, "4"):
		return b.v4
	case b.v4 != nil:
		return b.v4
	}
	return b.v6
}

func (b *binding) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	ip := b.local(network)
	if ip == nil {
		return nil, fmt.Errorf("VPN interface %q has no address for %s", b.iface, network)
	}
	// With a local address set, Go only tries remote addresses of the same family.
	d := net.Dialer{}
	if strings.HasPrefix(network, "udp") {
		d.LocalAddr = &net.UDPAddr{IP: ip}
	} else {
		d.LocalAddr = &net.TCPAddr{IP: ip}
	}
	return d.DialContext(ctx, network, addr)
}

func (b *binding) listenPacket(network, _ string) (net.PacketConn, error) {
	ip := b.local(network)
	if ip == nil {
		return nil, fmt.Errorf("VPN interface %q has no address for %s", b.iface, network)
	}
	return net.ListenPacket(network, net.JoinHostPort(ip.String(), "0"))
}

func (b *binding) configure(cfg *torrent.ClientConfig) {
	cfg.DisableIPv4 = b.v4 == nil
	cfg.DisableIPv6 = b.v6 == nil
	// Listening sockets: incoming TCP, and the UDP socket that uTP and the DHT share.
	cfg.ListenHost = func(network string) string {
		if ip := b.local(network); ip != nil {
			return ip.String()
		}
		return ""
	}
	// The library's TCP sockets dial out from an unspecified address, so they
	// are not used for outgoing connections; addPeerDialers replaces them.
	cfg.DialForPeerConns = false
	cfg.HTTPDialContext = b.DialContext
	cfg.TrackerDialContext = b.DialContext
	cfg.TrackerListenPacket = b.listenPacket
	// UPnP would open a port on the LAN router, which is no use behind a VPN.
	cfg.NoDefaultPortForwarding = true
	// WebRTC peers use their own network stack, which cannot be bound.
	cfg.DisableWebtorrent = true
}

// addPeerDialers sets up outgoing peer connections: uTP through the bound UDP
// sockets themselves, TCP through dialers with a fixed source address.
func (b *binding) addPeerDialers(client *torrent.Client) {
	for _, l := range client.Listeners() {
		if d, ok := l.(torrent.Dialer); ok && strings.HasPrefix(d.DialerNetwork(), "udp") {
			client.AddDialer(d)
		}
	}
	for _, network := range []string{"tcp4", "tcp6"} {
		if b.local(network) != nil {
			client.AddDialer(torrent.NetworkDialer{Network: network, Dialer: b})
		}
	}
}
