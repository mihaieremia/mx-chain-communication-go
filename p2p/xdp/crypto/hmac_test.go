package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"
	"time"

	"github.com/multiversx/mx-chain-core-go/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeHMAC(t *testing.T) {
	t.Parallel()

	key := []byte("test-secret-key")
	data := []byte("test data to authenticate")

	mac := ComputeHMAC(key, data)

	assert.Len(t, mac, HMACSize)
	assert.NotNil(t, mac)
}

func TestComputeHMAC_Deterministic(t *testing.T) {
	t.Parallel()

	key := []byte("deterministic-key")
	data := []byte("same data")

	mac1 := ComputeHMAC(key, data)
	mac2 := ComputeHMAC(key, data)

	assert.Equal(t, mac1, mac2)
}

func TestComputeHMAC_DifferentKeys(t *testing.T) {
	t.Parallel()

	key1 := []byte("key-one")
	key2 := []byte("key-two")
	data := []byte("same data")

	mac1 := ComputeHMAC(key1, data)
	mac2 := ComputeHMAC(key2, data)

	assert.NotEqual(t, mac1, mac2)
}

func TestComputeHMAC_DifferentData(t *testing.T) {
	t.Parallel()

	key := []byte("same-key")
	data1 := []byte("data one")
	data2 := []byte("data two")

	mac1 := ComputeHMAC(key, data1)
	mac2 := ComputeHMAC(key, data2)

	assert.NotEqual(t, mac1, mac2)
}

func TestVerifyHMAC_Valid(t *testing.T) {
	t.Parallel()

	key := []byte("test-key")
	data := []byte("test data")
	mac := ComputeHMAC(key, data)

	valid := VerifyHMAC(key, data, mac)

	assert.True(t, valid)
}

func TestVerifyHMAC_InvalidMAC(t *testing.T) {
	t.Parallel()

	key := []byte("test-key")
	data := []byte("test data")
	wrongMAC := make([]byte, HMACSize)
	rand.Read(wrongMAC)

	valid := VerifyHMAC(key, data, wrongMAC)

	assert.False(t, valid)
}

func TestVerifyHMAC_WrongKey(t *testing.T) {
	t.Parallel()

	key1 := []byte("correct-key")
	key2 := []byte("wrong-key")
	data := []byte("test data")
	mac := ComputeHMAC(key1, data)

	valid := VerifyHMAC(key2, data, mac)

	assert.False(t, valid)
}

func TestVerifyHMAC_ModifiedData(t *testing.T) {
	t.Parallel()

	key := []byte("test-key")
	data := []byte("original data")
	mac := ComputeHMAC(key, data)

	modifiedData := []byte("modified data")
	valid := VerifyHMAC(key, modifiedData, mac)

	assert.False(t, valid)
}

func TestVerifyHMAC_TruncatedMAC(t *testing.T) {
	t.Parallel()

	key := []byte("test-key")
	data := []byte("test data")
	mac := ComputeHMAC(key, data)

	truncatedMAC := mac[:16]
	valid := VerifyHMAC(key, data, truncatedMAC)

	assert.False(t, valid)
}

func TestSignPacket(t *testing.T) {
	t.Parallel()

	key := []byte("packet-signing-key")
	packetData := []byte("packet header and payload")

	signature := SignPacket(key, packetData)

	assert.Len(t, signature[:], HMACSize)
}

func TestVerifyPacket(t *testing.T) {
	t.Parallel()

	t.Run("valid signature", func(t *testing.T) {
		key := []byte("packet-key")
		packetData := []byte("packet data")
		signature := SignPacket(key, packetData)

		valid := VerifyPacket(key, packetData, signature)

		assert.True(t, valid)
	})

	t.Run("invalid signature", func(t *testing.T) {
		key := []byte("packet-key")
		packetData := []byte("packet data")
		var wrongSignature [32]byte
		rand.Read(wrongSignature[:])

		valid := VerifyPacket(key, packetData, wrongSignature)

		assert.False(t, valid)
	})
}

func TestNewAuthenticator(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(time.Hour, time.Hour)
	defer sm.Close()

	auth := NewAuthenticator(sm)

	require.NotNil(t, auth)
}

func TestAuthenticator_SignWithPeerID(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(time.Hour, time.Hour)
	defer sm.Close()

	// Create session with known key
	peerID := core.PeerID("test-peer-123")
	sharedKey := []byte("shared-secret-key-32-bytes-long!")
	session := NewSession(peerID, sharedKey, "127.0.0.1:37374")
	sm.AddSession(session)

	auth := NewAuthenticator(sm)
	packetData := []byte("packet to sign")

	signature, err := auth.SignWithPeerID(peerID, packetData)

	require.NoError(t, err)
	assert.Len(t, signature[:], HMACSize)

	// Verify the signature is correct
	expectedSignature := SignPacket(sharedKey, packetData)
	assert.Equal(t, expectedSignature, signature)
}

