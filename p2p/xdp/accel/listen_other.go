//go:build !linux

package accel

import (
	"net"

	"github.com/multiversx/mx-chain-communication-go/p2p"
	"github.com/multiversx/mx-chain-communication-go/p2p/xdp"
)

func newListenUDP(_ xdp.Config, _ p2p.Logger, network string, laddr *net.UDPAddr) (net.PacketConn, error) {
	return defaultListenUDP(network, laddr)
}
