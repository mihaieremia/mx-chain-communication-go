package xdp

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewReplayProtector(t *testing.T) {
	t.Parallel()

	rp, err := NewReplayProtector(1000, time.Minute, 100)

	require.NoError(t, err)
	require.NotNil(t, rp)
}

func TestNewReplayProtector_InvalidCacheSize(t *testing.T) {
	t.Parallel()

	_, err := NewReplayProtector(0, time.Minute, 100)

	require.Error(t, err)
}

func TestReplayProtector_IsValid_NewMessage(t *testing.T) {
	t.Parallel()

	rp, _ := NewReplayProtector(1000, time.Minute, 100)
	var peerID [32]byte
	copy(peerID[:], "peer-1")

	err := rp.IsValid(peerID, 1, time.Now().Unix())

	require.NoError(t, err)
}

func TestReplayProtector_IsValid_TimestampTooOld(t *testing.T) {
	t.Parallel()

	tolerance := time.Minute
	rp, _ := NewReplayProtector(1000, tolerance, 100)
	var peerID [32]byte

	// Timestamp older than tolerance
	oldTimestamp := time.Now().Add(-tolerance - time.Second).Unix()

	err := rp.IsValid(peerID, 1, oldTimestamp)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTimestampOutOfRange)
}

func TestReplayProtector_IsValid_TimestampTooFuture(t *testing.T) {
	t.Parallel()

	tolerance := time.Minute
	rp, _ := NewReplayProtector(1000, tolerance, 100)
	var peerID [32]byte

	// Timestamp too far in the future (more than tolerance/3)
	futureTimestamp := time.Now().Add(tolerance/3 + time.Minute).Unix()

	err := rp.IsValid(peerID, 1, futureTimestamp)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTimestampOutOfRange)
}

func TestReplayProtector_RecordMessage(t *testing.T) {
	t.Parallel()

	rp, _ := NewReplayProtector(1000, time.Minute, 100)
	var peerID [32]byte
	copy(peerID[:], "test-peer")

	rp.RecordMessage(peerID, 5)

	seqNo, ok := rp.GetLastSeqNo(peerID)
	require.True(t, ok)
	assert.Equal(t, uint64(5), seqNo)
}

func TestReplayProtector_ReplayDetected(t *testing.T) {
	t.Parallel()

	rp, _ := NewReplayProtector(1000, time.Minute, 100)
	var peerID [32]byte
	copy(peerID[:], "test-peer")
	timestamp := time.Now().Unix()

	// First message is valid
	err := rp.ValidateAndRecord(peerID, 1, timestamp)
	require.NoError(t, err)

	// Same message again should be rejected as replay
	err = rp.IsValid(peerID, 1, timestamp)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrReplayDetected)
}

func TestReplayProtector_SequenceNumberProgression(t *testing.T) {
	t.Parallel()

	rp, _ := NewReplayProtector(1000, time.Minute, 10) // Max gap of 10
	var peerID [32]byte
	copy(peerID[:], "test-peer")
	timestamp := time.Now().Unix()

	// Record sequence numbers in order
	for i := uint64(1); i <= 100; i++ {
		err := rp.ValidateAndRecord(peerID, i, timestamp)
		require.NoError(t, err)
	}

	// Last sequence should be 100
	seqNo, ok := rp.GetLastSeqNo(peerID)
	require.True(t, ok)
	assert.Equal(t, uint64(100), seqNo)
}

func TestReplayProtector_SequenceNumberTooOld(t *testing.T) {
	t.Parallel()

	maxGap := uint64(10)
	rp, _ := NewReplayProtector(1000, time.Minute, maxGap)
	var peerID [32]byte
	copy(peerID[:], "test-peer")
	timestamp := time.Now().Unix()

	// First record a high sequence number
	rp.RecordMessage(peerID, 100)

	// Try to validate a sequence number beyond the gap
	err := rp.IsValid(peerID, 100-maxGap-1, timestamp)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSequenceNumberTooOld)
}