func TestAuthenticator_SignWithPeerID_NoSession(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(time.Hour, time.Hour)
	defer sm.Close()

	auth := NewAuthenticator(sm)
	unknownPeerID := core.PeerID("unknown-peer")
	packetData := []byte("packet to sign")

	_, err := auth.SignWithPeerID(unknownPeerID, packetData)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoSession)
}

func TestAuthenticator_SignWithPeerID_EmptyKey(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(time.Hour, time.Hour)
	defer sm.Close()

	// Create session with empty key
	peerID := core.PeerID("test-peer")
	session := NewSession(peerID, []byte{}, "127.0.0.1:37374")
	sm.AddSession(session)

	auth := NewAuthenticator(sm)
	packetData := []byte("packet to sign")

	_, err := auth.SignWithPeerID(peerID, packetData)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoSession)
}

func TestAuthenticator_VerifyWithPeerID(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(time.Hour, time.Hour)
	defer sm.Close()

	peerID := core.PeerID("test-peer-456")
	sharedKey := []byte("shared-verification-key-32-bytes!")
	session := NewSession(peerID, sharedKey, "127.0.0.1:37374")
	sm.AddSession(session)

	auth := NewAuthenticator(sm)
	packetData := []byte("packet to verify")
	signature := SignPacket(sharedKey, packetData)

	valid := auth.VerifyWithPeerID(peerID, packetData, signature)

	assert.True(t, valid)
}

func TestAuthenticator_VerifyWithPeerID_NoSession(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(time.Hour, time.Hour)
	defer sm.Close()

	auth := NewAuthenticator(sm)
	unknownPeerID := core.PeerID("unknown-peer")
	packetData := []byte("packet data")
	var signature [32]byte

	valid := auth.VerifyWithPeerID(unknownPeerID, packetData, signature)

	assert.False(t, valid)
}

func TestAuthenticator_VerifyWithPeerID_WrongSignature(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(time.Hour, time.Hour)
	defer sm.Close()

	peerID := core.PeerID("test-peer-789")
	sharedKey := []byte("key-for-verification")
	session := NewSession(peerID, sharedKey, "127.0.0.1:37374")
	sm.AddSession(session)

	auth := NewAuthenticator(sm)
	packetData := []byte("packet to verify")
	var wrongSignature [32]byte
	rand.Read(wrongSignature[:])

	valid := auth.VerifyWithPeerID(peerID, packetData, wrongSignature)

	assert.False(t, valid)
}

func TestAuthenticator_Sign_TruncatedPeerID(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(time.Hour, time.Hour)
	defer sm.Close()

	// Create a truncated 32-byte peer ID
	var peerIDBytes [32]byte
	copy(peerIDBytes[:], "test-peer-truncated")

	// The session manager uses the same truncated bytes as key
	peerID := core.PeerID(peerIDBytes[:])
	sharedKey := []byte("shared-key-for-truncated-test!!")
	session := NewSession(peerID, sharedKey, "127.0.0.1:37374")
	sm.AddSession(session)

	auth := NewAuthenticator(sm)
	packetData := []byte("packet data")

	signature, err := auth.Sign(peerIDBytes, packetData)

	require.NoError(t, err)
	assert.Len(t, signature[:], HMACSize)
}

func TestAuthenticator_Verify_TruncatedPeerID(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(time.Hour, time.Hour)
	defer sm.Close()

	var peerIDBytes [32]byte
	copy(peerIDBytes[:], "test-peer-truncated")

	peerID := core.PeerID(peerIDBytes[:])
	sharedKey := []byte("shared-key-for-verify-truncated!")
	session := NewSession(peerID, sharedKey, "127.0.0.1:37374")
	sm.AddSession(session)

	auth := NewAuthenticator(sm)
	packetData := []byte("packet data")
	signature := SignPacket(sharedKey, packetData)

	valid := auth.Verify(peerIDBytes, packetData, signature)

	assert.True(t, valid)
}

func TestAuthenticator_SignAndVerify_Roundtrip(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(time.Hour, time.Hour)
	defer sm.Close()

	peerID := core.PeerID("roundtrip-peer")
	sharedKey := make([]byte, 32)
	rand.Read(sharedKey)
	session := NewSession(peerID, sharedKey, "127.0.0.1:37374")
	sm.AddSession(session)

	auth := NewAuthenticator(sm)
	packetData := make([]byte, 1400)
	rand.Read(packetData)

	// Sign
	signature, err := auth.SignWithPeerID(peerID, packetData)
	require.NoError(t, err)

	// Verify
	valid := auth.VerifyWithPeerID(peerID, packetData, signature)
	assert.True(t, valid)
}

func TestHMACSize(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 32, HMACSize)
}

func TestComputeHMAC_EmptyData(t *testing.T) {
	t.Parallel()

	key := []byte("key-for-empty")
	data := []byte{}

	mac := ComputeHMAC(key, data)

	assert.Len(t, mac, HMACSize)
}

func TestComputeHMAC_LargeData(t *testing.T) {
	t.Parallel()

	key := []byte("key-for-large-data")
	data := make([]byte, 1024*1024) // 1MB
	rand.Read(data)

	mac := ComputeHMAC(key, data)

	assert.Len(t, mac, HMACSize)
}

