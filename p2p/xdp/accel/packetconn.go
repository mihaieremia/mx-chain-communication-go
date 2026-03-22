package accel

import (
	"net"
	"time"
)

// XDPPacketConn is a thin wrapper around net.PacketConn that can back a QUIC
// transport with either a plain *net.UDPConn (fallback) or an AF_XDP socket.
type XDPPacketConn struct {
	conn    net.PacketConn
	isAFXDP bool
}

// WrapUDPConn creates an XDPPacketConn backed by a standard UDP connection.
// This is used for the fallback / non-XDP path and in tests.
func WrapUDPConn(conn *net.UDPConn) *XDPPacketConn {
	return &XDPPacketConn{conn: conn, isAFXDP: false}
}

// ReadFrom reads a packet from the connection.
func (x *XDPPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	return x.conn.ReadFrom(b)
}

// WriteTo writes a packet to the given address.
func (x *XDPPacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	return x.conn.WriteTo(b, addr)
}

// Close closes the underlying connection.
func (x *XDPPacketConn) Close() error { return x.conn.Close() }

// LocalAddr returns the local network address.
func (x *XDPPacketConn) LocalAddr() net.Addr { return x.conn.LocalAddr() }

// SetDeadline sets the read and write deadlines on the connection.
func (x *XDPPacketConn) SetDeadline(t time.Time) error { return x.conn.SetDeadline(t) }

// SetReadDeadline sets the deadline for future ReadFrom calls.
func (x *XDPPacketConn) SetReadDeadline(t time.Time) error { return x.conn.SetReadDeadline(t) }

// SetWriteDeadline sets the deadline for future WriteTo calls.
func (x *XDPPacketConn) SetWriteDeadline(t time.Time) error { return x.conn.SetWriteDeadline(t) }

// IsAFXDP reports whether this connection is backed by an AF_XDP socket.
func (x *XDPPacketConn) IsAFXDP() bool { return x.isAFXDP }

// compile-time interface check
var _ net.PacketConn = (*XDPPacketConn)(nil)
