package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

const (
	// KeySize is the size of X25519 keys
	KeySize = 32

	// SharedSecretSize is the size of the shared secret
	SharedSecretSize = 32

	// DerivedKeySize is the size of the derived key for HMAC
	DerivedKeySize = 32
)

var (
	// HKDFInfo is the info string for HKDF key derivation
	HKDFInfo = []byte("mvx-xdp-v1")
)

// KeyPair represents an X25519 key pair
type KeyPair struct {
	PrivateKey [KeySize]byte
	PublicKey  [KeySize]byte
}

// GenerateKeyPair generates a new X25519 key pair
func GenerateKeyPair() (*KeyPair, error) {
	var privateKey [KeySize]byte
	var publicKey [KeySize]byte

	// Generate random private key
	if _, err := rand.Read(privateKey[:]); err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Clamp private key per X25519 spec
	privateKey[0] &= 248
	privateKey[31] &= 127
	privateKey[31] |= 64

	// Derive public key
	curve25519.ScalarBaseMult(&publicKey, &privateKey)

	return &KeyPair{
		PrivateKey: privateKey,
		PublicKey:  publicKey,
	}, nil
}

// ComputeSharedSecret computes the shared secret using X25519 ECDH
func ComputeSharedSecret(privateKey, peerPublicKey [KeySize]byte) ([SharedSecretSize]byte, error) {
	var sharedSecret [SharedSecretSize]byte

	result, err := curve25519.X25519(privateKey[:], peerPublicKey[:])
	if err != nil {
		return sharedSecret, fmt.Errorf("failed to compute shared secret: %w", err)
	}

	copy(sharedSecret[:], result)
	return sharedSecret, nil
}

// DeriveKey derives a key from the shared secret using HKDF
func DeriveKey(sharedSecret [SharedSecretSize]byte, salt []byte) ([]byte, error) {
	if salt == nil {
		salt = make([]byte, 32)
	}

	// Use HKDF with SHA-256
	hkdfReader := hkdf.New(sha256.New, sharedSecret[:], salt, HKDFInfo)

	derivedKey := make([]byte, DerivedKeySize)
	if _, err := io.ReadFull(hkdfReader, derivedKey); err != nil {
		return nil, fmt.Errorf("failed to derive key: %w", err)
	}

	return derivedKey, nil
}

// DeriveSessionKey performs the full key exchange and derivation
func DeriveSessionKey(ourPrivateKey, theirPublicKey [KeySize]byte, salt []byte) ([]byte, error) {
	// Compute shared secret using ECDH
	sharedSecret, err := ComputeSharedSecret(ourPrivateKey, theirPublicKey)
	if err != nil {
		return nil, err
	}

	// Derive the session key
	return DeriveKey(sharedSecret, salt)
}

// KeyExchange manages the key exchange process
type KeyExchange struct {
	keyPair *KeyPair
}

// NewKeyExchange creates a new key exchange handler
func NewKeyExchange() (*KeyExchange, error) {
	kp, err := GenerateKeyPair()
	if err != nil {
		return nil, err
	}

	return &KeyExchange{
		keyPair: kp,
	}, nil
}

// GetPublicKey returns the local public key
func (ke *KeyExchange) GetPublicKey() [KeySize]byte {
	return ke.keyPair.PublicKey
}

// GetPublicKeyBytes returns the local public key as a byte slice
func (ke *KeyExchange) GetPublicKeyBytes() []byte {
	return ke.keyPair.PublicKey[:]
}

// DeriveSharedKey derives a shared key with a peer given their public key
func (ke *KeyExchange) DeriveSharedKey(peerPublicKey []byte, salt []byte) ([]byte, error) {
	if len(peerPublicKey) != KeySize {
		return nil, fmt.Errorf("invalid peer public key size: expected %d, got %d", KeySize, len(peerPublicKey))
	}

	var peerPubKeyArray [KeySize]byte
	copy(peerPubKeyArray[:], peerPublicKey)

	return DeriveSessionKey(ke.keyPair.PrivateKey, peerPubKeyArray, salt)
}

// RegenerateKeyPair generates a new key pair (for key rotation)
func (ke *KeyExchange) RegenerateKeyPair() error {
	kp, err := GenerateKeyPair()
	if err != nil {
		return err
	}

	ke.keyPair = kp
	return nil
}
