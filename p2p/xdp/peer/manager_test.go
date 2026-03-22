package peer

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/multiversx/mx-chain-communication-go/p2p/xdp/crypto"
	"github.com/multiversx/mx-chain-core-go/core"
	logger "github.com/multiversx/mx-chain-logger-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock logger for testing
type mockLogger struct{}

func (m *mockLogger) Trace(message string, args ...interface{})   {}
func (m *mockLogger) Debug(message string, args ...interface{})   {}
func (m *mockLogger) Info(message string, args ...interface{})    {}
func (m *mockLogger) Warn(message string, args ...interface{})    {}
func (m *mockLogger) Error(message string, args ...interface{})   {}
func (m *mockLogger) LogIfError(err error, args ...interface{})   {}
func (m *mockLogger) GetLevel() logger.LogLevel                   { return logger.LogTrace }
func (m *mockLogger) IsInterfaceNil() bool                        { return false }

// Helper to generate valid X25519 public keys for testing
func generateValidPublicKey(t testing.TB) []byte {
	ke, err := crypto.NewKeyExchange()
	require.NoError(t, err)
	return ke.GetPublicKeyBytes()
}

func TestDefaultManagerConfig(t *testing.T) {
	t.Parallel()

	config := DefaultManagerConfig()

	assert.Equal(t, 24*time.Hour, config.KeyRotationInterval)
	assert.Equal(t, 10*time.Minute, config.IdleTimeout)
	assert.Equal(t, 1000, config.MaxPeers)
}

func TestNewManager(t *testing.T) {
	t.Parallel()

	manager, err := NewManager(DefaultManagerConfig(), &mockLogger{})

	require.NoError(t, err)
	require.NotNil(t, manager)
	defer manager.Close()
}

func TestManager_RegisterPeer(t *testing.T) {
	t.Parallel()

	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})
	defer manager.Close()

	peerID := core.PeerID("test-peer-123")
	cap := &Capability{
		Supported: true,
		Version:   1,
		UDPPort:   37374,
		Features:  FeatureBasic,
		PublicKey: generateValidPublicKey(t),
	}
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 37374}

	err := manager.RegisterPeer(peerID, cap, addr)

	require.NoError(t, err)
	assert.True(t, manager.HasXDP(peerID))
}

func TestManager_RegisterPeer_Update(t *testing.T) {
	t.Parallel()

	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})
	defer manager.Close()

	peerID := core.PeerID("test-peer")
	pubKey := generateValidPublicKey(t)
	cap1 := &Capability{
		Supported: true,
		Version:   1,
		UDPPort:   37374,
		Features:  FeatureBasic,
		PublicKey: pubKey,
	}
	addr1 := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 37374}

	err := manager.RegisterPeer(peerID, cap1, addr1)
	require.NoError(t, err)

	// Update with new address
	addr2 := &net.UDPAddr{IP: net.ParseIP("192.168.1.2"), Port: 37374}
	err = manager.RegisterPeer(peerID, cap1, addr2)
	require.NoError(t, err)

	// Should still have the peer
	peer, ok := manager.GetPeer(peerID)
	require.True(t, ok)
	assert.Equal(t, addr2, peer.XDPAddress)
}

func TestManager_RegisterPeer_MaxPeers(t *testing.T) {
	t.Parallel()

	config := DefaultManagerConfig()
	config.MaxPeers = 3
	manager, _ := NewManager(config, &mockLogger{})
	defer manager.Close()

	// Register max peers
	for i := 0; i < 3; i++ {
		peerID := core.PeerID("peer-" + string(rune('A'+i)))
		cap := &Capability{
			Supported: true,
			PublicKey: generateValidPublicKey(t),
		}
		addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 37374 + i}
		err := manager.RegisterPeer(peerID, cap, addr)
		require.NoError(t, err)
	}

	// Try to register one more
	cap := &Capability{
		Supported: true,
		PublicKey: generateValidPublicKey(t),
	}
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.99"), Port: 37400}
	err := manager.RegisterPeer("extra-peer", cap, addr)

	require.Error(t, err)
}

func TestManager_UnregisterPeer(t *testing.T) {
	t.Parallel()

	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})
	defer manager.Close()

	peerID := core.PeerID("peer-to-remove")
	cap := &Capability{
		Supported: true,
		PublicKey: generateValidPublicKey(t),
	}
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 37374}

	manager.RegisterPeer(peerID, cap, addr)
	require.True(t, manager.HasXDP(peerID))

	manager.UnregisterPeer(peerID)

	assert.False(t, manager.HasXDP(peerID))
}

