package xdp

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"github.com/multiversx/mx-chain-communication-go/p2p"
)

// UDPSocket represents a standard UDP socket implementation of DataSocket
// This is the fallback when AF_XDP is not available
type UDPSocket struct {
	mu sync.RWMutex

	// Configuration
	config     Config
	ifaceName  string
	localAddr  *net.UDPAddr

	// UDP socket (fallback or primary depending on platform)
	udpConn *net.UDPConn

	// State
	closed   atomic.Bool

	// Stats
	txPackets atomic.Uint64
	rxPackets atomic.Uint64
	txBytes   atomic.Uint64
	rxBytes   atomic.Uint64
	txErrors  atomic.Uint64
	rxErrors  atomic.Uint64

	// Context for graceful shutdown
	ctx        context.Context
	cancelFunc context.CancelFunc

	// Logger
	log p2p.Logger
}

// SocketConfig holds socket configuration
type SocketConfig struct {
	Interface string
	Port      uint16
	QueueSize uint32
	BatchSize uint32
}

// Compile-time check that UDPSocket implements DataSocket
var _ DataSocket = (*UDPSocket)(nil)

// Socket is an alias for UDPSocket for backwards compatibility
// Deprecated: Use DataSocket interface and NewDataSocket factory instead
type Socket = UDPSocket

// NewSocket creates a new socket (backwards compatible, uses UDP)
// Deprecated: Use NewDataSocket factory instead
func NewSocket(config Config, log p2p.Logger) (*Socket, error) {
	return NewUDPSocket(config, log)
}

// NewUDPSocket creates a new UDP socket
func NewUDPSocket(config Config, log p2p.Logger) (*UDPSocket, error) {
	if log == nil {
		return nil, ErrNilLogger
	}

	// Check if XDP is supported
	supported, reason := IsXDPSupported()
	if !supported {
		log.Debug("XDP not supported, using UDP fallback", "reason", reason)
	}

	ctx, cancel := context.WithCancel(context.Background())

	s := &UDPSocket{
		config:     config,
		ctx:        ctx,
		cancelFunc: cancel,
		log:        log,
	}

	// Determine interface
	if config.Interface != "" {
		s.ifaceName = config.Interface
	} else {
		iface, err := GetDefaultInterface()
		if err != nil {
			log.Debug("could not determine default interface, using all interfaces")
			s.ifaceName = ""
		} else {
			s.ifaceName = iface
		}
	}

	// Create UDP socket
	addr := &net.UDPAddr{
		Port: int(config.Port),
	}

	// If interface is specified, try to bind to its IP
	if s.ifaceName != "" {
		if ip := getInterfaceIP(s.ifaceName); ip != nil {
			addr.IP = ip
		}
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create UDP socket: %w", err)
	}

	s.udpConn = conn
	s.localAddr = conn.LocalAddr().(*net.UDPAddr)

	// Set socket options for better performance
	if err := setSocketOptions(conn); err != nil {
		log.Debug("could not set socket options", "error", err)
	}

	log.Info("XDP socket created",
		"address", s.localAddr.String(),
		"interface", s.ifaceName,
		"xdpNative", supported,
	)

	return s, nil
}

// Send sends a packet to the specified address
func (s *UDPSocket) Send(data []byte, addr *net.UDPAddr) error {
	if s.closed.Load() {
		return ErrSocketClosed
	}

	n, err := s.udpConn.WriteToUDP(data, addr)
	if err != nil {
		s.txErrors.Add(1)
		return err
	}

	s.txPackets.Add(1)
	s.txBytes.Add(uint64(n))
	return nil
}

// SendBatch sends multiple packets (optimization for batch sending)
func (s *UDPSocket) SendBatch(packets [][]byte, addrs []*net.UDPAddr) error {
	if s.closed.Load() {
		return ErrSocketClosed
	}

	if len(packets) != len(addrs) {
		return fmt.Errorf("packets and addresses length mismatch")
	}

	// For now, send one by one
	// Future optimization: use sendmmsg on Linux
	for i, pkt := range packets {
		if err := s.Send(pkt, addrs[i]); err != nil {
			// Continue sending other packets even if one fails
			s.log.Trace("batch send error", "index", i, "error", err)
		}
	}

	return nil
}

// Receive receives a packet
func (s *UDPSocket) Receive(buf []byte) (int, *net.UDPAddr, error) {
	if s.closed.Load() {
		return 0, nil, ErrSocketClosed
	}

	n, addr, err := s.udpConn.ReadFromUDP(buf)
	if err != nil {
		if !s.closed.Load() {
			s.rxErrors.Add(1)
		}
		return 0, nil, err
	}

	s.rxPackets.Add(1)
	s.rxBytes.Add(uint64(n))
	return n, addr, nil
}

// LocalAddr returns the local address
func (s *UDPSocket) LocalAddr() *net.UDPAddr {
	return s.localAddr
}

// GetStats returns socket statistics
func (s *UDPSocket) GetStats() SocketStats {
	return SocketStats{
		TxPackets: s.txPackets.Load(),
		RxPackets: s.rxPackets.Load(),
		TxBytes:   s.txBytes.Load(),
		RxBytes:   s.rxBytes.Load(),
		TxErrors:  s.txErrors.Load(),
		RxErrors:  s.rxErrors.Load(),
	}
}

// SocketStats contains socket statistics
type SocketStats struct {
	TxPackets uint64
	RxPackets uint64
	TxBytes   uint64
	RxBytes   uint64
	TxErrors  uint64
	RxErrors  uint64
}

// Close closes the socket
func (s *UDPSocket) Close() error {
	if s.closed.Swap(true) {
		return nil // Already closed
	}

	s.cancelFunc()

	if s.udpConn != nil {
		return s.udpConn.Close()
	}

	return nil
}

// IsClosed returns true if the socket is closed
func (s *UDPSocket) IsClosed() bool {
	return s.closed.Load()
}

// getInterfaceIP returns the first IPv4 address of an interface
func getInterfaceIP(ifaceName string) net.IP {
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return nil
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return nil
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok {
			if ipv4 := ipnet.IP.To4(); ipv4 != nil {
				return ipv4
			}
		}
	}

	return nil
}

// setSocketOptions sets socket options for better performance
func setSocketOptions(conn *net.UDPConn) error {
	// Increase receive buffer size
	if err := conn.SetReadBuffer(4 * 1024 * 1024); err != nil {
		return err
	}

	// Increase send buffer size
	if err := conn.SetWriteBuffer(4 * 1024 * 1024); err != nil {
		return err
	}

	return nil
}
