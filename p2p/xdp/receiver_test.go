package xdp

import (
	"testing"

	"github.com/multiversx/mx-chain-core-go/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultReceiverConfig(t *testing.T) {
	t.Parallel()

	config := DefaultReceiverConfig()

	assert.Equal(t, 4, config.NumWorkers)
	assert.Equal(t, 10000, config.QueueSize)
	assert.Equal(t, 65536, config.BufferSize)
}

func TestReceiverConfig_Custom(t *testing.T) {
	t.Parallel()

	config := ReceiverConfig{
		NumWorkers: 8,
		QueueSize:  50000,
		BufferSize: 131072,
	}

	assert.Equal(t, 8, config.NumWorkers)
	assert.Equal(t, 50000, config.QueueSize)
	assert.Equal(t, 131072, config.BufferSize)
}

func TestNewReceiver_NilSocket(t *testing.T) {
	t.Parallel()

	_, err := NewReceiver(nil, nil, nil, nil, nil, DefaultReceiverConfig(), &mockLogger{})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSocketNotInitialized)
}

func TestNewReceiver_NilPeerManager(t *testing.T) {
	t.Parallel()

	_, err := NewReceiver(&UDPSocket{}, nil, nil, nil, nil, DefaultReceiverConfig(), &mockLogger{})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNilPeerManager)
}

func TestReceiverStats(t *testing.T) {
	t.Parallel()

	stats := ReceiverStats{
		MessagesReceived: 1000,
		MessagesDropped:  10,
		AuthFailures:     5,
		ReplayBlocked:    3,
		QueueLength:      50,
		FragmentsPending: 2,
	}

	assert.Equal(t, uint64(1000), stats.MessagesReceived)
	assert.Equal(t, uint64(10), stats.MessagesDropped)
	assert.Equal(t, uint64(5), stats.AuthFailures)
	assert.Equal(t, uint64(3), stats.ReplayBlocked)
	assert.Equal(t, 50, stats.QueueLength)
	assert.Equal(t, 2, stats.FragmentsPending)
}

func TestReceivedPacket(t *testing.T) {
	t.Parallel()

	data := []byte("test packet data")
	pkt := &receivedPacket{
		data: data,
		addr: nil,
	}

	assert.Equal(t, data, pkt.data)
	assert.Nil(t, pkt.addr)
}

func TestMessageHandler_Type(t *testing.T) {
	t.Parallel()

	// Test that a function matching MessageHandler signature compiles
	var handler MessageHandler = func(topic string, data []byte, peerID core.PeerID) {
		// No-op for type check
	}

	assert.NotNil(t, handler)
}

// Benchmarks
func BenchmarkReceiverConfig(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = DefaultReceiverConfig()
	}
}