func TestManager_GetPeer(t *testing.T) {
	t.Parallel()

	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})
	defer manager.Close()

	peerID := core.PeerID("get-test-peer")
	cap := &Capability{
		Supported: true,
		Version:   1,
		UDPPort:   37374,
		Features:  FeatureBasic | FeatureFragments,
		PublicKey: generateValidPublicKey(t),
	}
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 37374}

	manager.RegisterPeer(peerID, cap, addr)

	peer, ok := manager.GetPeer(peerID)

	require.True(t, ok)
	assert.Equal(t, peerID, peer.PeerID)
	assert.Equal(t, addr, peer.XDPAddress)
	assert.Equal(t, cap, peer.Capability)
	assert.True(t, peer.Connected)
}

func TestManager_GetPeer_NotFound(t *testing.T) {
	t.Parallel()

	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})
	defer manager.Close()

	_, ok := manager.GetPeer("unknown-peer")

	assert.False(t, ok)
}

func TestManager_GetPeerByAddress(t *testing.T) {
	t.Parallel()

	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})
	defer manager.Close()

	peerID := core.PeerID("address-test-peer")
	cap := &Capability{
		Supported: true,
		PublicKey: generateValidPublicKey(t),
	}
	addr := &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 37374}

	manager.RegisterPeer(peerID, cap, addr)

	foundPeerID, ok := manager.GetPeerByAddress(addr.String())

	require.True(t, ok)
	assert.Equal(t, peerID, foundPeerID)
}

func TestManager_GetPeerByAddress_NotFound(t *testing.T) {
	t.Parallel()

	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})
	defer manager.Close()

	_, ok := manager.GetPeerByAddress("192.168.1.1:12345")

	assert.False(t, ok)
}

func TestManager_HasXDP(t *testing.T) {
	t.Parallel()

	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})
	defer manager.Close()

	peerID := core.PeerID("xdp-peer")
	cap := &Capability{
		Supported: true,
		PublicKey: generateValidPublicKey(t),
	}
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 37374}

	// Not registered yet
	assert.False(t, manager.HasXDP(peerID))

	manager.RegisterPeer(peerID, cap, addr)

	// Now registered
	assert.True(t, manager.HasXDP(peerID))
}

func TestManager_HasXDP_NotSupported(t *testing.T) {
	t.Parallel()

	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})
	defer manager.Close()

	peerID := core.PeerID("no-xdp-peer")
	cap := &Capability{
		Supported: false, // XDP not supported
		PublicKey: generateValidPublicKey(t),
	}
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 37374}

	manager.RegisterPeer(peerID, cap, addr)

	assert.False(t, manager.HasXDP(peerID))
}

func TestManager_GetXDPAddress(t *testing.T) {
	t.Parallel()

	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})
	defer manager.Close()

	peerID := core.PeerID("addr-test")
	cap := &Capability{
		Supported: true,
		PublicKey: generateValidPublicKey(t),
	}
	expectedAddr := &net.UDPAddr{IP: net.ParseIP("172.16.0.1"), Port: 37374}

	manager.RegisterPeer(peerID, cap, expectedAddr)

	addr, ok := manager.GetXDPAddress(peerID)

	require.True(t, ok)
	assert.Equal(t, expectedAddr, addr)
}

func TestManager_GetPublicKey(t *testing.T) {
	t.Parallel()

	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})
	defer manager.Close()

	pubKey := manager.GetPublicKey()

	assert.Len(t, pubKey, 32) // X25519 public key is 32 bytes
}

func TestManager_TouchPeer(t *testing.T) {
	t.Parallel()

	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})
	defer manager.Close()

	peerID := core.PeerID("touch-test")
	cap := &Capability{
		Supported: true,
		PublicKey: generateValidPublicKey(t),
	}
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 37374}

	manager.RegisterPeer(peerID, cap, addr)

	peer, _ := manager.GetPeer(peerID)
	oldLastSeen := peer.LastSeen

	time.Sleep(10 * time.Millisecond)
	manager.TouchPeer(peerID)

	peer, _ = manager.GetPeer(peerID)
	assert.True(t, peer.LastSeen.After(oldLastSeen))
}

func TestManager_GetXDPPeers(t *testing.T) {
	t.Parallel()

	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})
	defer manager.Close()

	// Register 5 peers
	for i := 0; i < 5; i++ {
		peerID := core.PeerID("peer-" + string(rune('A'+i)))
		cap := &Capability{
			Supported: true,
			PublicKey: generateValidPublicKey(t),
		}
		addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 37374 + i}
		manager.RegisterPeer(peerID, cap, addr)
	}

	peers := manager.GetXDPPeers()

	assert.Len(t, peers, 5)
}

