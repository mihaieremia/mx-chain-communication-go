package crypto

import (
	"sync"
	"testing"
	"time"

	"github.com/multiversx/mx-chain-core-go/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSession(t *testing.T) {
	t.Parallel()

	peerID := core.PeerID("test-peer-123")
	sharedKey := []byte("shared-key-32-bytes-long-enough!")
	xdpAddress := "192.168.1.1:37374"

	session := NewSession(peerID, sharedKey, xdpAddress)

	require.NotNil(t, session)
	assert.Equal(t, peerID, session.PeerID)
	assert.Equal(t, sharedKey, session.SharedKey)
	assert.Equal(t, xdpAddress, session.XDPAddress)
	assert.Equal(t, uint64(0), session.SendSeqNo)
	assert.Equal(t, uint64(0), session.RecvSeqNo)
	assert.False(t, session.CreatedAt.IsZero())
	assert.False(t, session.LastActivity.IsZero())
}

func TestSession_NextSendSeqNo(t *testing.T) {
	t.Parallel()

	session := NewSession("peer", []byte("key"), "addr")

	seq1 := session.NextSendSeqNo()
	seq2 := session.NextSendSeqNo()
	seq3 := session.NextSendSeqNo()

	assert.Equal(t, uint64(0), seq1)
	assert.Equal(t, uint64(1), seq2)
	assert.Equal(t, uint64(2), seq3)
}

func TestSession_NextSendSeqNo_Concurrent(t *testing.T) {
	session := NewSession("peer", []byte("key"), "addr")

	var wg sync.WaitGroup
	numGoroutines := 10
	seqPerGoroutine := 100

	seqNos := make(chan uint64, numGoroutines*seqPerGoroutine)

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < seqPerGoroutine; i++ {
				seqNos <- session.NextSendSeqNo()
			}
		}()
	}

	wg.Wait()
	close(seqNos)

	// Collect all sequence numbers
	seen := make(map[uint64]bool)
	for seq := range seqNos {
		if seen[seq] {
			t.Fatalf("Duplicate sequence number: %d", seq)
		}
		seen[seq] = true
	}

	// Should have exactly numGoroutines * seqPerGoroutine unique sequence numbers
	assert.Len(t, seen, numGoroutines*seqPerGoroutine)
}

func TestSession_UpdateRecvSeqNo(t *testing.T) {
	t.Parallel()

	session := NewSession("peer", []byte("key"), "addr")

	// First update should succeed
	updated := session.UpdateRecvSeqNo(10)
	assert.True(t, updated)

	// Older sequence should fail
	updated = session.UpdateRecvSeqNo(5)
	assert.False(t, updated)

	// Same sequence should fail
	updated = session.UpdateRecvSeqNo(10)
	assert.False(t, updated)

	// Higher sequence should succeed
	updated = session.UpdateRecvSeqNo(15)
	assert.True(t, updated)
}

func TestSession_GetSharedKey(t *testing.T) {
	t.Parallel()

	originalKey := []byte("original-shared-key-32-bytes!!!!")
	session := NewSession("peer", originalKey, "addr")

	key := session.GetSharedKey()

	assert.Equal(t, originalKey, key)

	// Ensure it's a copy (modifying returned key shouldn't affect session)
	key[0] = 0xFF
	newKey := session.GetSharedKey()
	assert.NotEqual(t, byte(0xFF), newKey[0])
}

func TestSession_RotateKey(t *testing.T) {
	t.Parallel()

	originalKey := []byte("original-key")
	session := NewSession("peer", originalKey, "addr")

	// Generate some sequence numbers
	session.NextSendSeqNo() // SendSeqNo becomes 1
	session.NextSendSeqNo() // SendSeqNo becomes 2
	session.UpdateRecvSeqNo(100)

	// Rotate key
	newKey := []byte("new-rotated-key")
	session.RotateKey(newKey)

	// Verify key changed but sequence numbers are preserved (monotonically increasing).
	// Resetting sequence numbers would allow replayed pre-rotation messages to pass
	// the sequence check after the key change.
	assert.Equal(t, newKey, session.GetSharedKey())
	assert.Equal(t, uint64(2), session.SendSeqNo)
	assert.Equal(t, uint64(100), session.RecvSeqNo)
}

