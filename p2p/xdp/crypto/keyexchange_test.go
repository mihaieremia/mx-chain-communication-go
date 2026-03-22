package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateKeyPair(t *testing.T) {
	t.Parallel()

	kp, err := GenerateKeyPair()

	require.NoError(t, err)
	require.NotNil(t, kp)
	assert.Len(t, kp.PrivateKey, KeySize)
	assert.Len(t, kp.PublicKey, KeySize)
}

func TestGenerateKeyPair_Unique(t *testing.T) {
	t.Parallel()

	kp1, err := GenerateKeyPair()
	require.NoError(t, err)

	kp2, err := GenerateKeyPair()
	require.NoError(t, err)

	assert.NotEqual(t, kp1.PrivateKey, kp2.PrivateKey)
	assert.NotEqual(t, kp1.PublicKey, kp2.PublicKey)
}

func TestGenerateKeyPair_Clamping(t *testing.T) {
	t.Parallel()

	kp, err := GenerateKeyPair()
	require.NoError(t, err)

	// Check X25519 clamping requirements
	// First byte: lowest 3 bits must be 0 (& 248)
	assert.Equal(t, byte(0), kp.PrivateKey[0]&7, "lowest 3 bits of first byte should be 0")

	// Last byte: highest bit must be 0 (& 127), second highest must be 1 (| 64)
	assert.Equal(t, byte(0), kp.PrivateKey[31]&128, "highest bit of last byte should be 0")
	assert.Equal(t, byte(64), kp.PrivateKey[31]&64, "second highest bit of last byte should be 1")
}

func TestComputeSharedSecret(t *testing.T) {
	t.Parallel()

	// Generate two key pairs
	kp1, err := GenerateKeyPair()
	require.NoError(t, err)

	kp2, err := GenerateKeyPair()
	require.NoError(t, err)

	// Compute shared secrets from both sides
	secret1, err := ComputeSharedSecret(kp1.PrivateKey, kp2.PublicKey)
	require.NoError(t, err)

	secret2, err := ComputeSharedSecret(kp2.PrivateKey, kp1.PublicKey)
	require.NoError(t, err)

	// Both parties should derive the same shared secret
	assert.Equal(t, secret1, secret2)
}

func TestComputeSharedSecret_DifferentPeers(t *testing.T) {
	t.Parallel()

	kp1, _ := GenerateKeyPair()
	kp2, _ := GenerateKeyPair()
	kp3, _ := GenerateKeyPair()

	// Shared secret with different peers should be different
	secret12, _ := ComputeSharedSecret(kp1.PrivateKey, kp2.PublicKey)
	secret13, _ := ComputeSharedSecret(kp1.PrivateKey, kp3.PublicKey)

	assert.NotEqual(t, secret12, secret13)
}

func TestDeriveKey(t *testing.T) {
	t.Parallel()

	var sharedSecret [SharedSecretSize]byte
	rand.Read(sharedSecret[:])
	salt := []byte("test-salt")

	derivedKey, err := DeriveKey(sharedSecret, salt)

	require.NoError(t, err)
	assert.Len(t, derivedKey, DerivedKeySize)
}

func TestDeriveKey_Deterministic(t *testing.T) {
	t.Parallel()

	var sharedSecret [SharedSecretSize]byte
	copy(sharedSecret[:], "deterministic-shared-secret-test")
	salt := []byte("deterministic-salt")

	key1, err := DeriveKey(sharedSecret, salt)
	require.NoError(t, err)

	key2, err := DeriveKey(sharedSecret, salt)
	require.NoError(t, err)

	assert.Equal(t, key1, key2)
}

func TestDeriveKey_DifferentSalts(t *testing.T) {
	t.Parallel()

	var sharedSecret [SharedSecretSize]byte
	rand.Read(sharedSecret[:])

	key1, err := DeriveKey(sharedSecret, []byte("salt-one"))
	require.NoError(t, err)

	key2, err := DeriveKey(sharedSecret, []byte("salt-two"))
	require.NoError(t, err)

	assert.NotEqual(t, key1, key2)
}

func TestDeriveKey_NilSalt(t *testing.T) {
	t.Parallel()

	var sharedSecret [SharedSecretSize]byte
	rand.Read(sharedSecret[:])

	derivedKey, err := DeriveKey(sharedSecret, nil)

	require.NoError(t, err)
	assert.Len(t, derivedKey, DerivedKeySize)
}