func TestManager_GetConnectedCount(t *testing.T) {
	t.Parallel()

	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})
	defer manager.Close()

	assert.Equal(t, 0, manager.GetConnectedCount())

	// Register 3 peers
	for i := 0; i < 3; i++ {
		peerID := core.PeerID("peer-" + string(rune('A'+i)))
		cap := &Capability{
			Supported: true,
			PublicKey: generateValidPublicKey(t),
		}
		addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 37374 + i}
		manager.RegisterPeer(peerID, cap, addr)
	}

	assert.Equal(t, 3, manager.GetConnectedCount())
}

func TestManager_MarkDisconnected(t *testing.T) {
	t.Parallel()

	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})
	defer manager.Close()

	peerID := core.PeerID("disconnect-test")
	cap := &Capability{
		Supported: true,
		PublicKey: generateValidPublicKey(t),
	}
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 37374}

	manager.RegisterPeer(peerID, cap, addr)
	assert.True(t, manager.HasXDP(peerID))

	manager.MarkDisconnected(peerID)

	// HasXDP should be false for disconnected peers
	assert.False(t, manager.HasXDP(peerID))

	// But peer still exists
	peer, ok := manager.GetPeer(peerID)
	require.True(t, ok)
	assert.False(t, peer.Connected)
}

func TestManager_MarkConnected(t *testing.T) {
	t.Parallel()

	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})
	defer manager.Close()

	peerID := core.PeerID("reconnect-test")
	cap := &Capability{
		Supported: true,
		PublicKey: generateValidPublicKey(t),
	}
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 37374}

	manager.RegisterPeer(peerID, cap, addr)
	manager.MarkDisconnected(peerID)
	assert.False(t, manager.HasXDP(peerID))

	manager.MarkConnected(peerID)

	assert.True(t, manager.HasXDP(peerID))
}

func TestManager_ConcurrentAccess(t *testing.T) {
	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})
	defer manager.Close()

	var wg sync.WaitGroup
	numGoroutines := 10
	opsPerGoroutine := 100

	// Pre-generate public keys for each goroutine
	pubKeys := make([][]byte, numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		ke, _ := crypto.NewKeyExchange()
		pubKeys[i] = ke.GetPublicKeyBytes()
	}

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			peerID := core.PeerID("peer-" + string(rune('A'+id)))
			cap := &Capability{
				Supported: true,
				PublicKey: pubKeys[id],
			}
			addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 37374 + id}

			for i := 0; i < opsPerGoroutine; i++ {
				switch i % 6 {
				case 0:
					manager.RegisterPeer(peerID, cap, addr)
				case 1:
					manager.GetPeer(peerID)
				case 2:
					manager.HasXDP(peerID)
				case 3:
					manager.GetXDPPeers()
				case 4:
					manager.TouchPeer(peerID)
				case 5:
					manager.GetConnectedCount()
				}
			}
		}(g)
	}

	wg.Wait()
}

func TestManager_Close(t *testing.T) {
	t.Parallel()

	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})

	err := manager.Close()

	assert.NoError(t, err)
}

// Benchmarks
func BenchmarkManager_RegisterPeer(b *testing.B) {
	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})
	defer manager.Close()

	// Pre-generate keys for benchmark
	ke, _ := crypto.NewKeyExchange()
	pubKey := ke.GetPublicKeyBytes()

	cap := &Capability{
		Supported: true,
		PublicKey: pubKey,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		peerID := core.PeerID("peer-" + string(rune(i%256)))
		addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 37374}
		_ = manager.RegisterPeer(peerID, cap, addr)
	}
}

func BenchmarkManager_HasXDP(b *testing.B) {
	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})
	defer manager.Close()

	ke, _ := crypto.NewKeyExchange()
	peerID := core.PeerID("bench-peer")
	cap := &Capability{
		Supported: true,
		PublicKey: ke.GetPublicKeyBytes(),
	}
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 37374}
	manager.RegisterPeer(peerID, cap, addr)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.HasXDP(peerID)
	}
}

func BenchmarkManager_GetXDPPeers(b *testing.B) {
	manager, _ := NewManager(DefaultManagerConfig(), &mockLogger{})
	defer manager.Close()

	// Register 100 peers
	for i := 0; i < 100; i++ {
		ke, _ := crypto.NewKeyExchange()
		peerID := core.PeerID("peer-" + string(rune(i)))
		cap := &Capability{
			Supported: true,
			PublicKey: ke.GetPublicKeyBytes(),
		}
		addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 37374 + i}
		manager.RegisterPeer(peerID, cap, addr)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.GetXDPPeers()
	}
}
