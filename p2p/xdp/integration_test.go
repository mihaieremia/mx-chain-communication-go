package xdp

import (
	"bytes"
	"crypto/rand"
	"sync"
	"testing"
	"time"

	"github.com/multiversx/mx-chain-communication-go/p2p/xdp/crypto"
	"github.com/multiversx/mx-chain-core-go/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration tests for the XDP package components

func TestIntegration_PacketEncodeDecodeRoundtrip(t *testing.T) {
	t.Parallel()

	// Create a packet with all fields set
	original := NewPacket()
	original.Flags = FlagDirect | FlagPriority
	original.MsgType = MsgTypeConsensus
	original.TopicID = 0x1234
	original.SeqNo = 12345678
	original.Timestamp = time.Now().Unix()
	original.SetPeerID([]byte("test-peer-id-12345678901234567890"))
	_ = original.SetPayload([]byte("test payload data with some content"))
	original.SetFragment(1, 0)

	// Set a dummy HMAC
	rand.Read(original.HMAC[:])

	// Encode
	encoded, err := original.Encode()
	require.NoError(t, err)

	// Decode
	decoded, err := Decode(encoded)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.Magic, decoded.Magic)
	assert.Equal(t, original.Version, decoded.Version)
	assert.Equal(t, original.Flags, decoded.Flags)
	assert.Equal(t, original.MsgType, decoded.MsgType)
	assert.Equal(t, original.TopicID, decoded.TopicID)
	assert.Equal(t, original.SeqNo, decoded.SeqNo)
	assert.Equal(t, original.Timestamp, decoded.Timestamp)
	assert.Equal(t, original.FragTotal, decoded.FragTotal)
	assert.Equal(t, original.FragIndex, decoded.FragIndex)
	assert.Equal(t, original.PeerID, decoded.PeerID)
	assert.Equal(t, original.HMAC, decoded.HMAC)
	assert.Equal(t, original.Payload, decoded.Payload)
}

func TestIntegration_FragmentationReassembly(t *testing.T) {
	t.Parallel()

	// Create large data that requires fragmentation
	dataSize := MaxPayloadSize*5 + 500
	originalData := make([]byte, dataSize)
	rand.Read(originalData)

	var peerID [32]byte
	copy(peerID[:], "test-peer-integration")
	seqNo := uint64(99999)

	// Fragment the data
	packets, err := Fragment(originalData, seqNo, peerID)
	require.NoError(t, err)
	require.True(t, len(packets) > 1, "Should have multiple packets")

	// Create assembler and reassemble
	assembler := NewFragmentAssembler(time.Minute)
	defer assembler.Close()

	var reassembledData []byte
	var complete bool

	for _, pkt := range packets {
		reassembledData, complete, err = assembler.AddFragment(pkt)
		require.NoError(t, err)
	}

	// Verify reassembly completed successfully
	require.True(t, complete)
	assert.Equal(t, originalData, reassembledData)
}

func TestIntegration_FragmentationReassembly_OutOfOrder(t *testing.T) {
	t.Parallel()

	dataSize := MaxPayloadSize*4 + 100
	originalData := make([]byte, dataSize)
	rand.Read(originalData)

	var peerID [32]byte
	copy(peerID[:], "out-of-order-peer")
	seqNo := uint64(12345)

	packets, err := Fragment(originalData, seqNo, peerID)
	require.NoError(t, err)
	require.Len(t, packets, 5)

	assembler := NewFragmentAssembler(time.Minute)
	defer assembler.Close()

	// Add fragments in reverse order
	var reassembledData []byte
	var complete bool

	for i := len(packets) - 1; i >= 0; i-- {
		reassembledData, complete, err = assembler.AddFragment(packets[i])
		require.NoError(t, err)
	}

	require.True(t, complete)
	assert.Equal(t, originalData, reassembledData)
}

