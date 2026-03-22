package xdp

import (
	"net"
)

// DataSocket abstracts both UDP and AF_XDP socket implementations
// This allows transparent switching between kernel UDP and kernel-bypass AF_XDP
type DataSocket interface {
	// Send sends a packet to the specified address
	Send(data []byte, addr *net.UDPAddr) error

	// SendBatch sends multiple packets (optimization for batch sending)
	SendBatch(packets [][]byte, addrs []*net.UDPAddr) error

	// Receive receives a packet into the provided buffer
	// Returns the number of bytes received, the source address, and any error
	Receive(buf []byte) (int, *net.UDPAddr, error)

	// LocalAddr returns the local address the socket is bound to
	LocalAddr() *net.UDPAddr

	// GetStats returns socket statistics
	GetStats() SocketStats

	// Close closes the socket and releases resources
	Close() error

	// IsClosed returns true if the socket has been closed
	IsClosed() bool
}

// SocketType indicates which socket implementation is in use
type SocketType int

const (
	// SocketTypeUDP indicates a standard UDP socket
	SocketTypeUDP SocketType = iota
	// SocketTypeAFXDP indicates a real AF_XDP kernel-bypass socket
	SocketTypeAFXDP
)

// String returns the string representation of the socket type
func (st SocketType) String() string {
	switch st {
	case SocketTypeUDP:
		return "UDP"
	case SocketTypeAFXDP:
		return "AF_XDP"
	default:
		return "Unknown"
	}
}