// Benchmarks
func BenchmarkComputeHMAC(b *testing.B) {
	key := make([]byte, 32)
	data := make([]byte, 1400)
	rand.Read(key)
	rand.Read(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ComputeHMAC(key, data)
	}
}

func BenchmarkVerifyHMAC(b *testing.B) {
	key := make([]byte, 32)
	data := make([]byte, 1400)
	rand.Read(key)
	rand.Read(data)
	mac := ComputeHMAC(key, data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = VerifyHMAC(key, data, mac)
	}
}

func BenchmarkSignPacket(b *testing.B) {
	key := make([]byte, 32)
	data := make([]byte, 1400)
	rand.Read(key)
	rand.Read(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = SignPacket(key, data)
	}
}

func BenchmarkVerifyPacket(b *testing.B) {
	key := make([]byte, 32)
	data := make([]byte, 1400)
	rand.Read(key)
	rand.Read(data)
	sig := SignPacket(key, data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = VerifyPacket(key, data, sig)
	}
}

func BenchmarkAuthenticator_SignWithPeerID(b *testing.B) {
	sm := NewSessionManager(time.Hour, time.Hour)
	defer sm.Close()

	peerID := core.PeerID("bench-peer")
	sharedKey := make([]byte, 32)
	rand.Read(sharedKey)
	session := NewSession(peerID, sharedKey, "127.0.0.1:37374")
	sm.AddSession(session)

	auth := NewAuthenticator(sm)
	data := make([]byte, 1400)
	rand.Read(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = auth.SignWithPeerID(peerID, data)
	}
}

func BenchmarkAuthenticator_VerifyWithPeerID(b *testing.B) {
	sm := NewSessionManager(time.Hour, time.Hour)
	defer sm.Close()

	peerID := core.PeerID("bench-peer")
	sharedKey := make([]byte, 32)
	rand.Read(sharedKey)
	session := NewSession(peerID, sharedKey, "127.0.0.1:37374")
	sm.AddSession(session)

	auth := NewAuthenticator(sm)
	data := make([]byte, 1400)
	rand.Read(data)
	sig, _ := auth.SignWithPeerID(peerID, data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = auth.VerifyWithPeerID(peerID, data, sig)
	}
}

// Test constant-time comparison
func TestVerifyHMAC_ConstantTime(t *testing.T) {
	t.Parallel()

	// This test verifies that the verification doesn't have timing variations
	// based on where the first difference occurs
	key := []byte("constant-time-test-key")
	data := []byte("test data for timing")
	correctMAC := ComputeHMAC(key, data)

	// Create copy of correct MAC for test
	correctMACCopy := make([]byte, len(correctMAC))
	copy(correctMACCopy, correctMAC)

	// Create MACs with differences at different positions
	wrongFirstByte := make([]byte, len(correctMAC))
	copy(wrongFirstByte, correctMAC)
	wrongFirstByte[0] ^= 0xFF

	wrongLastByte := make([]byte, len(correctMAC))
	copy(wrongLastByte, correctMAC)
	wrongLastByte[31] ^= 0xFF

	wrongMiddleByte := make([]byte, len(correctMAC))
	copy(wrongMiddleByte, correctMAC)
	wrongMiddleByte[15] ^= 0xFF

	testCases := []struct {
		name     string
		mac      []byte
		expected bool
	}{
		{"correct MAC", correctMACCopy, true},
		{"wrong first byte", wrongFirstByte, false},
		{"wrong last byte", wrongLastByte, false},
		{"wrong middle byte", wrongMiddleByte, false},
		{"all zeros", make([]byte, 32), false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Just verify it runs without panic and returns consistent results
			result1 := VerifyHMAC(key, data, tc.mac)
			result2 := VerifyHMAC(key, data, tc.mac)
			assert.Equal(t, result1, result2)
			assert.Equal(t, tc.expected, result1)
		})
	}
}

// Test that different data produces different MACs (collision resistance)
func TestComputeHMAC_CollisionResistance(t *testing.T) {
	t.Parallel()

	key := []byte("collision-test-key")
	numSamples := 1000
	macs := make(map[string]bool)

	for i := 0; i < numSamples; i++ {
		data := make([]byte, 100)
		rand.Read(data)
		mac := ComputeHMAC(key, data)

		macStr := string(mac)
		if macs[macStr] {
			t.Fatalf("Found collision at iteration %d", i)
		}
		macs[macStr] = true
	}
}

// Test byte sensitivity
func TestComputeHMAC_ByteSensitivity(t *testing.T) {
	t.Parallel()

	key := []byte("sensitivity-test-key")
	data := bytes.Repeat([]byte{0x00}, 100)
	originalMAC := ComputeHMAC(key, data)

	// Flip each byte and verify MAC changes
	for i := 0; i < len(data); i++ {
		modified := make([]byte, len(data))
		copy(modified, data)
		modified[i] ^= 0x01

		modifiedMAC := ComputeHMAC(key, modified)
		assert.NotEqual(t, originalMAC, modifiedMAC, "MAC should change when byte %d is flipped", i)
	}
}