func TestDeriveSessionKey(t *testing.T) {
	t.Parallel()

	kp1, _ := GenerateKeyPair()
	kp2, _ := GenerateKeyPair()
	salt := []byte("session-salt")

	// Derive session key from both sides
	key1, err := DeriveSessionKey(kp1.PrivateKey, kp2.PublicKey, salt)
	require.NoError(t, err)

	key2, err := DeriveSessionKey(kp2.PrivateKey, kp1.PublicKey, salt)
	require.NoError(t, err)

	// Both sides should derive the same session key
	assert.Equal(t, key1, key2)
}

func TestNewKeyExchange(t *testing.T) {
	t.Parallel()

	ke, err := NewKeyExchange()

	require.NoError(t, err)
	require.NotNil(t, ke)
}

func TestKeyExchange_GetPublicKey(t *testing.T) {
	t.Parallel()

	ke, err := NewKeyExchange()
	require.NoError(t, err)

	pubKey := ke.GetPublicKey()

	assert.Len(t, pubKey, KeySize)
}

func TestKeyExchange_GetPublicKeyBytes(t *testing.T) {
	t.Parallel()

	ke, err := NewKeyExchange()
	require.NoError(t, err)

	pubKeyBytes := ke.GetPublicKeyBytes()

	assert.Len(t, pubKeyBytes, KeySize)

	// Should match GetPublicKey
	pubKey := ke.GetPublicKey()
	assert.Equal(t, pubKey[:], pubKeyBytes)
}

func TestKeyExchange_DeriveSharedKey(t *testing.T) {
	t.Parallel()

	ke1, _ := NewKeyExchange()
	ke2, _ := NewKeyExchange()
	salt := []byte("test-salt")

	// Derive shared keys from both sides
	key1, err := ke1.DeriveSharedKey(ke2.GetPublicKeyBytes(), salt)
	require.NoError(t, err)

	key2, err := ke2.DeriveSharedKey(ke1.GetPublicKeyBytes(), salt)
	require.NoError(t, err)

	// Both should derive the same key
	assert.Equal(t, key1, key2)
	assert.Len(t, key1, DerivedKeySize)
}

func TestKeyExchange_DeriveSharedKey_InvalidKeySize(t *testing.T) {
	t.Parallel()

	ke, _ := NewKeyExchange()

	testCases := []struct {
		name    string
		keySize int
	}{
		{"too short", 16},
		{"too long", 64},
		{"empty", 0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			invalidKey := make([]byte, tc.keySize)
			_, err := ke.DeriveSharedKey(invalidKey, nil)

			require.Error(t, err)
		})
	}
}

func TestKeyExchange_RegenerateKeyPair(t *testing.T) {
	t.Parallel()

	ke, _ := NewKeyExchange()
	originalPubKey := ke.GetPublicKey()

	err := ke.RegenerateKeyPair()
	require.NoError(t, err)

	newPubKey := ke.GetPublicKey()

	// Key should have changed
	assert.NotEqual(t, originalPubKey, newPubKey)
}

func TestKeyExchange_RegenerateKeyPair_StillValid(t *testing.T) {
	t.Parallel()

	ke1, _ := NewKeyExchange()
	ke2, _ := NewKeyExchange()
	salt := []byte("test-salt")

	// Regenerate ke1's keys
	err := ke1.RegenerateKeyPair()
	require.NoError(t, err)

	// Should still be able to derive shared keys
	key1, err := ke1.DeriveSharedKey(ke2.GetPublicKeyBytes(), salt)
	require.NoError(t, err)

	key2, err := ke2.DeriveSharedKey(ke1.GetPublicKeyBytes(), salt)
	require.NoError(t, err)

	assert.Equal(t, key1, key2)
}

func TestKeySize(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 32, KeySize)
}

func TestSharedSecretSize(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 32, SharedSecretSize)
}

func TestDerivedKeySize(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 32, DerivedKeySize)
}

func TestHKDFInfo(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []byte("mvx-xdp-v1"), HKDFInfo)
}

// Test full key exchange flow between two parties
func TestFullKeyExchangeFlow(t *testing.T) {
	t.Parallel()

	// Alice creates her key exchange handler
	alice, err := NewKeyExchange()
	require.NoError(t, err)

	// Bob creates his key exchange handler
	bob, err := NewKeyExchange()
	require.NoError(t, err)

	// Alice and Bob exchange public keys (simulated)
	alicePubKey := alice.GetPublicKeyBytes()
	bobPubKey := bob.GetPublicKeyBytes()

	// Use a common salt (e.g., derived from connection info)
	salt := []byte("connection-specific-salt")

	// Both derive the shared session key
	aliceKey, err := alice.DeriveSharedKey(bobPubKey, salt)
	require.NoError(t, err)

	bobKey, err := bob.DeriveSharedKey(alicePubKey, salt)
	require.NoError(t, err)

	// Both should have the same session key
	assert.Equal(t, aliceKey, bobKey)

	// Use the key to sign and verify a message
	message := []byte("test message")
	signature := SignPacket(aliceKey, message)
	valid := VerifyPacket(bobKey, message, signature)
	assert.True(t, valid)
}