func TestIntegration_ReplayProtection(t *testing.T) {
	t.Parallel()

	rp, err := NewReplayProtector(10000, time.Minute, 100)
	require.NoError(t, err)

	var peerID [32]byte
	copy(peerID[:], "replay-test-peer")
	timestamp := time.Now().Unix()

	// First message should be valid
	err = rp.ValidateAndRecord(peerID, 1, timestamp)
	require.NoError(t, err)

	// Same message should be rejected
	err = rp.ValidateAndRecord(peerID, 1, timestamp)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrReplayDetected)

	// Next message should be valid
	err = rp.ValidateAndRecord(peerID, 2, timestamp)
	require.NoError(t, err)

	// Old message with large gap should be rejected
	rp.RecordMessage(peerID, 200)
	err = rp.IsValid(peerID, 50, timestamp) // Gap of 150 > max gap of 100
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSequenceNumberTooOld)
}

func TestIntegration_CryptoSignVerify(t *testing.T) {
	t.Parallel()

	// Create session manager
	sm := crypto.NewSessionManager(24*time.Hour, time.Hour)
	defer sm.Close()

	// Create key exchange instances for two parties
	aliceKE, err := crypto.NewKeyExchange()
	require.NoError(t, err)

	bobKE, err := crypto.NewKeyExchange()
	require.NoError(t, err)

	// Derive shared keys
	aliceKey, err := aliceKE.DeriveSharedKey(bobKE.GetPublicKeyBytes(), nil)
	require.NoError(t, err)

	bobKey, err := bobKE.DeriveSharedKey(aliceKE.GetPublicKeyBytes(), nil)
	require.NoError(t, err)

	// Keys should match
	assert.Equal(t, aliceKey, bobKey)

	// Create sessions
	alicePeerID := core.PeerID("alice-peer-id")
	bobPeerID := core.PeerID("bob-peer-id")

	aliceSession := crypto.NewSession(bobPeerID, aliceKey, "192.168.1.1:37374")
	bobSession := crypto.NewSession(alicePeerID, bobKey, "192.168.1.2:37374")

	sm.AddSession(aliceSession)
	sm.AddSession(bobSession)

	// Create authenticator
	auth := crypto.NewAuthenticator(sm)

	// Alice signs a packet for Bob
	packetData := []byte("hello from alice to bob")
	signature, err := auth.SignWithPeerID(bobPeerID, packetData)
	require.NoError(t, err)

	// Bob verifies the packet
	valid := auth.VerifyWithPeerID(bobPeerID, packetData, signature)
	assert.True(t, valid)

	// Tampered data should fail
	tamperedData := []byte("hello from alice to bob!")
	valid = auth.VerifyWithPeerID(bobPeerID, tamperedData, signature)
	assert.False(t, valid)
}

func TestIntegration_TopicRegistry(t *testing.T) {
	t.Parallel()

	registry := NewTopicRegistry()

	topics := []string{
		"consensus/blocks",
		"tx/pool",
		"heartbeat",
		"sync/data",
		"validator/messages",
	}

	// Register all topics
	ids := make(map[string]uint16)
	for _, topic := range topics {
		id := registry.Register(topic)
		ids[topic] = id
	}

	// Verify all can be looked up
	for topic, expectedID := range ids {
		id, ok := registry.GetID(topic)
		require.True(t, ok)
		assert.Equal(t, expectedID, id)

		foundTopic, ok := registry.GetTopic(id)
		require.True(t, ok)
		assert.Equal(t, topic, foundTopic)
	}
}

func TestIntegration_SessionManagement(t *testing.T) {
	t.Parallel()

	sm := crypto.NewSessionManager(time.Hour, time.Hour)
	defer sm.Close()

	// Create multiple sessions
	for i := 0; i < 10; i++ {
		peerID := core.PeerID("peer-" + string(rune('A'+i)))
		key := make([]byte, 32)
		rand.Read(key)
		session := crypto.NewSession(peerID, key, "192.168.1.1:37374")
		sm.AddSession(session)
	}

	assert.Equal(t, 10, sm.SessionCount())

	// Get all sessions
	sessions := sm.GetAllSessions()
	assert.Len(t, sessions, 10)

	// Remove some sessions
	sm.RemoveSession("peer-A")
	sm.RemoveSession("peer-B")

	assert.Equal(t, 8, sm.SessionCount())
}

