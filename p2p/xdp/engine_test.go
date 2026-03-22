package xdp

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multiversx/mx-chain-communication-go/p2p/config"
	"github.com/multiversx/mx-chain-core-go/core"
	logger "github.com/multiversx/mx-chain-logger-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Port counter for unique ports in parallel tests
var testPort uint32 = 40000

func getNextTestPort() uint16 {
	return uint16(atomic.AddUint32(&testPort, 1))
}

// mockLogger for testing
type mockEngineLogger struct{}

func (m *mockEngineLogger) Trace(message string, args ...interface{})   {}
func (m *mockEngineLogger) Debug(message string, args ...interface{})   {}
func (m *mockEngineLogger) Info(message string, args ...interface{})    {}
func (m *mockEngineLogger) Warn(message string, args ...interface{})    {}
func (m *mockEngineLogger) Error(message string, args ...interface{})   {}
func (m *mockEngineLogger) LogIfError(err error, args ...interface{})   {}
func (m *mockEngineLogger) GetLevel() logger.LogLevel                   { return logger.LogTrace }
func (m *mockEngineLogger) IsInterfaceNil() bool                        { return false }

func TestNewEngine_NilLogger(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled: false,
		},
		Logger: nil,
	}

	_, err := NewEngine(args)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNilLogger)
}

func TestNewEngine_Disabled(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled: false,
		},
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)

	require.NoError(t, err)
	require.NotNil(t, engine)
	defer engine.Close()

	assert.False(t, engine.IsEnabled())
	assert.False(t, engine.IsRunning())
}

func TestNewEngine_Enabled(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled:   true,
			QueueSize: 1024,
			BatchSize: 32,
		},
		Port:   getNextTestPort(),
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)

	require.NoError(t, err)
	require.NotNil(t, engine)
	defer engine.Close()

	assert.True(t, engine.IsEnabled())
	assert.False(t, engine.IsRunning()) // Not started yet
}

func TestEngine_StartStop(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled:   true,
			QueueSize: 1024,
			BatchSize: 32,
		},
		Port:   getNextTestPort(),
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)
	require.NoError(t, err)
	require.NotNil(t, engine)

	// Start engine
	err = engine.Start()
	require.NoError(t, err)
	assert.True(t, engine.IsRunning())

	// Start again should be idempotent
	err = engine.Start()
	require.NoError(t, err)

	// Stop engine
	err = engine.Stop()
	require.NoError(t, err)
	assert.False(t, engine.IsRunning())

	// Stop again should be idempotent
	err = engine.Stop()
	require.NoError(t, err)
}

func TestEngine_StartStop_Disabled(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled: false,
		},
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)
	require.NoError(t, err)
	require.NotNil(t, engine)

	// Start should succeed but do nothing
	err = engine.Start()
	require.NoError(t, err)
	assert.False(t, engine.IsRunning())

	// Stop should succeed
	err = engine.Stop()
	require.NoError(t, err)
}

func TestEngine_Close(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled:   true,
			QueueSize: 1024,
			BatchSize: 32,
		},
		Port:   getNextTestPort(),
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)
	require.NoError(t, err)
	require.NotNil(t, engine)

	err = engine.Start()
	require.NoError(t, err)

	// Close should stop the engine
	err = engine.Close()
	require.NoError(t, err)
	assert.False(t, engine.IsRunning())
}

func TestEngine_Send_Disabled(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled: false,
		},
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)
	require.NoError(t, err)
	defer engine.Close()

	err = engine.Send("test-topic", []byte("test data"), core.PeerID("test-peer"))

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrXDPNotEnabled)
}

func TestEngine_Broadcast_Disabled(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled: false,
		},
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)
	require.NoError(t, err)
	defer engine.Close()

	// Should not panic
	engine.Broadcast("test-topic", []byte("test data"))
}

func TestEngine_BroadcastOnChannel_Disabled(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled: false,
		},
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)
	require.NoError(t, err)
	defer engine.Close()

	// Should not panic
	engine.BroadcastOnChannel("channel", "test-topic", []byte("test data"))
}

func TestEngine_HasXDP_Disabled(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled: false,
		},
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)
	require.NoError(t, err)
	defer engine.Close()

	assert.False(t, engine.HasXDP(core.PeerID("test-peer")))
}

func TestEngine_GetXDPPeers_Disabled(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled: false,
		},
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)
	require.NoError(t, err)
	defer engine.Close()

	assert.Nil(t, engine.GetXDPPeers())
}

func TestEngine_ExchangeCapability_Disabled(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled: false,
		},
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)
	require.NoError(t, err)
	defer engine.Close()

	err = engine.ExchangeCapability(context.Background(), core.PeerID("test-peer"))

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrXDPNotEnabled)
}

func TestEngine_GetComponents_Disabled(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled: false,
		},
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)
	require.NoError(t, err)
	defer engine.Close()

	// Components should be nil when disabled
	assert.Nil(t, engine.GetRouter())
	assert.Nil(t, engine.GetBroadcaster())
	assert.Nil(t, engine.GetSender())
	assert.Nil(t, engine.GetReceiver())
	assert.Nil(t, engine.GetPeerManager())
	assert.Nil(t, engine.GetMesh())

	// These should still exist
	assert.NotNil(t, engine.GetTopicRegistry())
	assert.NotNil(t, engine.GetMetrics())
}

