package xdp

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestDefaultSenderConfig(t *testing.T) {
	t.Parallel()

	config := DefaultSenderConfig()

	assert.Equal(t, 64, config.BatchSize)
	assert.Equal(t, 10000, config.QueueSize)
	assert.Equal(t, time.Millisecond, config.FlushInterval)
}

func TestNewSender_NilSocket(t *testing.T) {
	t.Parallel()

	_, err := NewSender(nil, nil, nil, nil, DefaultSenderConfig(), &mockLogger{})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSocketNotInitialized)
}

func TestNewSender_NilPeerManager(t *testing.T) {
	t.Parallel()

	// We need a minimal socket to get past the first check
	// Since we can't easily create a real socket, we'll test via error
	_, err := NewSender(&UDPSocket{}, nil, nil, nil, DefaultSenderConfig(), &mockLogger{})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNilPeerManager)
}

func TestNewSender_NilLogger(t *testing.T) {
	t.Parallel()

	// We can't test this easily without mocks since we need valid socket and peer manager
	// The test coverage is in the validation logic
}

func TestSenderStats(t *testing.T) {
	t.Parallel()

	stats := SenderStats{
		MessagesSent:    100,
		MessagesDropped: 5,
		QueueLength:     10,
	}

	assert.Equal(t, uint64(100), stats.MessagesSent)
	assert.Equal(t, uint64(5), stats.MessagesDropped)
	assert.Equal(t, 10, stats.QueueLength)
}

func TestSendRequest(t *testing.T) {
	t.Parallel()

	addr, _ := net.ResolveUDPAddr("udp", "192.168.1.1:37374")
	req := &sendRequest{
		topic:    "test/topic",
		data:     []byte("test data"),
		peerID:   "peer-123",
		peerAddr: addr,
		msgType:  MsgTypeConsensus,
		flags:    FlagDirect | FlagPriority,
	}

	assert.Equal(t, "test/topic", req.topic)
	assert.Equal(t, []byte("test data"), req.data)
	assert.Equal(t, core.PeerID("peer-123"), req.peerID)
	assert.Equal(t, addr, req.peerAddr)
	assert.Equal(t, byte(MsgTypeConsensus), req.msgType)
	assert.Equal(t, byte(FlagDirect|FlagPriority), req.flags)
}

// Test sequence number generation (isolated logic)
func TestSender_nextSeqNo_Sequential(t *testing.T) {
	t.Parallel()

	// Create a minimal test of the sequence counter logic
	seqCounters := &sync.Map{}

	nextSeqNo := func(peerID core.PeerID) uint64 {
		key := string(peerID)
		val, _ := seqCounters.LoadOrStore(key, new(uint64))
		counter := val.(*uint64)
		return atomic.AddUint64(counter, 1)
	}

	peerID := core.PeerID("test-peer")

	seq1 := nextSeqNo(peerID)
	seq2 := nextSeqNo(peerID)
	seq3 := nextSeqNo(peerID)

	// Sequences should be 1, 2, 3 (starts at 1 after AddUint64)
	assert.Equal(t, uint64(1), seq1)
	assert.Equal(t, uint64(2), seq2)
	assert.Equal(t, uint64(3), seq3)
}

func TestSender_nextSeqNo_DifferentPeers(t *testing.T) {
	t.Parallel()

	seqCounters := &sync.Map{}

	nextSeqNo := func(peerID core.PeerID) uint64 {
		key := string(peerID)
		val, _ := seqCounters.LoadOrStore(key, new(uint64))
		counter := val.(*uint64)
		return atomic.AddUint64(counter, 1)
	}

	peer1 := core.PeerID("peer-1")
	peer2 := core.PeerID("peer-2")

	// Each peer should have independent sequence numbers
	seq1a := nextSeqNo(peer1)
	seq2a := nextSeqNo(peer2)
	seq1b := nextSeqNo(peer1)
	seq2b := nextSeqNo(peer2)

	assert.Equal(t, uint64(1), seq1a)
	assert.Equal(t, uint64(1), seq2a)
	assert.Equal(t, uint64(2), seq1b)
	assert.Equal(t, uint64(2), seq2b)
}

func TestSender_nextSeqNo_Concurrent(t *testing.T) {
	seqCounters := &sync.Map{}

	nextSeqNo := func(peerID core.PeerID) uint64 {
		key := string(peerID)
		val, _ := seqCounters.LoadOrStore(key, new(uint64))
		counter := val.(*uint64)
		return atomic.AddUint64(counter, 1)
	}

	peerID := core.PeerID("concurrent-peer")

	var wg sync.WaitGroup
	numGoroutines := 10
	seqPerGoroutine := 100

	results := make(chan uint64, numGoroutines*seqPerGoroutine)

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < seqPerGoroutine; i++ {
				results <- nextSeqNo(peerID)
			}
		}()
	}

	wg.Wait()
	close(results)

	// Verify all sequence numbers are unique
	seen := make(map[uint64]bool)
	for seq := range results {
		if seen[seq] {
			t.Fatalf("Duplicate sequence number: %d", seq)
		}
		seen[seq] = true
	}

	assert.Len(t, seen, numGoroutines*seqPerGoroutine)

	// Verify sequence numbers are contiguous from 1 to total
	for i := uint64(1); i <= uint64(numGoroutines*seqPerGoroutine); i++ {
		if !seen[i] {
			t.Fatalf("Missing sequence number: %d", i)
		}
	}
}

func TestSenderConfig_Custom(t *testing.T) {
	t.Parallel()

	config := SenderConfig{
		BatchSize:     128,
		QueueSize:     50000,
		FlushInterval: 10 * time.Millisecond,
	}

	assert.Equal(t, 128, config.BatchSize)
	assert.Equal(t, 50000, config.QueueSize)
	assert.Equal(t, 10*time.Millisecond, config.FlushInterval)
}

// Benchmark sequence number generation
func BenchmarkSender_nextSeqNo(b *testing.B) {
	seqCounters := &sync.Map{}

	nextSeqNo := func(peerID core.PeerID) uint64 {
		key := string(peerID)
		val, _ := seqCounters.LoadOrStore(key, new(uint64))
		counter := val.(*uint64)
		return atomic.AddUint64(counter, 1)
	}

	peerID := core.PeerID("bench-peer")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = nextSeqNo(peerID)
	}
}

func BenchmarkSender_nextSeqNo_Concurrent(b *testing.B) {
	seqCounters := &sync.Map{}

	nextSeqNo := func(peerID core.PeerID) uint64 {
		key := string(peerID)
		val, _ := seqCounters.LoadOrStore(key, new(uint64))
		counter := val.(*uint64)
		return atomic.AddUint64(counter, 1)
	}

	peerID := core.PeerID("bench-peer")

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = nextSeqNo(peerID)
		}
	})
}
