//go:build linux

package xdp

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"github.com/multiversx/mx-chain-communication-go/p2p"
	"github.com/multiversx/mx-chain-communication-go/p2p/xdp/afxdp"
)

// AFXDPSocket wraps the afxdp.Manager to provide a DataSocket interface
// This enables real kernel-bypass packet I/O using AF_XDP
type AFXDPSocket struct {
	mu sync.RWMutex

	// AF_XDP manager
	manager *afxdp.Manager

	// Configuration
	config    Config
	ifaceName string
	localAddr *net.UDPAddr

	// Receive handling
	rxChan   chan *receivedData
	rxClosed atomic.Bool

	// State
	closed atomic.Bool

	// Stats
	txPackets atomic.Uint64
	rxPackets atomic.Uint64
	txBytes   atomic.Uint64
	rxBytes   atomic.Uint64
	txErrors  atomic.Uint64
	rxErrors  atomic.Uint64

	// Logger
	log p2p.Logger
}

// receivedData holds data received from AF_XDP
type receivedData struct {
	data []byte
	addr *net.UDPAddr
}

// Compile-time check that AFXDPSocket implements DataSocket
var _ DataSocket = (*AFXDPSocket)(nil)

// NewAFXDPSocket creates a new AF_XDP socket with real kernel bypass
func NewAFXDPSocket(config Config, log p2p.Logger) (*AFXDPSocket, error) {
	if log == nil {
		return nil, ErrNilLogger
	}

	// Determine interface
	ifaceName := config.Interface
	if ifaceName == "" {
		var err error
		ifaceName, err = GetDefaultInterface()
		if err != nil {
			return nil, fmt.Errorf("failed to determine default interface: %w", err)
		}
	}

	// Convert XDPMode to afxdp.XDPMode
	var afxdpMode afxdp.XDPMode
	switch config.XDPMode {
	case XDPModeNative:
		afxdpMode = afxdp.XDPModeNative
	case XDPModeSKB:
		afxdpMode = afxdp.XDPModeSKB
	case XDPModeHW:
		afxdpMode = afxdp.XDPModeHW
	default:
		afxdpMode = afxdp.XDPModeAuto
	}

	// Create AF_XDP manager config
	managerConfig := afxdp.ManagerConfig{
		Interface:   ifaceName,
		NumQueues:   config.NumQueues,
		XDPMode:     afxdpMode,
		PollTimeout: 1000, // 1 second
		SocketConfig: afxdp.Config{
			Interface:    ifaceName,
			QueueID:      config.QueueID,
			NumFrames:    afxdp.DefaultNumFrames,
			FrameSize:    afxdp.DefaultFrameSize,
			RxRingSize:   uint32(config.QueueSize),
			TxRingSize:   uint32(config.QueueSize),
			FillRingSize: uint32(config.QueueSize) * 2,
			CompRingSize: uint32(config.QueueSize),
			BatchSize:    int(config.BatchSize),
			ZeroCopy:     false, // Start with copy mode for compatibility
			NeedWakeup:   true,
			XDPMode:      afxdpMode,
			PollTimeout:  1000,
		},
	}

	// Create AF_XDP manager
	manager, err := afxdp.NewManager(managerConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create AF_XDP manager: %w", err)
	}

	s := &AFXDPSocket{
		manager:   manager,
		config:    config,
		ifaceName: ifaceName,
		localAddr: &net.UDPAddr{
			Port: int(config.Port),
		},
		rxChan: make(chan *receivedData, config.QueueSize),
		log:    log,
	}

	// Start the manager with receive callback
	err = manager.Start(func(queueID int, data []byte) {
		s.handleReceive(queueID, data)
	})
	if err != nil {
		manager.Close()
		return nil, fmt.Errorf("failed to start AF_XDP manager: %w", err)
	}

	log.Info("AF_XDP socket created",
		"interface", ifaceName,
		"mode", afxdpMode,
		"queues", managerConfig.NumQueues,
	)

	return s, nil
}

// handleReceive handles packets received from AF_XDP
func (s *AFXDPSocket) handleReceive(queueID int, data []byte) {
	if s.rxClosed.Load() {
		return
	}

	// AF_XDP gives us raw ethernet frames
	// We need to parse them to extract UDP payload and source address
	// For now, we'll create a copy and extract what we can
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	// Parse the packet to extract UDP info
	addr, payload, err := parseUDPPacket(data)
	if err != nil {
		s.rxErrors.Add(1)
		return
	}

	s.rxPackets.Add(1)
	s.rxBytes.Add(uint64(len(payload)))

	// Queue for receive
	select {
	case s.rxChan <- &receivedData{data: payload, addr: addr}:
	default:
		// Queue full, drop packet
		s.rxErrors.Add(1)
	}
}

