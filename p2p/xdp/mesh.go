package xdp

import (
	"sync"

	"github.com/multiversx/mx-chain-communication-go/p2p"
	"github.com/multiversx/mx-chain-communication-go/p2p/xdp/peer"
	"github.com/multiversx/mx-chain-core-go/core"
)

const (
	// DefaultMeshDegree is the desired number of peers in each topic mesh
	DefaultMeshDegree = 6

	// DefaultMeshDegreeLow is the low watermark for mesh degree
	DefaultMeshDegreeLow = 4

	// DefaultMeshDegreeHigh is the high watermark for mesh degree
	DefaultMeshDegreeHigh = 12
)

// MeshConfig holds mesh configuration
type MeshConfig struct {
	D    int // Desired mesh degree
	Dlo  int // Low watermark
	Dhi  int // High watermark
}

// DefaultMeshConfig returns default mesh configuration
func DefaultMeshConfig() MeshConfig {
	return MeshConfig{
		D:   DefaultMeshDegree,
		Dlo: DefaultMeshDegreeLow,
		Dhi: DefaultMeshDegreeHigh,
	}
}

// Mesh manages topic meshes for XDP broadcast
type Mesh struct {
	mu sync.RWMutex

	// meshes maps topic to list of peer IDs
	meshes map[string][]core.PeerID

	// subscriptions maps topic to whether we're subscribed
	subscriptions map[string]bool

	// peerManager for checking peer XDP capability
	peerManager *peer.Manager

	config MeshConfig
	log    p2p.Logger
}

// NewMesh creates a new mesh manager
func NewMesh(peerManager *peer.Manager, config MeshConfig, log p2p.Logger) *Mesh {
	return &Mesh{
		meshes:        make(map[string][]core.PeerID),
		subscriptions: make(map[string]bool),
		peerManager:   peerManager,
		config:        config,
		log:           log,
	}
}

// Subscribe subscribes to a topic
func (m *Mesh) Subscribe(topic string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.subscriptions[topic] = true
	if _, ok := m.meshes[topic]; !ok {
		m.meshes[topic] = make([]core.PeerID, 0)
	}
}

// Unsubscribe unsubscribes from a topic
func (m *Mesh) Unsubscribe(topic string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.subscriptions, topic)
	delete(m.meshes, topic)
}

// IsSubscribed returns true if subscribed to a topic
func (m *Mesh) IsSubscribed(topic string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.subscriptions[topic]
}

// AddPeer adds a peer to a topic's mesh
func (m *Mesh) AddPeer(topic string, peerID core.PeerID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if topic exists
	if !m.subscriptions[topic] {
		return false
	}

	// Check if already in mesh
	mesh := m.meshes[topic]
	for _, p := range mesh {
		if p == peerID {
			return false
		}
	}

	// Check high watermark
	if len(mesh) >= m.config.Dhi {
		return false
	}

	// Add to mesh
	m.meshes[topic] = append(mesh, peerID)

	m.log.Trace("added peer to mesh", "topic", topic, "peer", peerID.Pretty())
	return true
}

// RemovePeer removes a peer from a topic's mesh
func (m *Mesh) RemovePeer(topic string, peerID core.PeerID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	mesh, ok := m.meshes[topic]
	if !ok {
		return false
	}

	// Find and remove peer
	for i, p := range mesh {
		if p == peerID {
			m.meshes[topic] = append(mesh[:i], mesh[i+1:]...)
			m.log.Trace("removed peer from mesh", "topic", topic, "peer", peerID.Pretty())
			return true
		}
	}

	return false
}

// GetMeshPeers returns the peers in a topic's mesh
func (m *Mesh) GetMeshPeers(topic string) []core.PeerID {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mesh, ok := m.meshes[topic]
	if !ok {
		return nil
	}

	// Return a copy
	result := make([]core.PeerID, len(mesh))
	copy(result, mesh)
	return result
}

// GetMeshSize returns the size of a topic's mesh
func (m *Mesh) GetMeshSize(topic string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.meshes[topic])
}

// UpdateMesh updates the mesh for a topic based on connected peers
func (m *Mesh) UpdateMesh(topic string, connectedPeers []core.PeerID) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.subscriptions[topic] {
		return
	}

	mesh := m.meshes[topic]

	// Filter to only XDP-capable and still connected peers
	validPeers := make([]core.PeerID, 0, len(mesh))
	for _, p := range mesh {
		if m.peerManager.HasXDP(p) && contains(connectedPeers, p) {
			validPeers = append(validPeers, p)
		}
	}
	mesh = validPeers

	// Prune if too many
	if len(mesh) > m.config.Dhi {
		mesh = mesh[:m.config.D]
	}

	// Graft if too few
	if len(mesh) < m.config.Dlo {
		// Find candidates not in mesh
		candidates := make([]core.PeerID, 0)
		for _, p := range connectedPeers {
			if m.peerManager.HasXDP(p) && !contains(mesh, p) {
				candidates = append(candidates, p)
			}
		}

		// Add candidates until we reach D
		needed := m.config.D - len(mesh)
		for i := 0; i < needed && i < len(candidates); i++ {
			mesh = append(mesh, candidates[i])
		}
	}

	m.meshes[topic] = mesh
}

// RemovePeerFromAllMeshes removes a peer from all meshes
func (m *Mesh) RemovePeerFromAllMeshes(peerID core.PeerID) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for topic, mesh := range m.meshes {
		newMesh := make([]core.PeerID, 0, len(mesh))
		for _, p := range mesh {
			if p != peerID {
				newMesh = append(newMesh, p)
			}
		}
		m.meshes[topic] = newMesh
	}
}

// GetAllTopics returns all subscribed topics
func (m *Mesh) GetAllTopics() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	topics := make([]string, 0, len(m.subscriptions))
	for topic := range m.subscriptions {
		topics = append(topics, topic)
	}
	return topics
}

// GetStats returns mesh statistics
func (m *Mesh) GetStats() MeshStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := MeshStats{
		TopicCount:     len(m.subscriptions),
		TopicMeshSizes: make(map[string]int),
	}

	totalPeers := 0
	for topic, mesh := range m.meshes {
		stats.TopicMeshSizes[topic] = len(mesh)
		totalPeers += len(mesh)
	}

	if stats.TopicCount > 0 {
		stats.AverageMeshSize = float64(totalPeers) / float64(stats.TopicCount)
	}

	return stats
}

// MeshStats contains mesh statistics
type MeshStats struct {
	TopicCount      int
	AverageMeshSize float64
	TopicMeshSizes  map[string]int
}

// contains checks if a slice contains a value
func contains(slice []core.PeerID, val core.PeerID) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}