func TestEngine_GetComponents_Enabled(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled:   true,
			QueueSize: 1024,
			BatchSize: 32,
		},
		Port:   getNextTestPort(),
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)
	require.NoError(t, err)
	defer engine.Close()

	// All components should exist when enabled
	assert.NotNil(t, engine.GetRouter())
	assert.NotNil(t, engine.GetBroadcaster())
	assert.NotNil(t, engine.GetSender())
	assert.NotNil(t, engine.GetReceiver())
	assert.NotNil(t, engine.GetPeerManager())
	assert.NotNil(t, engine.GetMesh())
	assert.NotNil(t, engine.GetTopicRegistry())
	assert.NotNil(t, engine.GetMetrics())
}

func TestEngine_SubscribeUnsubscribeTopic_Disabled(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled: false,
		},
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)
	require.NoError(t, err)
	defer engine.Close()

	// Should not panic
	engine.SubscribeTopic("test-topic")
	engine.UnsubscribeTopic("test-topic")
}

func TestEngine_SubscribeUnsubscribeTopic_Enabled(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled:   true,
			QueueSize: 1024,
			BatchSize: 32,
		},
		Port:   getNextTestPort(),
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)
	require.NoError(t, err)
	defer engine.Close()

	// Should not panic
	engine.SubscribeTopic("test-topic")
	engine.UnsubscribeTopic("test-topic")
}

func TestEngine_SetMessageHandler_Disabled(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled: false,
		},
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)
	require.NoError(t, err)
	defer engine.Close()

	// Should not panic
	engine.SetMessageHandler(func(topic string, data []byte, peerID core.PeerID) {})
}

func TestEngine_SetLibP2PFallbacks_Disabled(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled: false,
		},
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)
	require.NoError(t, err)
	defer engine.Close()

	// Should not panic
	engine.SetLibP2PFallbacks(nil, nil)
}

func TestEngine_ConfigDefaults(t *testing.T) {
	t.Parallel()

	port := getNextTestPort()
	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled: true,
			// All other values are zero/default
		},
		Port:   port,
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)
	require.NoError(t, err)
	defer engine.Close()

	// Check that defaults were applied
	assert.Equal(t, port, engine.config.Port)
	assert.NotZero(t, engine.config.QueueSize)
	assert.NotZero(t, engine.config.BatchSize)
	assert.NotZero(t, engine.config.Security.KeyRotationInterval)
	assert.NotZero(t, engine.config.Security.ReplayWindowSize)
	assert.NotZero(t, engine.config.Security.TimestampTolerance)
}

func TestEngine_ConfigFromXDPConfig(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled:   true,
			Interface: "eth0",
			QueueSize: 4096,
			BatchSize: 128,
			Security: config.XDPSecurityConfig{
				KeyRotationIntervalSec: 3600, // 1 hour
				ReplayWindowSize:       50000,
				TimestampToleranceSec:  120, // 2 minutes
			},
		},
		Port:   getNextTestPort(),
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)
	require.NoError(t, err)
	defer engine.Close()

	assert.Equal(t, "eth0", engine.config.Interface)
	assert.Equal(t, uint32(4096), engine.config.QueueSize)
	assert.Equal(t, uint32(128), engine.config.BatchSize)
	assert.Equal(t, time.Hour, engine.config.Security.KeyRotationInterval)
	assert.Equal(t, 50000, engine.config.Security.ReplayWindowSize)
	assert.Equal(t, 2*time.Minute, engine.config.Security.TimestampTolerance)
}

func TestEngine_HasXDP_Enabled(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled:   true,
			QueueSize: 1024,
			BatchSize: 32,
		},
		Port:   getNextTestPort(),
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)
	require.NoError(t, err)
	defer engine.Close()

	// No peers registered yet
	assert.False(t, engine.HasXDP(core.PeerID("unknown-peer")))
}

func TestEngine_GetXDPPeers_Enabled(t *testing.T) {
	t.Parallel()

	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled:   true,
			QueueSize: 1024,
			BatchSize: 32,
		},
		Port:   getNextTestPort(),
		Logger: &mockEngineLogger{},
	}

	engine, err := NewEngine(args)
	require.NoError(t, err)
	defer engine.Close()

	// No peers registered yet
	peers := engine.GetXDPPeers()
	assert.Empty(t, peers)
}

// Benchmarks

func BenchmarkNewEngine_Disabled(b *testing.B) {
	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled: false,
		},
		Logger: &mockEngineLogger{},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine, _ := NewEngine(args)
		engine.Close()
	}
}

func BenchmarkEngine_IsEnabled(b *testing.B) {
	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled: true,
		},
		Port:   getNextTestPort(),
		Logger: &mockEngineLogger{},
	}

	engine, _ := NewEngine(args)
	defer engine.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = engine.IsEnabled()
	}
}

func BenchmarkEngine_HasXDP(b *testing.B) {
	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled: true,
		},
		Port:   getNextTestPort(),
		Logger: &mockEngineLogger{},
	}

	engine, _ := NewEngine(args)
	defer engine.Close()

	peerID := core.PeerID("test-peer")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = engine.HasXDP(peerID)
	}
}
