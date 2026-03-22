//go:build !linux

package xdp

import (
	"net"

	"github.com/multiversx/mx-chain-communication-go/p2p"
)

// AFXDPSocket is not available on non-Linux platforms
// This stub exists only to satisfy compile-time checks
type AFXDPSocket struct {
	localAddr *net.UDPAddr
}

// Compile-time check that AFXDPSocket implements DataSocket
var _ DataSocket = (*AFXDPSocket)(nil)

// NewAFXDPSocket returns an error on non-Linux platforms
func NewAFXDPSocket(config Config, log p2p.Logger) (*AFXDPSocket, error) {
	return nil, ErrXDPNotSupported
}

// Send is not available on non-Linux platforms
func (s *AFXDPSocket) Send(data []byte, addr *net.UDPAddr) error {
	return ErrXDPNotSupported
}

// SendBatch is not available on non-Linux platforms
func (s *AFXDPSocket) SendBatch(packets [][]byte, addrs []*net.UDPAddr) error {
	return ErrXDPNotSupported
}

// Receive is not available on non-Linux platforms
func (s *AFXDPSocket) Receive(buf []byte) (int, *net.UDPAddr, error) {
	return 0, nil, ErrXDPNotSupported
}

// LocalAddr returns nil on non-Linux platforms
func (s *AFXDPSocket) LocalAddr() *net.UDPAddr {
	return s.localAddr
}

// GetStats returns empty stats on non-Linux platforms
func (s *AFXDPSocket) GetStats() SocketStats {
	return SocketStats{}
}

// Close does nothing on non-Linux platforms
func (s *AFXDPSocket) Close() error {
	return nil
}

// IsClosed returns true on non-Linux platforms
func (s *AFXDPSocket) IsClosed() bool {
	return true
}
