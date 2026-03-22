package accel

import (
	"net"

	"github.com/multiversx/mx-chain-communication-go/p2p"
	"github.com/multiversx/mx-chain-communication-go/p2p/xdp"
)

// ListenUDPFunc matches the signature expected by quicreuse.OverrideListenUDP
type ListenUDPFunc func(network string, laddr *net.UDPAddr) (net.PacketConn, error)

// NewListenUDP creates a ListenUDP function that returns AF_XDP-backed
// PacketConns on Linux when supported, falling back to net.ListenUDP otherwise.
func NewListenUDP(cfg xdp.Config, log p2p.Logger) ListenUDPFunc {
	return func(network string, laddr *net.UDPAddr) (net.PacketConn, error) {
		return newListenUDP(cfg, log, network, laddr)
	}
}

func defaultListenUDP(network string, laddr *net.UDPAddr) (net.PacketConn, error) {
	return net.ListenUDP(network, laddr)
}