// parseUDPPacket parses an ethernet frame to extract UDP payload and source address
// Returns source address, UDP payload, and any error
func parseUDPPacket(data []byte) (*net.UDPAddr, []byte, error) {
	// Minimum sizes: Ethernet (14) + IP (20) + UDP (8) = 42 bytes
	if len(data) < 42 {
		return nil, nil, fmt.Errorf("packet too small: %d bytes", len(data))
	}

	// Check Ethernet type (IPv4 = 0x0800)
	etherType := uint16(data[12])<<8 | uint16(data[13])
	if etherType != 0x0800 {
		return nil, nil, fmt.Errorf("not IPv4: etherType=%04x", etherType)
	}

	// Parse IPv4 header
	ipHeader := data[14:]
	ipVersion := ipHeader[0] >> 4
	if ipVersion != 4 {
		return nil, nil, fmt.Errorf("not IPv4: version=%d", ipVersion)
	}

	ipHeaderLen := int(ipHeader[0]&0x0f) * 4
	if ipHeaderLen < 20 {
		return nil, nil, fmt.Errorf("invalid IP header length: %d", ipHeaderLen)
	}

	// Check protocol (UDP = 17)
	protocol := ipHeader[9]
	if protocol != 17 {
		return nil, nil, fmt.Errorf("not UDP: protocol=%d", protocol)
	}

	// Extract source IP
	srcIP := net.IP(ipHeader[12:16])

	// Parse UDP header
	udpHeader := ipHeader[ipHeaderLen:]
	if len(udpHeader) < 8 {
		return nil, nil, fmt.Errorf("UDP header too small")
	}

	srcPort := uint16(udpHeader[0])<<8 | uint16(udpHeader[1])
	udpLen := uint16(udpHeader[4])<<8 | uint16(udpHeader[5])

	// Extract payload
	payloadStart := 14 + ipHeaderLen + 8
	payloadEnd := 14 + ipHeaderLen + int(udpLen)
	if payloadEnd > len(data) {
		payloadEnd = len(data)
	}

	payload := data[payloadStart:payloadEnd]

	addr := &net.UDPAddr{
		IP:   srcIP,
		Port: int(srcPort),
	}

	return addr, payload, nil
}

// Send sends a packet to the specified address
func (s *AFXDPSocket) Send(data []byte, addr *net.UDPAddr) error {
	if s.closed.Load() {
		return ErrSocketClosed
	}

	// Build raw ethernet frame with IP/UDP headers
	frame, err := buildUDPPacket(s.localAddr, addr, data)
	if err != nil {
		s.txErrors.Add(1)
		return err
	}

	// Send through AF_XDP manager
	if err := s.manager.Send(frame); err != nil {
		s.txErrors.Add(1)
		return err
	}

	s.txPackets.Add(1)
	s.txBytes.Add(uint64(len(data)))
	return nil
}

