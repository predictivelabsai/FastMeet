package main

import (
	"net"

	"github.com/pion/turn/v4"
)

// hairpinGenerator fixes relay-to-relay traffic when the TURN server runs in a
// Docker bridge network behind a published port range. If both callers in a
// room are relayed, relay A sends to publicIP:portB; Docker's hairpin NAT then
// delivers it with a bridge-gateway source address, which does not match the
// permission relay B holds for publicIP, so the packet is dropped and ICE
// never connects. The wrapped conns send such packets to 127.0.0.1:portB
// instead and report loopback sources in the relay range as publicIP:port.
type hairpinGenerator struct {
	*turn.RelayAddressGeneratorPortRange
}

func (g hairpinGenerator) AllocatePacketConn(network string, port int) (net.PacketConn, net.Addr, error) {
	c, addr, err := g.RelayAddressGeneratorPortRange.AllocatePacketConn(network, port)
	if err != nil {
		return nil, nil, err
	}
	return &hairpinConn{PacketConn: c, public: g.RelayAddress, min: int(g.MinPort), max: int(g.MaxPort)}, addr, nil
}

type hairpinConn struct {
	net.PacketConn
	public   net.IP
	min, max int
}

func (h *hairpinConn) inRange(p int) bool { return p >= h.min && p <= h.max }

func (h *hairpinConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	if u, ok := addr.(*net.UDPAddr); ok && u.IP.Equal(h.public) && h.inRange(u.Port) {
		addr = &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: u.Port}
	}
	return h.PacketConn.WriteTo(b, addr)
}

func (h *hairpinConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, addr, err := h.PacketConn.ReadFrom(b)
	if u, ok := addr.(*net.UDPAddr); ok && u.IP.IsLoopback() && h.inRange(u.Port) {
		addr = &net.UDPAddr{IP: h.public, Port: u.Port}
	}
	return n, addr, err
}
