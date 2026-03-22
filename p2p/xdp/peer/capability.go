package peer

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiversx/mx-chain-communication-go/p2p"
	"github.com/multiversx/mx-chain-core-go/core"
)

const (
	// XDPCapabilityProtocolID is the protocol ID for XDP capability exchange
	XDPCapabilityProtocolID = protocol.ID("/mvx/xdp/capability/1.0.0")

	// CapabilityExchangeTimeout is the timeout for capability exchange
	CapabilityExchangeTimeout = 30 * time.Second

	// MaxCapabilityMessageSize is the maximum size of a capability message
	MaxCapabilityMessageSize = 1024
)

// Feature flags for XDP capabilities
const (
	FeatureBasic      uint32 = 1 << 0 // Basic XDP support
	FeatureFragments  uint32 = 1 << 1 // Fragment support
	FeatureCompression uint32 = 1 << 2 // Compression support (future)
	FeatureEncryption uint32 = 1 << 3 // End-to-end encryption (future)
)

// Capability represents XDP capability information
type Capability struct {
	Supported bool
	UDPPort   uint16
	PublicKey []byte // 32 bytes for X25519
	Features  uint32
	Version   uint8
}

// Encode encodes the capability to bytes
func (c *Capability) Encode() []byte {
	// Format: supported(1) + version(1) + port(2) + features(4) + pubkey_len(2) + pubkey(32)
	buf := make([]byte, 1+1+2+4+2+len(c.PublicKey))

	if c.Supported {
		buf[0] = 1
	}
	buf[1] = c.Version
	binary.BigEndian.PutUint16(buf[2:4], c.UDPPort)
	binary.BigEndian.PutUint32(buf[4:8], c.Features)
	binary.BigEndian.PutUint16(buf[8:10], uint16(len(c.PublicKey)))
	copy(buf[10:], c.PublicKey)

	return buf
}

// DecodeCapability decodes capability from bytes
func DecodeCapability(data []byte) (*Capability, error) {
	if len(data) < 10 {
		return nil, fmt.Errorf("capability data too short")
	}

	c := &Capability{
		Supported: data[0] == 1,
		Version:   data[1],
		UDPPort:   binary.BigEndian.Uint16(data[2:4]),
		Features:  binary.BigEndian.Uint32(data[4:8]),
	}

	pubKeyLen := binary.BigEndian.Uint16(data[8:10])
	if len(data) < int(10+pubKeyLen) {
		return nil, fmt.Errorf("capability data too short for public key")
	}

	c.PublicKey = make([]byte, pubKeyLen)
	copy(c.PublicKey, data[10:10+pubKeyLen])

	return c, nil
}

// readCapabilityFromStream reads a complete capability message from the stream,
// accumulating reads since a single Read is not guaranteed to return all data.
func readCapabilityFromStream(s network.Stream) ([]byte, error) {
	buf := make([]byte, MaxCapabilityMessageSize)
	var n int
	for n < MaxCapabilityMessageSize {
		nn, err := s.Read(buf[n:])
		n += nn
		if err == io.EOF || nn == 0 {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if n == 0 {
		return nil, fmt.Errorf("empty capability message")
	}
	return buf[:n], nil
}

// CapabilityHandler handles XDP capability exchange with peers
type CapabilityHandler struct {
	mu sync.RWMutex

	host        host.Host
	peerManager *Manager
	localCap    *Capability
	log         p2p.Logger

	// Callback when a peer's capability is received
	onCapabilityReceived func(peerID core.PeerID, cap *Capability)
}

// NewCapabilityHandler creates a new capability handler
func NewCapabilityHandler(
	h host.Host,
	peerManager *Manager,
	udpPort uint16,
	log p2p.Logger,
) *CapabilityHandler {
	ch := &CapabilityHandler{
		host:        h,
		peerManager: peerManager,
		log:         log,
	}

	// Set up local capability
	ch.localCap = &Capability{
		Supported: true,
		Version:   1,
		UDPPort:   udpPort,
		Features:  FeatureBasic | FeatureFragments,
		PublicKey: peerManager.GetPublicKey(),
	}

	// Register stream handler
	h.SetStreamHandler(XDPCapabilityProtocolID, ch.handleStream)

	return ch
}

// SetCapabilityCallback sets the callback for when capability is received
func (ch *CapabilityHandler) SetCapabilityCallback(cb func(peerID core.PeerID, cap *Capability)) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.onCapabilityReceived = cb
}

// handleStream handles incoming capability exchange streams
func (ch *CapabilityHandler) handleStream(s network.Stream) {
	defer s.Close()

	if err := s.SetDeadline(time.Now().Add(CapabilityExchangeTimeout)); err != nil {
		ch.log.Trace("failed to set stream deadline", "error", err)
	}

	remotePeerID := s.Conn().RemotePeer()

	// Read remote capability
	capData, readErr := readCapabilityFromStream(s)
	if readErr != nil {
		ch.log.Trace("failed to read capability",
			"peer", remotePeerID.String(),
			"error", readErr,
		)
		return
	}

	remoteCap, err := DecodeCapability(capData)
	if err != nil {
		ch.log.Trace("failed to decode capability",
			"peer", remotePeerID.String(),
			"error", err,
		)
		return
	}

	// Send our capability
	_, err = s.Write(ch.localCap.Encode())
	if err != nil {
		ch.log.Trace("failed to send capability",
			"peer", remotePeerID.String(),
			"error", err,
		)
		return
	}

	// Process the received capability
	ch.processCapability(core.PeerID(remotePeerID), remoteCap, s.Conn().RemoteMultiaddr())
}

// ExchangeCapability initiates capability exchange with a peer
func (ch *CapabilityHandler) ExchangeCapability(ctx context.Context, peerID core.PeerID) (*Capability, error) {
	// Open stream to peer
	s, err := ch.host.NewStream(ctx, peer.ID(peerID), XDPCapabilityProtocolID)
	if err != nil {
		return nil, fmt.Errorf("failed to open capability stream: %w", err)
	}
	defer s.Close()

	// Set deadline
	deadline := time.Now().Add(CapabilityExchangeTimeout)
	s.SetDeadline(deadline)

	// Send our capability
	_, err = s.Write(ch.localCap.Encode())
	if err != nil {
		return nil, fmt.Errorf("failed to send capability: %w", err)
	}

	// Read remote capability
	capData, readErr := readCapabilityFromStream(s)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read capability: %w", readErr)
	}

	remoteCap, err := DecodeCapability(capData)
	if err != nil {
		return nil, fmt.Errorf("failed to decode capability: %w", err)
	}

	// Process the received capability
	ch.processCapability(peerID, remoteCap, s.Conn().RemoteMultiaddr())

	return remoteCap, nil
}

