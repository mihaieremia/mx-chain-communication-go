package peer

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/multiversx/mx-chain-communication-go/p2p"
	"github.com/multiversx/mx-chain-communication-go/p2p/xdp/crypto"
	"github.com/multiversx/mx-chain-core-go/core"
)

// XDPPeer represents a peer with XDP capability
type XDPPeer struct {
	PeerID     core.PeerID
	XDPAddress *net.UDPAddr
	Session    *crypto.Session
	Capability *Capability
	Connected  bool
	LastSeen   time.Time
}

// Manager manages XDP-capable peers
type Manager struct {
	mu sync.RWMutex

	// peers maps peer ID to XDP peer info
	peers map[string]*XDPPeer

	// addressIndex maps UDP address to peer ID for fast lookup on receive
	addressIndex map[string]core.PeerID

	// sessionManager handles crypto sessions
	sessionManager *crypto.SessionManager

	// keyExchange for deriving shared keys
	keyExchange *crypto.KeyExchange

	// config
	config ManagerConfig

	// logger
	log p2p.Logger
}

// ManagerConfig holds configuration for the peer manager
type ManagerConfig struct {
	KeyRotationInterval time.Duration
	IdleTimeout         time.Duration
	MaxPeers            int
}

// DefaultManagerConfig returns default manager configuration
func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		KeyRotationInterval: 24 * time.Hour,
		IdleTimeout:         10 * time.Minute,
		MaxPeers:            1000,
	}
}

// NewManager creates a new XDP peer manager
func NewManager(config ManagerConfig, log p2p.Logger) (*Manager, error) {
	keyExchange, err := crypto.NewKeyExchange()
	if err != nil {
		return nil, fmt.Errorf("failed to create key exchange: %w", err)
	}

	sessionManager := crypto.NewSessionManager(config.KeyRotationInterval, config.IdleTimeout)

	return &Manager{
		peers:          make(map[string]*XDPPeer),
		addressIndex:   make(map[string]core.PeerID),
		sessionManager: sessionManager,
		keyExchange:    keyExchange,
		config:         config,
		log:            log,
	}, nil
}

// RegisterPeer registers a new XDP-capable peer
func (m *Manager) RegisterPeer(peerID core.PeerID, capability *Capability, xdpAddr *net.UDPAddr) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	peerKey := string(peerID)

	// Check if already registered
	if existing, ok := m.peers[peerKey]; ok {
		// Update existing peer
		existing.XDPAddress = xdpAddr
		existing.Capability = capability
		existing.LastSeen = time.Now()
		existing.Connected = true

		// Update address index
		m.addressIndex[xdpAddr.String()] = peerID
		return nil
	}

	// Check max peers
	if len(m.peers) >= m.config.MaxPeers {
		return fmt.Errorf("maximum number of XDP peers reached: %d", m.config.MaxPeers)
	}

	// Derive shared key
	sharedKey, err := m.keyExchange.DeriveSharedKey(capability.PublicKey, nil)
	if err != nil {
		return fmt.Errorf("failed to derive shared key: %w", err)
	}

	// Create session
	session := crypto.NewSession(peerID, sharedKey, xdpAddr.String())
	m.sessionManager.AddSession(session)

	// Create peer entry
	peer := &XDPPeer{
		PeerID:     peerID,
		XDPAddress: xdpAddr,
		Session:    session,
		Capability: capability,
		Connected:  true,
		LastSeen:   time.Now(),
	}

	m.peers[peerKey] = peer
	m.addressIndex[xdpAddr.String()] = peerID

	m.log.Debug("registered XDP peer",
		"peerID", peerID.Pretty(),
		"xdpAddress", xdpAddr.String(),
	)

	return nil
}

// UnregisterPeer removes a peer
func (m *Manager) UnregisterPeer(peerID core.PeerID) {
	m.mu.Lock()
	defer m.mu.Unlock()

	peerKey := string(peerID)
	if peer, ok := m.peers[peerKey]; ok {
		if peer.XDPAddress != nil {
			delete(m.addressIndex, peer.XDPAddress.String())
		}
		m.sessionManager.RemoveSession(peerID)
		delete(m.peers, peerKey)

		m.log.Debug("unregistered XDP peer", "peerID", peerID.Pretty())
	}
}

// GetPeer returns peer information
func (m *Manager) GetPeer(peerID core.PeerID) (*XDPPeer, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	peer, ok := m.peers[string(peerID)]
	return peer, ok
}

// GetPeerByAddress returns peer by UDP address
func (m *Manager) GetPeerByAddress(addr string) (core.PeerID, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	peerID, ok := m.addressIndex[addr]
	return peerID, ok
}

// HasXDP checks if a peer supports XDP
func (m *Manager) HasXDP(peerID core.PeerID) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	peer, ok := m.peers[string(peerID)]
	return ok && peer.Connected && peer.Capability != nil && peer.Capability.Supported
}

// GetSession returns the session for a peer
func (m *Manager) GetSession(peerID core.PeerID) (*crypto.Session, bool) {
	return m.sessionManager.GetSession(peerID)
}

// GetXDPAddress returns the XDP address for a peer
func (m *Manager) GetXDPAddress(peerID core.PeerID) (*net.UDPAddr, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	peer, ok := m.peers[string(peerID)]
	if !ok {
		return nil, false
	}
	return peer.XDPAddress, true
}

// GetPublicKey returns our public key for capability exchange
func (m *Manager) GetPublicKey() []byte {
	return m.keyExchange.GetPublicKeyBytes()
}

// TouchPeer updates the last seen time for a peer
func (m *Manager) TouchPeer(peerID core.PeerID) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if peer, ok := m.peers[string(peerID)]; ok {
		peer.LastSeen = time.Now()
		if peer.Session != nil {
			peer.Session.Touch()
		}
	}
}

// GetXDPPeers returns all XDP-capable peers
func (m *Manager) GetXDPPeers() []core.PeerID {
	m.mu.RLock()
	defer m.mu.RUnlock()

	peers := make([]core.PeerID, 0, len(m.peers))
	for _, peer := range m.peers {
		if peer.Connected {
			peers = append(peers, peer.PeerID)
		}
	}
	return peers
}

// GetConnectedCount returns the number of connected XDP peers
func (m *Manager) GetConnectedCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, peer := range m.peers {
		if peer.Connected {
			count++
		}
	}
	return count
}

// MarkDisconnected marks a peer as disconnected
func (m *Manager) MarkDisconnected(peerID core.PeerID) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if peer, ok := m.peers[string(peerID)]; ok {
		peer.Connected = false
	}
}

// MarkConnected marks a peer as connected
func (m *Manager) MarkConnected(peerID core.PeerID) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if peer, ok := m.peers[string(peerID)]; ok {
		peer.Connected = true
		peer.LastSeen = time.Now()
	}
}

// Close closes the manager
func (m *Manager) Close() error {
	return m.sessionManager.Close()
}
