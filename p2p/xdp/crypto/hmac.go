package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"errors"

	"github.com/multiversx/mx-chain-core-go/core"
)

const (
	// HMACSize is the size of the HMAC-SHA256 output
	HMACSize = 32
)

// ComputeHMAC computes HMAC-SHA256 for the given data using the provided key
func ComputeHMAC(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// VerifyHMAC verifies the HMAC using constant-time comparison
func VerifyHMAC(key, data, expectedMAC []byte) bool {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	computedMAC := mac.Sum(nil)

	// Use constant-time comparison to prevent timing attacks
	return subtle.ConstantTimeCompare(computedMAC, expectedMAC) == 1
}

// SignPacket signs the packet data and returns the HMAC
func SignPacket(key, packetData []byte) [32]byte {
	var result [32]byte
	mac := ComputeHMAC(key, packetData)
	copy(result[:], mac)
	return result
}

// VerifyPacket verifies the packet HMAC
func VerifyPacket(key, packetData []byte, expectedHMAC [32]byte) bool {
	return VerifyHMAC(key, packetData, expectedHMAC[:])
}

// ErrNoSession is returned when no session exists for a peer
var ErrNoSession = errors.New("no session found for peer")

// Authenticator provides message authentication services
type Authenticator struct {
	sessionManager *SessionManager
}

// NewAuthenticator creates a new authenticator
func NewAuthenticator(sessionManager *SessionManager) *Authenticator {
	return &Authenticator{
		sessionManager: sessionManager,
	}
}

// SignWithPeerID signs a packet for a specific peer using their session key
// This is the preferred method as it uses O(1) session lookup
func (a *Authenticator) SignWithPeerID(peerID core.PeerID, packetData []byte) ([32]byte, error) {
	var result [32]byte

	session, ok := a.sessionManager.GetSession(peerID)
	if !ok {
		return result, ErrNoSession
	}

	key := session.GetSharedKey()
	if len(key) == 0 {
		return result, ErrNoSession
	}

	return SignPacket(key, packetData), nil
}

// VerifyWithPeerID verifies a packet HMAC from a specific peer
// This is the preferred method as it uses O(1) session lookup
func (a *Authenticator) VerifyWithPeerID(peerID core.PeerID, packetData []byte, expectedHMAC [32]byte) bool {
	session, ok := a.sessionManager.GetSession(peerID)
	if !ok {
		return false
	}

	key := session.GetSharedKey()
	if len(key) == 0 {
		return false
	}

	return VerifyPacket(key, packetData, expectedHMAC)
}