// processCapability processes a received capability
func (ch *CapabilityHandler) processCapability(peerID core.PeerID, cap *Capability, remoteAddr interface{}) {
	if !cap.Supported {
		ch.log.Trace("peer does not support XDP", "peer", peerID.Pretty())
		return
	}

	// Extract IP from the multiaddr and construct UDP address
	var udpAddr *net.UDPAddr

	// Try to get IP from the connection's remote address
	// This is a simplified version - in practice you'd parse the multiaddr properly
	if ma, ok := remoteAddr.(fmt.Stringer); ok {
		// Parse multiaddr to extract IP
		// Format is typically /ip4/x.x.x.x/tcp/port or similar
		maStr := ma.String()
		ip := extractIPFromMultiaddr(maStr)
		if ip != nil {
			udpAddr = &net.UDPAddr{
				IP:   ip,
				Port: int(cap.UDPPort),
			}
		}
	}

	if udpAddr == nil {
		ch.log.Trace("could not determine UDP address for peer", "peer", peerID.Pretty())
		return
	}

	// Register the peer with their XDP capability
	err := ch.peerManager.RegisterPeer(peerID, cap, udpAddr)
	if err != nil {
		ch.log.Warn("failed to register XDP peer",
			"peer", peerID.Pretty(),
			"error", err,
		)
		return
	}

	ch.log.Debug("XDP capability exchanged",
		"peer", peerID.Pretty(),
		"udpAddr", udpAddr.String(),
		"features", cap.Features,
	)

	// Call callback if set
	ch.mu.RLock()
	cb := ch.onCapabilityReceived
	ch.mu.RUnlock()

	if cb != nil {
		cb(peerID, cap)
	}
}

// UpdateLocalCapability updates the local capability (e.g., after key rotation)
func (ch *CapabilityHandler) UpdateLocalCapability(cap *Capability) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.localCap = cap
}

// GetLocalCapability returns the local capability
func (ch *CapabilityHandler) GetLocalCapability() *Capability {
	ch.mu.RLock()
	defer ch.mu.RUnlock()

	// Return a copy
	return &Capability{
		Supported: ch.localCap.Supported,
		Version:   ch.localCap.Version,
		UDPPort:   ch.localCap.UDPPort,
		Features:  ch.localCap.Features,
		PublicKey: append([]byte{}, ch.localCap.PublicKey...),
	}
}

// extractIPFromMultiaddr extracts IP address from a multiaddr string
func extractIPFromMultiaddr(maStr string) net.IP {
	// Simple parser for /ip4/x.x.x.x/... or /ip6/.../...
	// This is a simplified implementation

	// Try to find ip4
	var ip net.IP

	// Parse the multiaddr manually
	// Format: /ip4/1.2.3.4/tcp/1234 or /ip6/::1/tcp/1234
	parts := splitMultiaddr(maStr)

	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == "ip4" || parts[i] == "ip6" {
			ip = net.ParseIP(parts[i+1])
			if ip != nil {
				return ip
			}
		}
	}

	return nil
}

// splitMultiaddr splits a multiaddr string into components
func splitMultiaddr(maStr string) []string {
	var parts []string
	current := ""

	for _, ch := range maStr {
		if ch == '/' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(ch)
		}
	}

	if current != "" {
		parts = append(parts, current)
	}

	return parts
}

// Close closes the capability handler
func (ch *CapabilityHandler) Close() error {
	ch.host.RemoveStreamHandler(XDPCapabilityProtocolID)
	return nil
}