func TestReplayProtector_SequenceNumberWithinGap(t *testing.T) {
	t.Parallel()

	maxGap := uint64(10)
	rp, _ := NewReplayProtector(1000, time.Minute, maxGap)
	var peerID [32]byte
	copy(peerID[:], "test-peer")
	timestamp := time.Now().Unix()

	// Record high sequence number
	rp.RecordMessage(peerID, 100)

	// Slightly old sequence within gap should be valid (if not seen)
	err := rp.IsValid(peerID, 95, timestamp)

	require.NoError(t, err)
}

func TestReplayProtector_OutOfOrderWithinGap(t *testing.T) {
	t.Parallel()

	maxGap := uint64(100)
	rp, _ := NewReplayProtector(1000, time.Minute, maxGap)
	var peerID [32]byte
	copy(peerID[:], "test-peer")
	timestamp := time.Now().Unix()

	// Simulate out-of-order delivery
	order := []uint64{1, 5, 3, 2, 4, 10, 7, 8, 6, 9}

	for _, seq := range order {
		err := rp.ValidateAndRecord(peerID, seq, timestamp)
		require.NoError(t, err, "sequence %d should be valid", seq)
	}

	// Highest should be recorded
	lastSeq, ok := rp.GetLastSeqNo(peerID)
	require.True(t, ok)
	assert.Equal(t, uint64(10), lastSeq)
}

func TestReplayProtector_MultiplePeers(t *testing.T) {
	t.Parallel()

	rp, _ := NewReplayProtector(1000, time.Minute, 100)
	timestamp := time.Now().Unix()

	// Create multiple peers
	peers := make([][32]byte, 5)
	for i := range peers {
		peers[i][0] = byte(i)
	}

	// Each peer uses different sequence numbers
	for i, peerID := range peers {
		for j := uint64(1); j <= 10; j++ {
			seq := uint64(i)*100 + j
			err := rp.ValidateAndRecord(peerID, seq, timestamp)
			require.NoError(t, err)
		}
	}

	// Verify each peer has correct last sequence
	for i, peerID := range peers {
		lastSeq, ok := rp.GetLastSeqNo(peerID)
		require.True(t, ok)
		assert.Equal(t, uint64(i)*100+10, lastSeq)
	}
}

func TestReplayProtector_ClearPeer(t *testing.T) {
	t.Parallel()

	rp, _ := NewReplayProtector(1000, time.Minute, 100)
	var peerID [32]byte
	copy(peerID[:], "test-peer")

	// Record some messages
	rp.RecordMessage(peerID, 100)

	_, ok := rp.GetLastSeqNo(peerID)
	require.True(t, ok)

	// Clear peer
	rp.ClearPeer(peerID)

	// Should no longer have sequence info
	_, ok = rp.GetLastSeqNo(peerID)
	assert.False(t, ok)
}

func TestReplayProtector_GetStats(t *testing.T) {
	t.Parallel()

	rp, _ := NewReplayProtector(1000, time.Minute, 100)
	timestamp := time.Now().Unix()

	// Initially empty
	stats := rp.GetStats()
	assert.Equal(t, 0, stats.CacheSize)
	assert.Equal(t, 0, stats.PeersTracked)

	// Add messages from multiple peers
	for i := 0; i < 5; i++ {
		var peerID [32]byte
		peerID[0] = byte(i)

		for j := uint64(1); j <= 10; j++ {
			_ = rp.ValidateAndRecord(peerID, j, timestamp)
		}
	}

	stats = rp.GetStats()
	assert.Equal(t, 50, stats.CacheSize) // 5 peers * 10 messages
	assert.Equal(t, 5, stats.PeersTracked)
}

func TestReplayProtector_LRUEviction(t *testing.T) {
	t.Parallel()

	cacheSize := 10
	rp, _ := NewReplayProtector(cacheSize, time.Minute, 100)
	var peerID [32]byte
	timestamp := time.Now().Unix()

	// Add more messages than cache size
	for i := uint64(1); i <= uint64(cacheSize)+5; i++ {
		_ = rp.ValidateAndRecord(peerID, i, timestamp)
	}

	// Cache should be at max size (LRU evicts old entries)
	stats := rp.GetStats()
	assert.Equal(t, cacheSize, stats.CacheSize)

	// Early messages should be evicted
	// Note: They might be accepted again if re-sent (LRU eviction)
	// This is a trade-off for bounded memory
}

