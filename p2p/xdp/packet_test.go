package xdp

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPacket(t *testing.T) {
	t.Parallel()

	pkt := NewPacket()

	assert.Equal(t, MagicByte1, pkt.Magic[0])
	assert.Equal(t, MagicByte2, pkt.Magic[1])
	assert.Equal(t, ProtocolVersion, pkt.Version)
	assert.NotZero(t, pkt.Timestamp)
}

func TestPacket_SetPayload(t *testing.T) {
	t.Parallel()

	t.Run("valid payload", func(t *testing.T) {
		pkt := NewPacket()
		payload := []byte("test payload data")

		err := pkt.SetPayload(payload)

		require.NoError(t, err)
		assert.Equal(t, payload, pkt.Payload)
	})

	t.Run("payload at max size", func(t *testing.T) {
		pkt := NewPacket()
		payload := make([]byte, MaxPayloadSize)

		err := pkt.SetPayload(payload)

		require.NoError(t, err)
		assert.Len(t, pkt.Payload, MaxPayloadSize)
	})

	t.Run("payload exceeds max size", func(t *testing.T) {
		pkt := NewPacket()
		payload := make([]byte, MaxPayloadSize+1)

		err := pkt.SetPayload(payload)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPacketTooLarge)
	})
}

func TestPacket_SetPeerID(t *testing.T) {
	t.Parallel()

	t.Run("exact 32 bytes", func(t *testing.T) {
		pkt := NewPacket()
		peerID := make([]byte, 32)
		for i := range peerID {
			peerID[i] = byte(i)
		}

		pkt.SetPeerID(peerID)

		assert.Equal(t, peerID, pkt.PeerID[:])
	})

	t.Run("shorter than 32 bytes", func(t *testing.T) {
		pkt := NewPacket()
		peerID := []byte("short-peer-id")

		pkt.SetPeerID(peerID)

		assert.Equal(t, peerID, pkt.PeerID[:len(peerID)])
		// Rest should be zeros
		for i := len(peerID); i < 32; i++ {
			assert.Zero(t, pkt.PeerID[i])
		}
	})

	t.Run("longer than 32 bytes truncates", func(t *testing.T) {
		pkt := NewPacket()
		peerID := make([]byte, 64)
		for i := range peerID {
			peerID[i] = byte(i)
		}

		pkt.SetPeerID(peerID)

		assert.Equal(t, peerID[:32], pkt.PeerID[:])
	})
}

func TestPacket_SetFragment(t *testing.T) {
	t.Parallel()

	t.Run("single packet (no fragment)", func(t *testing.T) {
		pkt := NewPacket()

		pkt.SetFragment(1, 0)

		assert.Equal(t, uint16(1), pkt.FragTotal)
		assert.Equal(t, uint16(0), pkt.FragIndex)
		assert.False(t, pkt.IsFragment())
	})

	t.Run("multi-fragment packet", func(t *testing.T) {
		pkt := NewPacket()

		pkt.SetFragment(5, 2)

		assert.Equal(t, uint16(5), pkt.FragTotal)
		assert.Equal(t, uint16(2), pkt.FragIndex)
		assert.True(t, pkt.IsFragment())
		assert.Equal(t, FlagFragment, pkt.Flags&FlagFragment)
	})
}

func TestPacket_Flags(t *testing.T) {
	t.Parallel()

	t.Run("broadcast flag", func(t *testing.T) {
		pkt := NewPacket()

		pkt.Flags = FlagBroadcast

		assert.True(t, pkt.IsBroadcast())
		assert.False(t, pkt.IsDirect())
		assert.False(t, pkt.IsFragment())
	})

	t.Run("direct flag", func(t *testing.T) {
		pkt := NewPacket()

		pkt.Flags = FlagDirect

		assert.False(t, pkt.IsBroadcast())
		assert.True(t, pkt.IsDirect())
		assert.False(t, pkt.IsFragment())
	})

	t.Run("multiple flags", func(t *testing.T) {
		pkt := NewPacket()

		pkt.Flags = FlagDirect | FlagPriority | FlagAckReq

		assert.True(t, pkt.IsDirect())
		assert.Equal(t, FlagPriority, pkt.Flags&FlagPriority)
		assert.Equal(t, FlagAckReq, pkt.Flags&FlagAckReq)
	})
}