func TestSession_IsExpired(t *testing.T) {
	t.Parallel()

	session := NewSession("peer", []byte("key"), "addr")

	// Not expired with long max age
	assert.False(t, session.IsExpired(time.Hour))

	// Simulate old session
	session.CreatedAt = time.Now().Add(-2 * time.Hour)
	assert.True(t, session.IsExpired(time.Hour))
	assert.False(t, session.IsExpired(3*time.Hour))
}

func TestSession_IsIdle(t *testing.T) {
	t.Parallel()

	session := NewSession("peer", []byte("key"), "addr")

	// Not idle with long timeout
	assert.False(t, session.IsIdle(time.Hour))

	// Simulate idle session
	session.LastActivity = time.Now().Add(-30 * time.Minute)
	assert.True(t, session.IsIdle(15*time.Minute))
	assert.False(t, session.IsIdle(time.Hour))
}

func TestSession_Touch(t *testing.T) {
	t.Parallel()

	session := NewSession("peer", []byte("key"), "addr")

	// Set old activity time
	session.LastActivity = time.Now().Add(-time.Hour)
	oldActivity := session.LastActivity

	session.Touch()

	// LastActivity should be updated
	assert.True(t, session.LastActivity.After(oldActivity))
	assert.WithinDuration(t, time.Now(), session.LastActivity, time.Second)
}

// SessionManager tests

func TestNewSessionManager(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(24*time.Hour, time.Hour)
	defer sm.Close()

	require.NotNil(t, sm)
	assert.Equal(t, 0, sm.SessionCount())
}

func TestSessionManager_AddAndGetSession(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(24*time.Hour, time.Hour)
	defer sm.Close()

	peerID := core.PeerID("test-peer")
	session := NewSession(peerID, []byte("key"), "addr")

	sm.AddSession(session)

	retrieved, ok := sm.GetSession(peerID)
	require.True(t, ok)
	assert.Equal(t, session, retrieved)
}

func TestSessionManager_GetSession_NotFound(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(24*time.Hour, time.Hour)
	defer sm.Close()

	_, ok := sm.GetSession("unknown-peer")
	assert.False(t, ok)
}

func TestSessionManager_RemoveSession(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(24*time.Hour, time.Hour)
	defer sm.Close()

	peerID := core.PeerID("peer-to-remove")
	session := NewSession(peerID, []byte("key"), "addr")
	sm.AddSession(session)

	// Verify it exists
	_, ok := sm.GetSession(peerID)
	require.True(t, ok)

	// Remove it
	sm.RemoveSession(peerID)

	// Should no longer exist
	_, ok = sm.GetSession(peerID)
	assert.False(t, ok)
}

func TestSessionManager_GetAllSessions(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(24*time.Hour, time.Hour)
	defer sm.Close()

	// Add multiple sessions
	for i := 0; i < 5; i++ {
		peerID := core.PeerID("peer-" + string(rune('A'+i)))
		session := NewSession(peerID, []byte("key"), "addr")
		sm.AddSession(session)
	}

	sessions := sm.GetAllSessions()
	assert.Len(t, sessions, 5)
}

func TestSessionManager_SessionCount(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(24*time.Hour, time.Hour)
	defer sm.Close()

	assert.Equal(t, 0, sm.SessionCount())

	sm.AddSession(NewSession("peer1", []byte("key"), "addr"))
	assert.Equal(t, 1, sm.SessionCount())

	sm.AddSession(NewSession("peer2", []byte("key"), "addr"))
	assert.Equal(t, 2, sm.SessionCount())

	sm.RemoveSession("peer1")
	assert.Equal(t, 1, sm.SessionCount())
}