func TestReplayProtector_ConcurrentAccess(t *testing.T) {
	rp, _ := NewReplayProtector(100000, time.Minute, 1000)
	timestamp := time.Now().Unix()

	var wg sync.WaitGroup
	numGoroutines := 10
	messagesPerGoroutine := 1000

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			var peerID [32]byte
			peerID[0] = byte(goroutineID)

			for i := uint64(1); i <= uint64(messagesPerGoroutine); i++ {
				_ = rp.ValidateAndRecord(peerID, i, timestamp)
			}
		}(g)
	}

	wg.Wait()

	// Verify all peers are tracked
	stats := rp.GetStats()
	assert.Equal(t, numGoroutines, stats.PeersTracked)
}

func TestReplayProtector_ConcurrentSamePeer(t *testing.T) {
	rp, _ := NewReplayProtector(100000, time.Minute, 1000)
	timestamp := time.Now().Unix()

	var peerID [32]byte
	copy(peerID[:], "shared-peer")

	var wg sync.WaitGroup
	numGoroutines := 10

	// Multiple goroutines trying to record for same peer
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			for i := uint64(1); i <= 100; i++ {
				// Different goroutines might send same sequence
				seq := uint64(goroutineID)*10 + i
				_ = rp.ValidateAndRecord(peerID, seq, timestamp)
			}
		}(g)
	}

	wg.Wait()

	// Should have highest sequence number
	lastSeq, ok := rp.GetLastSeqNo(peerID)
	require.True(t, ok)
	assert.Greater(t, lastSeq, uint64(0))
}

func TestReplayProtector_ValidateAndRecord(t *testing.T) {
	t.Parallel()

	rp, _ := NewReplayProtector(1000, time.Minute, 100)
	var peerID [32]byte
	timestamp := time.Now().Unix()

	// First message valid
	err := rp.ValidateAndRecord(peerID, 1, timestamp)
	require.NoError(t, err)

	// Second message valid
	err = rp.ValidateAndRecord(peerID, 2, timestamp)
	require.NoError(t, err)

	// Replay should fail
	err = rp.ValidateAndRecord(peerID, 1, timestamp)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrReplayDetected)
}

func TestReplayProtector_NewPeerSequence(t *testing.T) {
	t.Parallel()

	rp, _ := NewReplayProtector(1000, time.Minute, 100)
	var peerID [32]byte
	copy(peerID[:], "new-peer")
	timestamp := time.Now().Unix()

	// New peer with high starting sequence should be valid
	err := rp.ValidateAndRecord(peerID, 1000, timestamp)
	require.NoError(t, err)

	lastSeq, ok := rp.GetLastSeqNo(peerID)
	require.True(t, ok)
	assert.Equal(t, uint64(1000), lastSeq)
}

// Benchmarks
func BenchmarkReplayProtector_Validate(b *testing.B) {
	rp, _ := NewReplayProtector(100000, time.Minute, 1000)
	var peerID [32]byte
	timestamp := time.Now().Unix()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = rp.IsValid(peerID, uint64(i), timestamp)
	}
}

func BenchmarkReplayProtector_ValidateAndRecord(b *testing.B) {
	rp, _ := NewReplayProtector(100000, time.Minute, 1000)
	var peerID [32]byte
	timestamp := time.Now().Unix()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = rp.ValidateAndRecord(peerID, uint64(i), timestamp)
	}
}

func BenchmarkReplayProtector_ConcurrentValidate(b *testing.B) {
	rp, _ := NewReplayProtector(100000, time.Minute, 1000)
	timestamp := time.Now().Unix()

	b.RunParallel(func(pb *testing.PB) {
		var peerID [32]byte
		var seq uint64

		for pb.Next() {
			seq++
			_ = rp.ValidateAndRecord(peerID, seq, timestamp)
		}
	})
}