// Benchmarks
func BenchmarkGenerateKeyPair(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = GenerateKeyPair()
	}
}

func BenchmarkComputeSharedSecret(b *testing.B) {
	kp1, _ := GenerateKeyPair()
	kp2, _ := GenerateKeyPair()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ComputeSharedSecret(kp1.PrivateKey, kp2.PublicKey)
	}
}

func BenchmarkDeriveKey(b *testing.B) {
	var sharedSecret [SharedSecretSize]byte
	rand.Read(sharedSecret[:])
	salt := []byte("benchmark-salt")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = DeriveKey(sharedSecret, salt)
	}
}

func BenchmarkDeriveSessionKey(b *testing.B) {
	kp1, _ := GenerateKeyPair()
	kp2, _ := GenerateKeyPair()
	salt := []byte("benchmark-salt")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = DeriveSessionKey(kp1.PrivateKey, kp2.PublicKey, salt)
	}
}

func BenchmarkKeyExchange_DeriveSharedKey(b *testing.B) {
	ke1, _ := NewKeyExchange()
	ke2, _ := NewKeyExchange()
	peerPubKey := ke2.GetPublicKeyBytes()
	salt := []byte("benchmark-salt")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ke1.DeriveSharedKey(peerPubKey, salt)
	}
}

// Test that key derivation produces different keys for different shared secrets
func TestDeriveKey_SecretSensitivity(t *testing.T) {
	t.Parallel()

	salt := []byte("same-salt")
	keys := make(map[string]bool)

	for i := 0; i < 100; i++ {
		var secret [SharedSecretSize]byte
		rand.Read(secret[:])

		key, err := DeriveKey(secret, salt)
		require.NoError(t, err)

		keyStr := string(key)
		if keys[keyStr] {
			t.Fatalf("Found duplicate key at iteration %d", i)
		}
		keys[keyStr] = true
	}
}

// Test that shared secret computation is symmetric
func TestComputeSharedSecret_Symmetry(t *testing.T) {
	t.Parallel()

	for i := 0; i < 100; i++ {
		kp1, _ := GenerateKeyPair()
		kp2, _ := GenerateKeyPair()

		secret1, err := ComputeSharedSecret(kp1.PrivateKey, kp2.PublicKey)
		require.NoError(t, err)

		secret2, err := ComputeSharedSecret(kp2.PrivateKey, kp1.PublicKey)
		require.NoError(t, err)

		assert.Equal(t, secret1, secret2, "Shared secrets should match at iteration %d", i)
	}
}

// Test key exchange with multiple parties
func TestKeyExchange_MultipleParties(t *testing.T) {
	t.Parallel()

	// Create 5 parties
	parties := make([]*KeyExchange, 5)
	for i := range parties {
		ke, err := NewKeyExchange()
		require.NoError(t, err)
		parties[i] = ke
	}

	salt := []byte("multi-party-salt")

	// Each pair should be able to derive a unique shared key
	sharedKeys := make(map[string]bool)

	for i := 0; i < len(parties); i++ {
		for j := i + 1; j < len(parties); j++ {
			key1, err := parties[i].DeriveSharedKey(parties[j].GetPublicKeyBytes(), salt)
			require.NoError(t, err)

			key2, err := parties[j].DeriveSharedKey(parties[i].GetPublicKeyBytes(), salt)
			require.NoError(t, err)

			// Keys should match between pair
			assert.Equal(t, key1, key2)

			// Key should be unique to this pair
			keyStr := string(key1)
			assert.False(t, sharedKeys[keyStr], "Shared key should be unique for each pair")
			sharedKeys[keyStr] = true
		}
	}
}

// Test key derivation output format
func TestDeriveKey_OutputFormat(t *testing.T) {
	t.Parallel()

	var secret [SharedSecretSize]byte
	rand.Read(secret[:])

	key, err := DeriveKey(secret, nil)
	require.NoError(t, err)

	// Key should be non-zero
	allZero := bytes.Equal(key, make([]byte, DerivedKeySize))
	assert.False(t, allZero, "Derived key should not be all zeros")
}