func TestPacket_EncodeDecode(t *testing.T) {
	t.Parallel()

	t.Run("encode and decode roundtrip", func(t *testing.T) {
		original := NewPacket()
		original.Flags = FlagDirect | FlagPriority
		original.MsgType = MsgTypeConsensus
		original.TopicID = 0x1234
		original.SeqNo = 12345678
		original.Timestamp = time.Now().Unix()
		original.SetPeerID([]byte("test-peer-id-12345678901234567890"))
		_ = original.SetPayload([]byte("test payload data"))
		original.SetFragment(1, 0)

		// Set a dummy HMAC
		for i := range original.HMAC {
			original.HMAC[i] = byte(i)
		}

		// Encode
		encoded, err := original.Encode()
		require.NoError(t, err)
		assert.Equal(t, HeaderSize+len(original.Payload), len(encoded))

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
	})

	t.Run("empty payload", func(t *testing.T) {
		original := NewPacket()

		encoded, err := original.Encode()
		require.NoError(t, err)
		assert.Equal(t, HeaderSize, len(encoded))

		decoded, err := Decode(encoded)
		require.NoError(t, err)
		assert.Nil(t, decoded.Payload)
	})

	t.Run("max payload", func(t *testing.T) {
		original := NewPacket()
		payload := make([]byte, MaxPayloadSize)
		for i := range payload {
			payload[i] = byte(i % 256)
		}
		_ = original.SetPayload(payload)

		encoded, err := original.Encode()
		require.NoError(t, err)
		assert.Equal(t, DefaultMTU, len(encoded))

		decoded, err := Decode(encoded)
		require.NoError(t, err)
		assert.Equal(t, payload, decoded.Payload)
	})
}

func TestDecode_Errors(t *testing.T) {
	t.Parallel()

	t.Run("packet too small", func(t *testing.T) {
		data := make([]byte, HeaderSize-1)

		_, err := Decode(data)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPacketTooSmall)
	})

	t.Run("invalid magic", func(t *testing.T) {
		data := make([]byte, HeaderSize)
		data[0] = 0x00
		data[1] = 0x00

		_, err := Decode(data)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidMagic)
	})

	t.Run("invalid version", func(t *testing.T) {
		data := make([]byte, HeaderSize)
		data[0] = MagicByte1
		data[1] = MagicByte2
		data[2] = ProtocolVersion + 1 // Invalid version

		_, err := Decode(data)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidVersion)
	})
}

func TestPacket_EncodeForHMAC(t *testing.T) {
	t.Parallel()

	pkt := NewPacket()
	pkt.Flags = FlagDirect
	pkt.MsgType = MsgTypeBlock
	pkt.TopicID = 0xABCD
	pkt.SeqNo = 99999
	pkt.SetPeerID([]byte("peer-for-hmac-test"))
	_ = pkt.SetPayload([]byte("payload data"))

	hmacData := pkt.EncodeForHMAC()

	// HMAC data should be header (minus HMAC) + payload
	expectedLen := (HeaderSize - HMACSize) + len(pkt.Payload)
	assert.Equal(t, expectedLen, len(hmacData))

	// Verify magic bytes are in the right place
	assert.Equal(t, MagicByte1, hmacData[0])
	assert.Equal(t, MagicByte2, hmacData[1])

	// Verify payload is at the end
	payloadStart := HeaderSize - HMACSize
	assert.Equal(t, pkt.Payload, hmacData[payloadStart:])
}

func TestTopicToID(t *testing.T) {
	t.Parallel()

	t.Run("deterministic", func(t *testing.T) {
		topic := "test/topic/name"

		id1 := TopicToID(topic)
		id2 := TopicToID(topic)

		assert.Equal(t, id1, id2)
	})

	t.Run("different topics different IDs", func(t *testing.T) {
		id1 := TopicToID("topic1")
		id2 := TopicToID("topic2")

		assert.NotEqual(t, id1, id2)
	})

	t.Run("empty topic", func(t *testing.T) {
		id := TopicToID("")

		// Should not panic, just return some value
		assert.IsType(t, uint16(0), id)
	})
}

