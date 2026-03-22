package xdp

import (
	"testing"

	"github.com/multiversx/mx-chain-communication-go/p2p/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConnectionNotifier(t *testing.T) {
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

	notifier := NewConnectionNotifier(engine, &mockEngineLogger{})
	require.NotNil(t, notifier)
	defer notifier.Close()

	assert.False(t, notifier.IsInterfaceNil())
}

func TestConnectionNotifier_IsInterfaceNil(t *testing.T) {
	t.Parallel()

	var notifier *ConnectionNotifier = nil
	assert.True(t, notifier.IsInterfaceNil())
}

func TestConnectionNotifier_NilEngine(t *testing.T) {
	t.Parallel()

	notifier := NewConnectionNotifier(nil, &mockEngineLogger{})
	require.NotNil(t, notifier)
	defer notifier.Close()

	// Should not panic with nil engine
	notifier.Listen(nil, nil)
	notifier.ListenClose(nil, nil)
	notifier.Connected(nil, nil)
	notifier.Disconnected(nil, nil)
}

func TestConnectionNotifier_DisabledEngine(t *testing.T) {
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

	notifier := NewConnectionNotifier(engine, &mockEngineLogger{})
	require.NotNil(t, notifier)
	defer notifier.Close()

	// Should not panic with disabled engine
	notifier.Connected(nil, nil)
	notifier.Disconnected(nil, nil)
}

func TestConnectionNotifier_Close(t *testing.T) {
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

	notifier := NewConnectionNotifier(engine, &mockEngineLogger{})
	require.NotNil(t, notifier)

	// Close should not error
	err = notifier.Close()
	require.NoError(t, err)

	// Double close should be safe
	err = notifier.Close()
	require.NoError(t, err)
}

func TestConnectionNotifier_ListenMethods(t *testing.T) {
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

	notifier := NewConnectionNotifier(engine, &mockEngineLogger{})
	require.NotNil(t, notifier)
	defer notifier.Close()

	// These are no-op but should not panic
	notifier.Listen(nil, nil)
	notifier.ListenClose(nil, nil)
}

// Benchmarks

func BenchmarkConnectionNotifier_Create(b *testing.B) {
	args := EngineArgs{
		Config: config.XDPConfig{
			Enabled: false,
		},
		Logger: &mockEngineLogger{},
	}

	engine, _ := NewEngine(args)
	defer engine.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		notifier := NewConnectionNotifier(engine, &mockEngineLogger{})
		notifier.Close()
	}
}
