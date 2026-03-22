package crypto

import (
	"sync"
	"time"

	"github.com/multiversx/mx-chain-core-go/core"
)

// Session represents an XDP session with a peer
type Session struct {
	mu sync.RWMutex

	// PeerID is the remote peer's ID
	PeerID core.PeerID

	// SharedKey is the derived shared secret for HMAC
	SharedKey []byte

	// SendSeqNo is the next sequence number to send
	SendSeqNo uint64

	// RecvSeqNo is the last received sequence number
	RecvSeqNo uint64

	// CreatedAt is when the session was created
	CreatedAt time.Time

	// LastActivity is the last time there was activity on this session
	LastActivity time.Time

	// XDPAddress is the peer's XDP endpoint (IP:port)
	XDPAddress string

	// PublicKey is the peer's ephemeral public key for key exchange
	PublicKey []byte
}

// NewSession creates a new XDP session
func NewSession(peerID core.PeerID, sharedKey []byte, xdpAddress string) *Session {
	now := time.Now()
	return &Session{
		PeerID:       peerID,
		SharedKey:    sharedKey,
		SendSeqNo:    0,
		RecvSeqNo:    0,
		CreatedAt:    now,
		LastActivity: now,
		XDPAddress:   xdpAddress,
	}
}

// NextSendSeqNo returns the next sequence number for sending and increments the counter
func (s *Session) NextSendSeqNo() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	seqNo := s.SendSeqNo
	s.SendSeqNo++
	s.LastActivity = time.Now()
	return seqNo
}

// UpdateRecvSeqNo updates the received sequence number if it's newer
func (s *Session) UpdateRecvSeqNo(seqNo uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if seqNo > s.RecvSeqNo {
		s.RecvSeqNo = seqNo
		s.LastActivity = time.Now()
		return true
	}
	return false
}

// GetSharedKey returns the shared key (thread-safe)
func (s *Session) GetSharedKey() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := make([]byte, len(s.SharedKey))
	copy(key, s.SharedKey)
	return key
}

// RotateKey rotates the session key with a new shared key.
// Sequence numbers are intentionally NOT reset so they remain monotonically
// increasing across rotations. Resetting them would allow replayed pre-rotation
// messages to pass the sequence check after the key change.
func (s *Session) RotateKey(newKey []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.SharedKey = newKey
}

// IsExpired checks if the session is expired based on the given max age
func (s *Session) IsExpired(maxAge time.Duration) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return time.Since(s.CreatedAt) > maxAge
}

// IsIdle checks if the session is idle based on the given idle timeout
func (s *Session) IsIdle(idleTimeout time.Duration) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return time.Since(s.LastActivity) > idleTimeout
}

// Touch updates the last activity timestamp
func (s *Session) Touch() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.LastActivity = time.Now()
}

// SessionManager manages XDP sessions
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session // key: peerID string

	keyRotationInterval time.Duration
	idleTimeout         time.Duration
	stopChan            chan struct{}
}

// NewSessionManager creates a new session manager
func NewSessionManager(keyRotationInterval, idleTimeout time.Duration) *SessionManager {
	sm := &SessionManager{
		sessions:            make(map[string]*Session),
		keyRotationInterval: keyRotationInterval,
		idleTimeout:         idleTimeout,
		stopChan:            make(chan struct{}),
	}

	go sm.maintenanceLoop()

	return sm
}

// AddSession adds a new session
func (sm *SessionManager) AddSession(session *Session) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.sessions[string(session.PeerID)] = session
}

// GetSession returns a session for the given peer ID
func (sm *SessionManager) GetSession(peerID core.PeerID) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[string(peerID)]
	return session, ok
}

// RemoveSession removes a session
func (sm *SessionManager) RemoveSession(peerID core.PeerID) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	delete(sm.sessions, string(peerID))
}

// GetAllSessions returns all active sessions
func (sm *SessionManager) GetAllSessions() []*Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	sessions := make([]*Session, 0, len(sm.sessions))
	for _, s := range sm.sessions {
		sessions = append(sessions, s)
	}
	return sessions
}

// SessionCount returns the number of active sessions
func (sm *SessionManager) SessionCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return len(sm.sessions)
}

// maintenanceLoop handles session cleanup and key rotation
func (sm *SessionManager) maintenanceLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-sm.stopChan:
			return
		case <-ticker.C:
			sm.cleanup()
		}
	}
}

// cleanup removes expired and idle sessions
func (sm *SessionManager) cleanup() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	for peerID, session := range sm.sessions {
		// Remove idle sessions
		if session.IsIdle(sm.idleTimeout) {
			delete(sm.sessions, peerID)
			continue
		}

		// Note: Key rotation is handled by the capability exchange protocol
		// This just logs when rotation might be needed
		if session.IsExpired(sm.keyRotationInterval) {
			// Mark for rotation - the actual rotation happens via capability re-exchange
		}
	}
}

// Close stops the session manager
func (sm *SessionManager) Close() error {
	close(sm.stopChan)
	return nil
}