func TestSessionManager_ReplaceSession(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(24*time.Hour, time.Hour)
	defer sm.Close()

	peerID := core.PeerID("peer-to-replace")
	session1 := NewSession(peerID, []byte("key1"), "addr1")
	sm.AddSession(session1)

	// Add new session for same peer (replace)
	session2 := NewSession(peerID, []byte("key2"), "addr2")
	sm.AddSession(session2)

	// Should still have only 1 session
	assert.Equal(t, 1, sm.SessionCount())

	// Should be the new session
	retrieved, _ := sm.GetSession(peerID)
	assert.Equal(t, []byte("key2"), retrieved.GetSharedKey())
}

func TestSessionManager_ConcurrentAccess(t *testing.T) {
	sm := NewSessionManager(24*time.Hour, time.Hour)
	defer sm.Close()

	var wg sync.WaitGroup
	numGoroutines := 10
	opsPerGoroutine := 100

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				peerID := core.PeerID("peer-" + string(rune('A'+id)))

				// Mix of operations
				switch i % 4 {
				case 0:
					sm.AddSession(NewSession(peerID, []byte("key"), "addr"))
				case 1:
					sm.GetSession(peerID)
				case 2:
					sm.GetAllSessions()
				case 3:
					sm.SessionCount()
				}
			}
		}(g)
	}

	wg.Wait()
}

func TestSessionManager_Close(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(24*time.Hour, time.Hour)

	err := sm.Close()
	assert.NoError(t, err)
}

// Test session manager cleanup
func TestSessionManager_CleanupIdleSessions(t *testing.T) {
	// Use short idle timeout for testing
	idleTimeout := 100 * time.Millisecond
	sm := NewSessionManager(24*time.Hour, idleTimeout)
	defer sm.Close()

	peerID := core.PeerID("idle-peer")
	session := NewSession(peerID, []byte("key"), "addr")
	sm.AddSession(session)

	// Mark as old activity
	session.LastActivity = time.Now().Add(-time.Hour)

	// Wait for cleanup cycle (runs every minute, but we can trigger manually)
	sm.cleanup()

	// Session should be removed due to idle
	_, ok := sm.GetSession(peerID)
	assert.False(t, ok)
}

func TestSessionManager_ActiveSessionNotCleaned(t *testing.T) {
	t.Parallel()

	idleTimeout := time.Hour
	sm := NewSessionManager(24*time.Hour, idleTimeout)
	defer sm.Close()

	peerID := core.PeerID("active-peer")
	session := NewSession(peerID, []byte("key"), "addr")
	sm.AddSession(session)

	// Trigger cleanup
	sm.cleanup()

	// Session should still exist (not idle)
	_, ok := sm.GetSession(peerID)
	assert.True(t, ok)
}

// Benchmarks
func BenchmarkSession_NextSendSeqNo(b *testing.B) {
	session := NewSession("peer", []byte("key"), "addr")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = session.NextSendSeqNo()
	}
}

func BenchmarkSession_GetSharedKey(b *testing.B) {
	session := NewSession("peer", make([]byte, 32), "addr")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = session.GetSharedKey()
	}
}

func BenchmarkSessionManager_GetSession(b *testing.B) {
	sm := NewSessionManager(24*time.Hour, time.Hour)
	defer sm.Close()

	peerID := core.PeerID("bench-peer")
	sm.AddSession(NewSession(peerID, []byte("key"), "addr"))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = sm.GetSession(peerID)
	}
}

func BenchmarkSessionManager_AddSession(b *testing.B) {
	sm := NewSessionManager(24*time.Hour, time.Hour)
	defer sm.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		peerID := core.PeerID(string(rune(i % 256)))
		sm.AddSession(NewSession(peerID, []byte("key"), "addr"))
	}
}

func BenchmarkSessionManager_ConcurrentGetSession(b *testing.B) {
	sm := NewSessionManager(24*time.Hour, time.Hour)
	defer sm.Close()

	peerID := core.PeerID("bench-peer")
	sm.AddSession(NewSession(peerID, []byte("key"), "addr"))

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = sm.GetSession(peerID)
		}
	})
}
