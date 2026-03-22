package xdp

import (
	"sync"
	"sync/atomic"

	"github.com/multiversx/mx-chain-communication-go/p2p"
	"github.com/multiversx/mx-chain-communication-go/p2p/xdp/peer"
	"github.com/multiversx/mx-chain-core-go/core"
)

// Router routes messages between XDP and libp2p based on peer capabilities
type Router struct {
	mu sync.RWMutex

	// XDP components
	sender      *Sender
	broadcaster *Broadcaster
	peerManager *peer.Manager

	// libp2p fallback
	libp2pSender      LibP2PDirectSender
	libp2pBroadcaster LibP2PBroadcaster

	// Configuration
	enabled        atomic.Bool
	preferXDP      bool // When both are available, prefer XDP

	// Stats
	xdpSends      atomic.Uint64
	libp2pSends   atomic.Uint64
	xdpBroadcasts atomic.Uint64
	libp2pFallbacks atomic.Uint64
	sendErrors    atomic.Uint64

	log p2p.Logger
}

// LibP2PDirectSender is the interface for libp2p direct send fallback
type LibP2PDirectSender interface {
	SendToConnectedPeer(topic string, buff []byte, peerID core.PeerID) error
}

// RouterConfig holds router configuration
type RouterConfig struct {
	PreferXDP bool
}

// DefaultRouterConfig returns default router configuration
func DefaultRouterConfig() RouterConfig {
	return RouterConfig{
		PreferXDP: true,
	}
}

// NewRouter creates a new message router
func NewRouter(
	sender *Sender,
	broadcaster *Broadcaster,
	peerManager *peer.Manager,
	config RouterConfig,
	log p2p.Logger,
) *Router {
	r := &Router{
		sender:      sender,
		broadcaster: broadcaster,
		peerManager: peerManager,
		preferXDP:   config.PreferXDP,
		log:         log,
	}

	r.enabled.Store(true)

	return r
}

// SetLibP2PFallbacks sets the libp2p fallback handlers
func (r *Router) SetLibP2PFallbacks(sender LibP2PDirectSender, broadcaster LibP2PBroadcaster) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.libp2pSender = sender
	r.libp2pBroadcaster = broadcaster

	// Also set on broadcaster
	if r.broadcaster != nil {
		r.broadcaster.SetLibP2PFallback(broadcaster)
	}
}

// Enable enables the router
func (r *Router) Enable() {
	r.enabled.Store(true)
}

// Disable disables the router (all messages go through libp2p)
func (r *Router) Disable() {
	r.enabled.Store(false)
}

// IsEnabled returns true if the router is enabled
func (r *Router) IsEnabled() bool {
	return r.enabled.Load()
}

// Send sends a message to a specific peer
func (r *Router) Send(topic string, data []byte, peerID core.PeerID) error {
	// If disabled, always use libp2p
	if !r.enabled.Load() {
		return r.sendViaLibP2P(topic, data, peerID)
	}

	// Check if peer supports XDP
	if r.peerManager.HasXDP(peerID) && r.preferXDP {
		err := r.sender.Send(topic, data, peerID)
		if err == nil {
			r.xdpSends.Add(1)
			return nil
		}

		r.log.Trace("XDP send failed, falling back to libp2p",
			"peer", peerID.Pretty(),
			"error", err,
		)
		// Fall through to libp2p
	}

	return r.sendViaLibP2P(topic, data, peerID)
}

// sendViaLibP2P sends a message via libp2p
func (r *Router) sendViaLibP2P(topic string, data []byte, peerID core.PeerID) error {
	r.mu.RLock()
	sender := r.libp2pSender
	r.mu.RUnlock()

	if sender == nil {
		r.sendErrors.Add(1)
		return ErrNilMessageHandler
	}

	err := sender.SendToConnectedPeer(topic, data, peerID)
	if err != nil {
		r.sendErrors.Add(1)
		return err
	}

	r.libp2pSends.Add(1)
	return nil
}

// Broadcast broadcasts a message on a topic
func (r *Router) Broadcast(topic string, data []byte) {
	r.BroadcastOnChannel(topic, topic, data)
}

// BroadcastOnChannel broadcasts a message on a specific channel
func (r *Router) BroadcastOnChannel(channel string, topic string, data []byte) {
	// If disabled, always use libp2p
	if !r.enabled.Load() {
		r.mu.RLock()
		fallback := r.libp2pBroadcaster
		r.mu.RUnlock()

		if fallback != nil {
			fallback.BroadcastOnChannel(channel, topic, data)
			r.libp2pFallbacks.Add(1)
		}
		return
	}

	// Use XDP broadcaster (which handles fallback internally)
	if r.broadcaster != nil {
		r.broadcaster.BroadcastOnChannel(channel, topic, data)
		r.xdpBroadcasts.Add(1)
	}
}

// SendToConnectedPeer sends a direct message to a connected peer
// This is the main entry point from messagesHandler
func (r *Router) SendToConnectedPeer(topic string, data []byte, peerID core.PeerID) error {
	return r.Send(topic, data, peerID)
}

// HasXDPPeer checks if a peer supports XDP
func (r *Router) HasXDPPeer(peerID core.PeerID) bool {
	return r.peerManager.HasXDP(peerID)
}

// GetXDPPeers returns all XDP-capable peers
func (r *Router) GetXDPPeers() []core.PeerID {
	return r.peerManager.GetXDPPeers()
}

// GetStats returns router statistics
func (r *Router) GetStats() RouterStats {
	return RouterStats{
		Enabled:          r.enabled.Load(),
		XDPSends:         r.xdpSends.Load(),
		LibP2PSends:      r.libp2pSends.Load(),
		XDPBroadcasts:    r.xdpBroadcasts.Load(),
		LibP2PFallbacks:  r.libp2pFallbacks.Load(),
		SendErrors:       r.sendErrors.Load(),
		XDPPeerCount:     r.peerManager.GetConnectedCount(),
	}
}

// RouterStats contains router statistics
type RouterStats struct {
	Enabled          bool
	XDPSends         uint64
	LibP2PSends      uint64
	XDPBroadcasts    uint64
	LibP2PFallbacks  uint64
	SendErrors       uint64
	XDPPeerCount     int
}

// Close closes the router
func (r *Router) Close() error {
	r.enabled.Store(false)
	return nil
}