func TestIntegration_ConcurrentFragmentation(t *testing.T) {
	assembler := NewFragmentAssembler(time.Minute)
	defer assembler.Close()

	var wg sync.WaitGroup
	numSenders := 5
	messagesPerSender := 10

	results := make(chan []byte, numSenders*messagesPerSender)

	for sender := 0; sender < numSenders; sender++ {
		wg.Add(1)
		go func(senderID int) {
			defer wg.Done()

			for msg := 0; msg < messagesPerSender; msg++ {
				var peerID [32]byte
				peerID[0] = byte(senderID)
				peerID[1] = byte(msg)

				// Create message
				dataSize := MaxPayloadSize*2 + 100*msg
				data := make([]byte, dataSize)
				rand.Read(data)

				// Fragment
				packets, err := Fragment(data, uint64(msg), peerID)
				if err != nil {
					t.Errorf("Fragment error: %v", err)
					return
				}

				// Reassemble
				var result []byte
				var complete bool
				for _, pkt := range packets {
					result, complete, err = assembler.AddFragment(pkt)
					if err != nil {
						t.Errorf("AddFragment error: %v", err)
						return
					}
				}

				if !complete {
					t.Errorf("Reassembly not complete for sender %d msg %d", senderID, msg)
					return
				}

				if !bytes.Equal(data, result) {
					t.Errorf("Data mismatch for sender %d msg %d", senderID, msg)
					return
				}

				results <- result
			}
		}(sender)
	}

	wg.Wait()
	close(results)

	count := 0
	for range results {
		count++
	}
	assert.Equal(t, numSenders*messagesPerSender, count)
}

func TestIntegration_ConfigDefaults(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()

	assert.NotZero(t, config.Port)
	assert.True(t, config.QueueSize > 0)
	assert.True(t, config.BatchSize > 0)
	assert.True(t, config.UseRealXDP)
}

// Benchmarks

func BenchmarkIntegration_PacketRoundtrip(b *testing.B) {
	payload := make([]byte, 1000)
	rand.Read(payload)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pkt := NewPacket()
		pkt.Flags = FlagDirect
		pkt.MsgType = MsgTypeConsensus
		pkt.TopicID = 0x1234
		pkt.SeqNo = uint64(i)
		pkt.Timestamp = time.Now().Unix()
		_ = pkt.SetPayload(payload)

		encoded, _ := pkt.Encode()
		_, _ = Decode(encoded)
	}
}

func BenchmarkIntegration_FragmentReassemble(b *testing.B) {
	dataSize := MaxPayloadSize * 3
	data := make([]byte, dataSize)
	rand.Read(data)

	var peerID [32]byte
	rand.Read(peerID[:])

	assembler := NewFragmentAssembler(time.Minute)
	defer assembler.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		peerID[0] = byte(i % 256)
		packets, _ := Fragment(data, uint64(i), peerID)

		for _, pkt := range packets {
			pkt.SeqNo = uint64(i)
			_, _, _ = assembler.AddFragment(pkt)
		}
	}
}

func BenchmarkIntegration_CryptoSignVerify(b *testing.B) {
	sm := crypto.NewSessionManager(24*time.Hour, time.Hour)
	defer sm.Close()

	peerID := core.PeerID("bench-peer")
	key := make([]byte, 32)
	rand.Read(key)
	session := crypto.NewSession(peerID, key, "192.168.1.1:37374")
	sm.AddSession(session)

	auth := crypto.NewAuthenticator(sm)
	data := make([]byte, 1000)
	rand.Read(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sig, _ := auth.SignWithPeerID(peerID, data)
		_ = auth.VerifyWithPeerID(peerID, data, sig)
	}
}

func BenchmarkIntegration_ReplayProtection(b *testing.B) {
	rp, _ := NewReplayProtector(100000, time.Minute, 1000)
	timestamp := time.Now().Unix()

	var peerID [32]byte
	rand.Read(peerID[:])

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = rp.ValidateAndRecord(peerID, uint64(i), timestamp)
	}
}
