package xdp

import (
	"context"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/multiformats/go-multiaddr"
	"github.com/multiversx/mx-chain-communication-go/p2p"
	"github.com/multiversx/mx-chain-core-go/core"
)

const (
	// capabilityExchangeTimeout is the timeout for capability exchange with a peer
	capabilityExchangeTimeout = 10 * time.Second

	// maxConcurrentExchanges limits the number of concurrent capability exchanges
	maxConcurrentExchanges = 10
)

// ConnectionNotifier handles libp2p connection events and triggers XDP capability exchange
type ConnectionNotifier struct {
	engine *Engine
	log    p2p.Logger

	// Track ongoing exchanges to avoid duplicates
	mu              sync.Mutex
	pendingPeers    map[core.PeerID]struct{}
	exchangeSem     chan struct{} // Semaphore for limiting concurrent exchanges
	ctx             context.Context
	cancelFunc      context.CancelFunc
}

// NewConnectionNotifier creates a new connection notifier for XDP capability exchange
func NewConnectionNotifier(engine *Engine, log p2p.Logger) *ConnectionNotifier {
	ctx, cancel := context.WithCancel(context.Background())

	return &ConnectionNotifier{
		engine:       engine,
		log:          log,
		pendingPeers: make(map[core.PeerID]struct{}),
		exchangeSem:  make(chan struct{}, maxConcurrentExchanges),
		ctx:          ctx,
		cancelFunc:   cancel,
	}
}

// Listen is called when network starts listening on an addr
func (cn *ConnectionNotifier) Listen(network.Network, multiaddr.Multiaddr) {}

// ListenClose is called when network stops listening on an addr
func (cn *ConnectionNotifier) ListenClose(network.Network, multiaddr.Multiaddr) {}

// Connected is called when a connection is opened
func (cn *ConnectionNotifier) Connected(_ network.Network, conn network.Conn) {
	if cn.engine == nil || !cn.engine.IsEnabled() {
		return
	}

	peerID := core.PeerID(conn.RemotePeer())

	// Check if we're already exchanging with this peer
	cn.mu.Lock()
	if _, exists := cn.pendingPeers[peerID]; exists {
		cn.mu.Unlock()
		return
	}
	// Check if we already have XDP capability for this peer
	if cn.engine.HasXDP(peerID) {
		cn.mu.Unlock()
		return
	}
	cn.pendingPeers[peerID] = struct{}{}
	cn.mu.Unlock()

	// Trigger capability exchange asynchronously
	go cn.exchangeCapability(peerID)
}

// Disconnected is called when a connection is closed
func (cn *ConnectionNotifier) Disconnected(_ network.Network, conn network.Conn) {
	if cn.engine == nil || !cn.engine.IsEnabled() {
		return
	}

	peerID := core.PeerID(conn.RemotePeer())

	// Remove from pending if present
	cn.mu.Lock()
	delete(cn.pendingPeers, peerID)
	cn.mu.Unlock()

	// Note: We don't remove XDP capability on disconnect because:
	// 1. The peer might reconnect soon
	// 2. The peer manager has its own cleanup logic with TTLs
	cn.log.Trace("peer disconnected", "peerID", peerID.Pretty())
}

// exchangeCapability performs the capability exchange with a peer
func (cn *ConnectionNotifier) exchangeCapability(peerID core.PeerID) {
	defer func() {
		if r := recover(); r != nil {
			cn.log.Warn("panic recovered in exchangeCapability",
				"peerID", peerID.Pretty(),
				"panic", r,
			)
		}
	}()

	// Acquire semaphore to limit concurrent exchanges
	select {
	case cn.exchangeSem <- struct{}{}:
		defer func() { <-cn.exchangeSem }()
	case <-cn.ctx.Done():
		cn.removePending(peerID)
		return
	}

	defer cn.removePending(peerID)

	ctx, cancel := context.WithTimeout(cn.ctx, capabilityExchangeTimeout)
	defer cancel()

	cn.log.Trace("exchanging XDP capability with peer", "peerID", peerID.Pretty())

	err := cn.engine.ExchangeCapability(ctx, peerID)
	if err != nil {
		// This is normal - many peers won't support XDP
		cn.log.Trace("XDP capability exchange failed",
			"peerID", peerID.Pretty(),
			"error", err,
		)
		return
	}

	cn.log.Debug("XDP capability exchanged successfully", "peerID", peerID.Pretty())
}

// removePending removes a peer from the pending set
func (cn *ConnectionNotifier) removePending(peerID core.PeerID) {
	cn.mu.Lock()
	delete(cn.pendingPeers, peerID)
	cn.mu.Unlock()
}

// Close stops the connection notifier
func (cn *ConnectionNotifier) Close() error {
	cn.cancelFunc()
	return nil
}

// IsInterfaceNil returns true if there is no value under the interface
func (cn *ConnectionNotifier) IsInterfaceNil() bool {
	return cn == nil
}
