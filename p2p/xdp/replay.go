package xdp

import (
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru"
)

// ReplayProtector prevents replay attacks by tracking seen messages
type ReplayProtector struct {
	mu sync.RWMutex

	// seenMessages is an LRU cache of seen message identifiers
	seenMessages *lru.Cache

	// seqNoMap tracks the last seen sequence number per peer
	seqNoMap sync.Map

	// config
	timestampTolerance time.Duration
	maxSeqNoGap        uint64
}

// NewReplayProtector creates a new replay protector
func NewReplayProtector(cacheSize int, timestampTolerance time.Duration, maxSeqNoGap uint64) (*ReplayProtector, error) {
	cache, err := lru.New(cacheSize)
	if err != nil {
		return nil, err
	}

	return &ReplayProtector{
		seenMessages:       cache,
		timestampTolerance: timestampTolerance,
		maxSeqNoGap:        maxSeqNoGap,
	}, nil
}

// messageKey creates a unique key for a message
type messageKey struct {
	peerID [32]byte
	seqNo  uint64
}

// peerSeqState tracks sequence number state for a peer
type peerSeqState struct {
	lastSeqNo uint64
	lastSeen  time.Time
}

// IsValid checks if a message is valid (not a replay)
func (rp *ReplayProtector) IsValid(peerID [32]byte, seqNo uint64, timestamp int64) error {
	// Check timestamp window
	now := time.Now().Unix()

	// Reject messages that are too old
	if timestamp < now-int64(rp.timestampTolerance.Seconds()) {
		return ErrTimestampOutOfRange
	}

	// Reject messages that are too far in the future
	if timestamp > now+int64(rp.timestampTolerance.Seconds())/3 {
		return ErrTimestampOutOfRange
	}

	// Check sequence number progression
	peerKey := string(peerID[:])
	if lastState, ok := rp.seqNoMap.Load(peerKey); ok {
		state := lastState.(*peerSeqState)

		// If the sequence number is too old, reject
		if seqNo < state.lastSeqNo {
			// Allow some gap for out-of-order delivery
			if state.lastSeqNo-seqNo > rp.maxSeqNoGap {
				return ErrSequenceNumberTooOld
			}
		}
	}

	// Check if we've seen this exact message
	key := messageKey{peerID: peerID, seqNo: seqNo}
	if rp.seenMessages.Contains(key) {
		return ErrReplayDetected
	}

	return nil
}

// RecordMessage records a message as seen
func (rp *ReplayProtector) RecordMessage(peerID [32]byte, seqNo uint64) {
	// Add to seen messages cache
	key := messageKey{peerID: peerID, seqNo: seqNo}
	rp.seenMessages.Add(key, struct{}{})

	// Update sequence number tracking
	peerKey := string(peerID[:])

	for {
		lastState, loaded := rp.seqNoMap.Load(peerKey)

		if loaded {
			// Existing peer - check if this is a newer sequence
			state := lastState.(*peerSeqState)
			if seqNo <= state.lastSeqNo {
				// Not a newer sequence, don't update
				return
			}

			// Create new state with updated sequence
			newState := &peerSeqState{
				lastSeqNo: seqNo,
				lastSeen:  time.Now(),
			}

			// Try to CAS with the actual old value
			if rp.seqNoMap.CompareAndSwap(peerKey, lastState, newState) {
				return
			}
			// CAS failed - another goroutine updated, retry the loop
			continue
		}

		// New peer - try to store atomically
		newState := &peerSeqState{
			lastSeqNo: seqNo,
			lastSeen:  time.Now(),
		}
		// Use LoadOrStore for new peers - if another goroutine stored first,
		// we'll get their value and retry the loop
		actual, loaded := rp.seqNoMap.LoadOrStore(peerKey, newState)
		if !loaded {
			// We successfully stored our new state
			return
		}
		// Another goroutine stored first, update lastState and check if we need to update
		state := actual.(*peerSeqState)
		if seqNo <= state.lastSeqNo {
			// Their sequence is already higher or equal, we're done
			return
		}
		// Our sequence is higher, retry to update it
	}
}

// ValidateAndRecord validates a message and records it if valid
func (rp *ReplayProtector) ValidateAndRecord(peerID [32]byte, seqNo uint64, timestamp int64) error {
	err := rp.IsValid(peerID, seqNo, timestamp)
	if err != nil {
		return err
	}

	rp.RecordMessage(peerID, seqNo)
	return nil
}

// GetLastSeqNo returns the last seen sequence number for a peer
func (rp *ReplayProtector) GetLastSeqNo(peerID [32]byte) (uint64, bool) {
	peerKey := string(peerID[:])
	if lastState, ok := rp.seqNoMap.Load(peerKey); ok {
		state := lastState.(*peerSeqState)
		return state.lastSeqNo, true
	}
	return 0, false
}

// ClearPeer removes all tracking data for a peer
func (rp *ReplayProtector) ClearPeer(peerID [32]byte) {
	peerKey := string(peerID[:])
	rp.seqNoMap.Delete(peerKey)

	// Note: We don't remove from seenMessages as LRU will handle eviction
	// and we want to continue rejecting old messages even after peer cleanup
}

// Stats returns replay protection statistics
type ReplayStats struct {
	CacheSize   int
	PeersTracked int
}

// GetStats returns current statistics
func (rp *ReplayProtector) GetStats() ReplayStats {
	peerCount := 0
	rp.seqNoMap.Range(func(key, value interface{}) bool {
		peerCount++
		return true
	})

	return ReplayStats{
		CacheSize:   rp.seenMessages.Len(),
		PeersTracked: peerCount,
	}
}