func TestTopicRegistry(t *testing.T) {
	t.Parallel()

	t.Run("register and lookup", func(t *testing.T) {
		registry := NewTopicRegistry()
		topic := "consensus/blocks"

		id := registry.Register(topic)

		// Lookup by ID
		foundTopic, ok := registry.GetTopic(id)
		require.True(t, ok)
		assert.Equal(t, topic, foundTopic)

		// Lookup by string
		foundID, ok := registry.GetID(topic)
		require.True(t, ok)
		assert.Equal(t, id, foundID)
	})

	t.Run("register same topic returns same ID", func(t *testing.T) {
		registry := NewTopicRegistry()
		topic := "test/topic"

		id1 := registry.Register(topic)
		id2 := registry.Register(topic)

		assert.Equal(t, id1, id2)
	})

	t.Run("lookup unknown topic", func(t *testing.T) {
		registry := NewTopicRegistry()

		_, ok := registry.GetID("unknown/topic")

		assert.False(t, ok)
	})

	t.Run("lookup unknown ID", func(t *testing.T) {
		registry := NewTopicRegistry()

		_, ok := registry.GetTopic(0xFFFF)

		assert.False(t, ok)
	})

	t.Run("multiple topics", func(t *testing.T) {
		registry := NewTopicRegistry()
		topics := []string{
			"consensus/blocks",
			"tx/pool",
			"heartbeat",
			"sync/data",
		}

		ids := make([]uint16, len(topics))
		for i, topic := range topics {
			ids[i] = registry.Register(topic)
		}

		// Verify all can be looked up
		for i, topic := range topics {
			foundTopic, ok := registry.GetTopic(ids[i])
			require.True(t, ok)
			assert.Equal(t, topic, foundTopic)
		}
	})
}

func TestPacket_Size(t *testing.T) {
	t.Parallel()

	t.Run("empty payload", func(t *testing.T) {
		pkt := NewPacket()

		assert.Equal(t, HeaderSize, pkt.Size())
	})

	t.Run("with payload", func(t *testing.T) {
		pkt := NewPacket()
		_ = pkt.SetPayload([]byte("test data"))

		assert.Equal(t, HeaderSize+9, pkt.Size())
	})
}

func TestPacket_SetTopic(t *testing.T) {
	t.Parallel()

	pkt := NewPacket()
	topic := "consensus/validator"

	pkt.SetTopic(topic)

	expectedID := TopicToID(topic)
	assert.Equal(t, expectedID, pkt.TopicID)
}

func TestHeaderSize(t *testing.T) {
	t.Parallel()

	// Verify HeaderSize constant matches actual header components
	// Magic(2) + Version(1) + Flags(1) + MsgType(1) + TopicID(2) +
	// SeqNo(8) + Timestamp(8) + Fragment(4) + PeerID(32) + HMAC(32) = 91
	expectedSize := MagicSize + VersionSize + FlagsSize + MsgTypeSize + TopicIDSize +
		SeqNoSize + TimestampSize + FragmentSize + PeerIDSize + HMACSize

	assert.Equal(t, expectedSize, HeaderSize)
	assert.Equal(t, 91, HeaderSize) // Calculated: 2+1+1+1+2+8+8+4+32+32 = 91
}

func TestMaxPayloadSize(t *testing.T) {
	t.Parallel()

	// Verify MaxPayloadSize + HeaderSize = MTU
	assert.Equal(t, DefaultMTU, HeaderSize+MaxPayloadSize)
	assert.Equal(t, 1409, MaxPayloadSize) // 1500 - 91 = 1409
}

// Benchmark tests
func BenchmarkPacketEncode(b *testing.B) {
	pkt := NewPacket()
	pkt.Flags = FlagDirect
	pkt.MsgType = MsgTypeBlock
	_ = pkt.SetPayload(bytes.Repeat([]byte("x"), 1000))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = pkt.Encode()
	}
}

func BenchmarkPacketDecode(b *testing.B) {
	pkt := NewPacket()
	pkt.Flags = FlagDirect
	pkt.MsgType = MsgTypeBlock
	_ = pkt.SetPayload(bytes.Repeat([]byte("x"), 1000))
	encoded, _ := pkt.Encode()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Decode(encoded)
	}
}

func BenchmarkPacketEncodeForHMAC(b *testing.B) {
	pkt := NewPacket()
	pkt.Flags = FlagDirect
	pkt.MsgType = MsgTypeBlock
	_ = pkt.SetPayload(bytes.Repeat([]byte("x"), 1000))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pkt.EncodeForHMAC()
	}
}

func BenchmarkTopicToID(b *testing.B) {
	topic := "consensus/blocks/validator/shard0"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = TopicToID(topic)
	}
}
