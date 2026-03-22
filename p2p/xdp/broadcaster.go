package xdp

import (
	"sync"
	"sync/atomic"

	"github.com/multiversx/mx-chain-communication-go/p2p"
	"github.com/multiversx/mx-chain-communication-go/p2p/xdp/peer"
	"github.com/multiversx/mx-chain-core-go/core"
)

// Broadcaster handles XDP-based message broadcasting
type Broadcaster struct {
	mu sync.RWMutex

	sender        *Sender
	mesh          *Mesh
	peerManager   *peer.Manager
	topicRegistry *TopicRegistry

	// Fallback to libp2p for non-XDP peers
	libp2pFallback LibP2PBroadcaster

	// Stats — atomic counters to avoid mutex on hot broadcast path
	xdpBroadcasts   atomic.Uint64
	libp2pFallbacks atomic.Uint64

	log p2p.Logger
}

// LibP2PBroadcaster is the interface for libp2p broadcast fallback
type LibP2PBroadcaster interface {
	Broadcast(topic string, buff []byte)
	BroadcastOnChannel(channel string, topic string, buff []byte)
}

// NewBroadcaster creates a new XDP broadcaster
func NewBroadcaster(
	sender *Sender,
	mesh *Mesh,
	peerManager *peer.Manager,
	topicRegistry *TopicRegistry,
	log p2p.Logger,
) *Broadcaster {
	return &Broadcaster{
		sender:        sender,
		mesh:          mesh,
		peerManager:   peerManager,
		topicRegistry: topicRegistry,
		log:           log,
	}
}

// SetLibP2PFallback sets the libp2p fallback broadcaster
func (b *Broadcaster) SetLibP2PFallback(fallback LibP2PBroadcaster) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.libp2pFallback = fallback
}

// Broadcast broadcasts a message on a topic
func (b *Broadcaster) Broadcast(topic string, data []byte) {
	b.BroadcastOnChannel(topic, topic, data)
}

// BroadcastOnChannel broadcasts a message on a specific channel
func (b *Broadcaster) BroadcastOnChannel(channel string, topic string, data []byte) {
	// Get mesh peers for this topic
	meshPeers := b.mesh.GetMeshPeers(topic)

	if len(meshPeers) == 0 {
		// No XDP peers, use libp2p fallback
		b.mu.RLock()
		fallback := b.libp2pFallback
		b.mu.RUnlock()

		if fallback != nil {
			fallback.BroadcastOnChannel(channel, topic, data)
		}
		return
	}

	// Separate XDP-capable and non-XDP peers
	xdpPeers := make([]core.PeerID, 0, len(meshPeers))
	nonXDPPeers := make([]core.PeerID, 0)

	for _, peerID := range meshPeers {
		if b.peerManager.HasXDP(peerID) {
			xdpPeers = append(xdpPeers, peerID)
		} else {
			nonXDPPeers = append(nonXDPPeers, peerID)
		}
	}

	// Send to XDP peers
	if len(xdpPeers) > 0 {
		if err := b.sender.Broadcast(topic, data, xdpPeers); err != nil {
			b.log.Trace("XDP broadcast error", "topic", topic, "error", err)
		} else {
			b.xdpBroadcasts.Add(1)
		}
	}

	// Fallback to libp2p for non-XDP peers
	if len(nonXDPPeers) > 0 {
		b.mu.RLock()
		fallback := b.libp2pFallback
		b.mu.RUnlock()

		if fallback != nil {
			fallback.BroadcastOnChannel(channel, topic, data)
			b.libp2pFallbacks.Add(1)
		}
	}
}

// BroadcastToAll broadcasts to all known XDP peers (for special cases)
func (b *Broadcaster) BroadcastToAll(topic string, data []byte) {
	allPeers := b.peerManager.GetXDPPeers()

	if len(allPeers) == 0 {
		// Fallback
		b.mu.RLock()
		fallback := b.libp2pFallback
		b.mu.RUnlock()

		if fallback != nil {
			fallback.Broadcast(topic, data)
		}
		return
	}

	if err := b.sender.Broadcast(topic, data, allPeers); err != nil {
		b.log.Trace("XDP broadcast to all error", "topic", topic, "error", err)
	}
}

// SendToConnectedPeer sends a direct message to a peer
func (b *Broadcaster) SendToConnectedPeer(topic string, data []byte, peerID core.PeerID) error {
	// Try XDP first if peer supports it
	if b.peerManager.HasXDP(peerID) {
		return b.sender.Send(topic, data, peerID)
	}

	// Peer doesn't support XDP, this should be handled by the router
	return ErrPeerNotXDPCapable
}

// SubscribeTopic subscribes to a topic and sets up the mesh
func (b *Broadcaster) SubscribeTopic(topic string) {
	b.mesh.Subscribe(topic)
	b.topicRegistry.Register(topic)
}

// UnsubscribeTopic unsubscribes from a topic
func (b *Broadcaster) UnsubscribeTopic(topic string) {
	b.mesh.Unsubscribe(topic)
}

// UpdateMesh updates the mesh for a topic
func (b *Broadcaster) UpdateMesh(topic string, connectedPeers []core.PeerID) {
	b.mesh.UpdateMesh(topic, connectedPeers)
}

// GetStats returns broadcaster statistics
func (b *Broadcaster) GetStats() BroadcasterStats {
	return BroadcasterStats{
		XDPBroadcasts:   b.xdpBroadcasts.Load(),
		LibP2PFallbacks: b.libp2pFallbacks.Load(),
		MeshStats:       b.mesh.GetStats(),
	}
}

// BroadcasterStats contains broadcaster statistics
type BroadcasterStats struct {
	XDPBroadcasts   uint64
	LibP2PFallbacks uint64
	MeshStats       MeshStats
}

// Close closes the broadcaster
func (b *Broadcaster) Close() error {
	return nil
}
