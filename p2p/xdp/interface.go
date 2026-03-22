package xdp

import (
	"github.com/multiversx/mx-chain-core-go/core"
)

// XDPMessenger is the main interface for XDP messaging
type XDPMessenger interface {
	// Send sends a message to a specific peer via XDP
	Send(topic string, data []byte, peerID core.PeerID) error

	// Broadcast broadcasts a message to all mesh peers
	Broadcast(topic string, data []byte)

	// BroadcastOnChannel broadcasts on a specific channel
	BroadcastOnChannel(channel string, topic string, data []byte)

	// HasXDP checks if a peer supports XDP
	HasXDP(peerID core.PeerID) bool

	// GetXDPPeers returns all XDP-capable peers
	GetXDPPeers() []core.PeerID

	// IsEnabled returns true if XDP is enabled and functional
	IsEnabled() bool

	// Close closes the XDP messenger
	Close() error
}

// XDPSender is the interface for sending XDP messages
type XDPSender interface {
	// Send sends a message to a specific peer
	Send(topic string, data []byte, peerID core.PeerID) error

	// Broadcast sends a message to multiple peers
	Broadcast(topic string, data []byte, peerIDs []core.PeerID) error

	// Close closes the sender
	Close() error
}

// XDPReceiver is the interface for receiving XDP messages
type XDPReceiver interface {
	// Start starts receiving messages
	Start() error

	// Stop stops receiving messages
	Stop() error

	// SetMessageHandler sets the handler for received messages
	SetMessageHandler(handler MessageHandler)

	// Close closes the receiver
	Close() error
}

// XDPRouter is the interface for routing between XDP and libp2p
type XDPRouter interface {
	// Send sends a message, routing via XDP or libp2p as appropriate
	Send(topic string, data []byte, peerID core.PeerID) error

	// Broadcast broadcasts a message
	Broadcast(topic string, data []byte)

	// BroadcastOnChannel broadcasts on a specific channel
	BroadcastOnChannel(channel string, topic string, data []byte)

	// SendToConnectedPeer sends a direct message to a connected peer
	SendToConnectedPeer(topic string, data []byte, peerID core.PeerID) error

	// HasXDPPeer checks if a peer supports XDP
	HasXDPPeer(peerID core.PeerID) bool

	// Enable enables XDP routing
	Enable()

	// Disable disables XDP routing (all via libp2p)
	Disable()

	// IsEnabled returns true if XDP routing is enabled
	IsEnabled() bool

	// Close closes the router
	Close() error
}

// XDPPeerManager is the interface for managing XDP peers
type XDPPeerManager interface {
	// HasXDP checks if a peer supports XDP
	HasXDP(peerID core.PeerID) bool

	// GetXDPPeers returns all XDP-capable peers
	GetXDPPeers() []core.PeerID

	// GetConnectedCount returns the number of connected XDP peers
	GetConnectedCount() int

	// Close closes the peer manager
	Close() error
}

// XDPSocket is the interface for the XDP socket
type XDPSocket interface {
	// Send sends data to an address
	Send(data []byte, addr interface{}) error

	// Receive receives data
	Receive(buf []byte) (int, interface{}, error)

	// Close closes the socket
	Close() error

	// IsClosed returns true if closed
	IsClosed() bool
}

// XDPMetrics is the interface for XDP metrics
type XDPMetrics interface {
	// RecordSend records a sent packet
	RecordSend(bytes int, latencyMicros uint64, viaXDP bool)

	// RecordReceive records a received packet
	RecordReceive(bytes int, latencyMicros uint64)

	// RecordSendError records a send error
	RecordSendError()

	// RecordReceiveError records a receive error
	RecordReceiveError()

	// GetSnapshot returns a metrics snapshot
	GetSnapshot() MetricsSnapshot
}