// buildUDPPacket builds a raw ethernet frame containing a UDP packet
func buildUDPPacket(src, dst *net.UDPAddr, payload []byte) ([]byte, error) {
	// For a proper implementation, we'd need:
	// 1. Destination MAC address (from ARP or neighbor cache)
	// 2. Source MAC address (from interface)
	// 3. Proper IP/UDP checksum calculation
	//
	// This is a simplified version - in production, you'd want to:
	// - Cache MAC addresses
	// - Use hardware checksum offload where available
	// - Handle VLAN tags if needed

	// Ethernet header (14 bytes)
	// For now, use broadcast MAC - real impl needs ARP resolution
	frame := make([]byte, 14+20+8+len(payload))

	// Ethernet: dst MAC (6) + src MAC (6) + ethertype (2)
	// Using broadcast for demo - real impl needs ARP
	copy(frame[0:6], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}) // dst MAC
	copy(frame[6:12], []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00}) // src MAC (should be real)
	frame[12] = 0x08 // IPv4
	frame[13] = 0x00

	// IPv4 header (20 bytes, no options)
	ipHeader := frame[14:]
	ipHeader[0] = 0x45                                  // Version 4, IHL 5 (20 bytes)
	ipHeader[1] = 0x00                                  // DSCP/ECN
	totalLen := 20 + 8 + len(payload)                   // IP + UDP + payload
	ipHeader[2] = byte(totalLen >> 8)                   // Total length
	ipHeader[3] = byte(totalLen)
	ipHeader[4] = 0x00                                  // Identification
	ipHeader[5] = 0x00
	ipHeader[6] = 0x40                                  // Flags (Don't Fragment)
	ipHeader[7] = 0x00                                  // Fragment offset
	ipHeader[8] = 64                                    // TTL
	ipHeader[9] = 17                                    // Protocol (UDP)
	// Checksum calculated below
	ipHeader[10] = 0x00
	ipHeader[11] = 0x00

	// Source IP
	srcIP := src.IP.To4()
	if srcIP == nil {
		srcIP = net.IPv4zero.To4()
	}
	copy(ipHeader[12:16], srcIP)

	// Destination IP
	dstIP := dst.IP.To4()
	if dstIP == nil {
		return nil, fmt.Errorf("destination must be IPv4")
	}
	copy(ipHeader[16:20], dstIP)

	// Calculate IP checksum
	ipChecksum := calculateIPChecksum(ipHeader[:20])
	ipHeader[10] = byte(ipChecksum >> 8)
	ipHeader[11] = byte(ipChecksum)

	// UDP header (8 bytes)
	udpHeader := frame[34:]
	udpHeader[0] = byte(src.Port >> 8) // Source port
	udpHeader[1] = byte(src.Port)
	udpHeader[2] = byte(dst.Port >> 8) // Destination port
	udpHeader[3] = byte(dst.Port)
	udpLen := 8 + len(payload)
	udpHeader[4] = byte(udpLen >> 8) // UDP length
	udpHeader[5] = byte(udpLen)
	udpHeader[6] = 0x00 // Checksum (optional for IPv4)
	udpHeader[7] = 0x00

	// Copy payload
	copy(frame[42:], payload)

	return frame, nil
}

// calculateIPChecksum calculates the IP header checksum
func calculateIPChecksum(header []byte) uint16 {
	var sum uint32
	for i := 0; i < len(header); i += 2 {
		sum += uint32(header[i])<<8 | uint32(header[i+1])
	}
	for sum > 0xffff {
		sum = (sum >> 16) + (sum & 0xffff)
	}
	return ^uint16(sum)
}

// SendBatch sends multiple packets
func (s *AFXDPSocket) SendBatch(packets [][]byte, addrs []*net.UDPAddr) error {
	if s.closed.Load() {
		return ErrSocketClosed
	}

	if len(packets) != len(addrs) {
		return fmt.Errorf("packets and addresses length mismatch")
	}

	// Build frames
	frames := make([][]byte, len(packets))
	for i, pkt := range packets {
		frame, err := buildUDPPacket(s.localAddr, addrs[i], pkt)
		if err != nil {
			s.log.Trace("failed to build packet", "index", i, "error", err)
			continue
		}
		frames[i] = frame
	}

	// Send batch through manager
	sent, err := s.manager.SendBatch(frames)
	if err != nil {
		s.txErrors.Add(uint64(len(packets) - sent))
		return err
	}

	s.txPackets.Add(uint64(sent))
	return nil
}

// Receive receives a packet
func (s *AFXDPSocket) Receive(buf []byte) (int, *net.UDPAddr, error) {
	if s.closed.Load() {
		return 0, nil, ErrSocketClosed
	}

	// Wait for received data
	select {
	case received, ok := <-s.rxChan:
		if !ok {
			return 0, nil, ErrSocketClosed
		}
		n := copy(buf, received.data)
		return n, received.addr, nil
	}
}

// LocalAddr returns the local address
func (s *AFXDPSocket) LocalAddr() *net.UDPAddr {
	return s.localAddr
}

// GetStats returns socket statistics
func (s *AFXDPSocket) GetStats() SocketStats {
	return SocketStats{
		TxPackets: s.txPackets.Load(),
		RxPackets: s.rxPackets.Load(),
		TxBytes:   s.txBytes.Load(),
		RxBytes:   s.rxBytes.Load(),
		TxErrors:  s.txErrors.Load(),
		RxErrors:  s.rxErrors.Load(),
	}
}

// Close closes the socket
func (s *AFXDPSocket) Close() error {
	if s.closed.Swap(true) {
		return nil // Already closed
	}

	s.rxClosed.Store(true)
	close(s.rxChan)

	if s.manager != nil {
		return s.manager.Close()
	}

	return nil
}

// IsClosed returns true if the socket is closed
func (s *AFXDPSocket) IsClosed() bool {
	return s.closed.Load()
}
