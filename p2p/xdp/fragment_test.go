package xdp

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFragment_SinglePacket(t *testing.T) {
	t.Parallel()

	data := []byte("small message that fits in one packet")
	var peerID [32]byte
	copy(peerID[:], "test-peer")
	seqNo := uint64(12345)

	packets, err := Fragment(data, seqNo, peerID)

	require.NoError(t, err)
	require.Len(t, packets, 1)

	pkt := packets[0]
	assert.Equal(t, data, pkt.Payload)
	assert.Equal(t, uint16(1), pkt.FragTotal)
	assert.Equal(t, uint16(0), pkt.FragIndex)
	assert.False(t, pkt.IsFragment())
	assert.Equal(t, seqNo, pkt.SeqNo)
	assert.Equal(t, peerID, pkt.PeerID)
}

func TestFragment_MultiplePackets(t *testing.T) {
	t.Parallel()

	// Create data larger than MaxPayloadSize
	dataSize := MaxPayloadSize*3 + 100
	data := make([]byte, dataSize)
	for i := range data {
		data[i] = byte(i % 256)
	}

	var peerID [32]byte
	copy(peerID[:], "test-peer")
	seqNo := uint64(99999)

	packets, err := Fragment(data, seqNo, peerID)

	require.NoError(t, err)
	require.Len(t, packets, 4)

	// Verify each fragment
	for i, pkt := range packets {
		assert.Equal(t, uint16(4), pkt.FragTotal)
		assert.Equal(t, uint16(i), pkt.FragIndex)
		assert.True(t, pkt.IsFragment())
		assert.Equal(t, seqNo, pkt.SeqNo)
		assert.Equal(t, peerID, pkt.PeerID)
	}

	// Verify reassembly produces original data
	var reassembled []byte
	for _, pkt := range packets {
		reassembled = append(reassembled, pkt.Payload...)
	}
	assert.Equal(t, data, reassembled)
}

func TestFragment_ExactMultiple(t *testing.T) {
	t.Parallel()

	// Data that's exactly 2x MaxPayloadSize
	dataSize := MaxPayloadSize * 2
	data := make([]byte, dataSize)
	for i := range data {
		data[i] = byte(i % 256)
	}

	var peerID [32]byte
	packets, err := Fragment(data, 1, peerID)

	require.NoError(t, err)
	require.Len(t, packets, 2)

	// Each packet should have exactly MaxPayloadSize
	assert.Len(t, packets[0].Payload, MaxPayloadSize)
	assert.Len(t, packets[1].Payload, MaxPayloadSize)
}

func TestFragment_Errors(t *testing.T) {
	t.Parallel()

	var peerID [32]byte

	t.Run("empty data", func(t *testing.T) {
		_, err := Fragment([]byte{}, 1, peerID)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidPacket)
	})

	t.Run("data exceeds max message size", func(t *testing.T) {
		data := make([]byte, MaxMessageSize+1)

		_, err := Fragment(data, 1, peerID)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPacketTooLarge)
	})
}

func TestFragmentAssembler_SinglePacket(t *testing.T) {
	t.Parallel()

	fa := NewFragmentAssembler(time.Minute)
	defer fa.Close()

	pkt := NewPacket()
	pkt.FragTotal = 1
	pkt.FragIndex = 0
	pkt.Payload = []byte("single packet message")

	data, complete, err := fa.AddFragment(pkt)

	require.NoError(t, err)
	assert.True(t, complete)
	assert.Equal(t, pkt.Payload, data)
}

func TestFragmentAssembler_InOrder(t *testing.T) {
	t.Parallel()

	fa := NewFragmentAssembler(time.Minute)
	defer fa.Close()

	originalData := make([]byte, MaxPayloadSize*3+100)
	for i := range originalData {
		originalData[i] = byte(i % 256)
	}

	var peerID [32]byte
	copy(peerID[:], "test-peer")
	packets, _ := Fragment(originalData, 1, peerID)

	// Add fragments in order
	for i, pkt := range packets {
		data, complete, err := fa.AddFragment(pkt)

		require.NoError(t, err)

		if i < len(packets)-1 {
			assert.False(t, complete)
			assert.Nil(t, data)
		} else {
			assert.True(t, complete)
			assert.Equal(t, originalData, data)
		}
	}

	// Pending count should be 0 after completion
	assert.Equal(t, 0, fa.PendingCount())
}

func TestFragmentAssembler_OutOfOrder(t *testing.T) {
	t.Parallel()

	fa := NewFragmentAssembler(time.Minute)
	defer fa.Close()

	originalData := make([]byte, MaxPayloadSize*4+100)
	for i := range originalData {
		originalData[i] = byte(i % 256)
	}

	var peerID [32]byte
	packets, _ := Fragment(originalData, 1, peerID)

	// Add fragments out of order: 2, 0, 4, 1, 3
	order := []int{2, 0, 4, 1, 3}
	for i, idx := range order {
		data, complete, err := fa.AddFragment(packets[idx])

		require.NoError(t, err)

		if i < len(order)-1 {
			assert.False(t, complete)
		} else {
			assert.True(t, complete)
			assert.Equal(t, originalData, data)
		}
	}
}

func TestFragmentAssembler_DuplicateFragment(t *testing.T) {
	t.Parallel()

	fa := NewFragmentAssembler(time.Minute)
	defer fa.Close()

	originalData := make([]byte, MaxPayloadSize*2+100)
	var peerID [32]byte
	packets, _ := Fragment(originalData, 1, peerID)

	// Add first fragment
	_, complete, err := fa.AddFragment(packets[0])
	require.NoError(t, err)
	assert.False(t, complete)

	// Add same fragment again (duplicate)
	_, complete, err = fa.AddFragment(packets[0])
	require.NoError(t, err)
	assert.False(t, complete)

	// Received count shouldn't increase for duplicates
	assert.Equal(t, 1, fa.PendingCount())

	// Complete with remaining fragments
	for i := 1; i < len(packets); i++ {
		_, _, _ = fa.AddFragment(packets[i])
	}

	assert.Equal(t, 0, fa.PendingCount())
}

func TestFragmentAssembler_MultipleMessages(t *testing.T) {
	t.Parallel()

	fa := NewFragmentAssembler(time.Minute)
	defer fa.Close()

	// Create two different messages from different peers
	msg1 := make([]byte, MaxPayloadSize*2+50)
	msg2 := make([]byte, MaxPayloadSize*2+100)
	for i := range msg1 {
		msg1[i] = byte(i % 256)
	}
	for i := range msg2 {
		msg2[i] = byte(255 - (i % 256))
	}

	var peer1, peer2 [32]byte
	copy(peer1[:], "peer-1")
	copy(peer2[:], "peer-2")

	packets1, _ := Fragment(msg1, 1, peer1)
	packets2, _ := Fragment(msg2, 1, peer2)

	// Interleave fragments from both messages
	_, complete, _ := fa.AddFragment(packets1[0])
	assert.False(t, complete)

	_, complete, _ = fa.AddFragment(packets2[0])
	assert.False(t, complete)

	_, complete, _ = fa.AddFragment(packets1[1])
	assert.False(t, complete)

	// Complete message 1
	data1, complete, _ := fa.AddFragment(packets1[2])
	assert.True(t, complete)
	assert.Equal(t, msg1, data1)

	_, complete, _ = fa.AddFragment(packets2[1])
	assert.False(t, complete)

	// Complete message 2
	data2, complete, _ := fa.AddFragment(packets2[2])
	assert.True(t, complete)
	assert.Equal(t, msg2, data2)

	assert.Equal(t, 0, fa.PendingCount())
}

func TestFragmentAssembler_Errors(t *testing.T) {
	t.Parallel()

	fa := NewFragmentAssembler(time.Minute)
	defer fa.Close()

	t.Run("zero total fragments", func(t *testing.T) {
		pkt := NewPacket()
		pkt.FragTotal = 0
		pkt.FragIndex = 0

		_, _, err := fa.AddFragment(pkt)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidFragment)
	})

	t.Run("index exceeds total", func(t *testing.T) {
		pkt := NewPacket()
		pkt.FragTotal = 3
		pkt.FragIndex = 5

		_, _, err := fa.AddFragment(pkt)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidFragment)
	})

	t.Run("index equals total", func(t *testing.T) {
		pkt := NewPacket()
		pkt.FragTotal = 3
		pkt.FragIndex = 3 // Should be 0, 1, or 2

		_, _, err := fa.AddFragment(pkt)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidFragment)
	})

	t.Run("too many fragments", func(t *testing.T) {
		pkt := NewPacket()
		pkt.FragTotal = MaxFragments + 1
		pkt.FragIndex = 0

		_, _, err := fa.AddFragment(pkt)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPacketTooLarge)
	})

	t.Run("inconsistent total", func(t *testing.T) {
		var peerID [32]byte
		seqNo := uint64(123)

		// First fragment says total is 3
		pkt1 := NewPacket()
		pkt1.PeerID = peerID
		pkt1.SeqNo = seqNo
		pkt1.FragTotal = 3
		pkt1.FragIndex = 0
		pkt1.Payload = []byte("frag0")

		_, _, err := fa.AddFragment(pkt1)
		require.NoError(t, err)

		// Second fragment says total is 4 (inconsistent)
		pkt2 := NewPacket()
		pkt2.PeerID = peerID
		pkt2.SeqNo = seqNo
		pkt2.FragTotal = 4 // Wrong!
		pkt2.FragIndex = 1
		pkt2.Payload = []byte("frag1")

		_, _, err = fa.AddFragment(pkt2)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidFragment)
	})
}

func TestFragmentAssembler_Timeout(t *testing.T) {
	// Short timeout for testing
	timeout := 100 * time.Millisecond
	fa := NewFragmentAssembler(timeout)
	defer fa.Close()

	// Add partial message
	pkt := NewPacket()
	pkt.FragTotal = 3
	pkt.FragIndex = 0
	pkt.Payload = []byte("partial")

	_, complete, err := fa.AddFragment(pkt)
	require.NoError(t, err)
	assert.False(t, complete)
	assert.Equal(t, 1, fa.PendingCount())

	// Wait for cleanup
	time.Sleep(timeout * 2)

	// Should be cleaned up
	assert.Equal(t, 0, fa.PendingCount())
}

func TestFragmentAssembler_DefaultTimeout(t *testing.T) {
	t.Parallel()

	// Create with zero timeout, should use default
	fa := NewFragmentAssembler(0)
	defer fa.Close()

	// Should work normally
	pkt := NewPacket()
	pkt.FragTotal = 1
	pkt.FragIndex = 0
	pkt.Payload = []byte("test")

	data, complete, err := fa.AddFragment(pkt)
	require.NoError(t, err)
	assert.True(t, complete)
	assert.Equal(t, []byte("test"), data)
}

func TestFragmentAssembler_PendingCount(t *testing.T) {
	t.Parallel()

	fa := NewFragmentAssembler(time.Minute)
	defer fa.Close()

	assert.Equal(t, 0, fa.PendingCount())

	// Add partial messages from different peers
	for i := 0; i < 5; i++ {
		pkt := NewPacket()
		var peerID [32]byte
		peerID[0] = byte(i)
		pkt.PeerID = peerID
		pkt.SeqNo = uint64(i)
		pkt.FragTotal = 3
		pkt.FragIndex = 0
		pkt.Payload = []byte("partial")

		_, _, _ = fa.AddFragment(pkt)
	}

	assert.Equal(t, 5, fa.PendingCount())
}

func TestFragment_MaxPayloadBoundary(t *testing.T) {
	t.Parallel()

	var peerID [32]byte

	testCases := []struct {
		name            string
		size            int
		expectedPackets int
	}{
		{"exactly MaxPayloadSize", MaxPayloadSize, 1},
		{"MaxPayloadSize + 1", MaxPayloadSize + 1, 2},
		{"exactly 2x MaxPayloadSize", MaxPayloadSize * 2, 2},
		{"2x MaxPayloadSize + 1", MaxPayloadSize*2 + 1, 3},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.size)

			packets, err := Fragment(data, 1, peerID)

			require.NoError(t, err)
			assert.Len(t, packets, tc.expectedPackets)
		})
	}
}

// Benchmarks
func BenchmarkFragment_SmallMessage(b *testing.B) {
	data := bytes.Repeat([]byte("x"), 1000)
	var peerID [32]byte

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Fragment(data, uint64(i), peerID)
	}
}

func BenchmarkFragment_LargeMessage(b *testing.B) {
	data := bytes.Repeat([]byte("x"), MaxPayloadSize*10)
	var peerID [32]byte

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Fragment(data, uint64(i), peerID)
	}
}

func BenchmarkFragmentAssembler_Add(b *testing.B) {
	fa := NewFragmentAssembler(time.Minute)
	defer fa.Close()

	data := bytes.Repeat([]byte("x"), MaxPayloadSize*5)
	var peerID [32]byte
	packets, _ := Fragment(data, 1, peerID)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Use different seqNo each iteration to avoid state buildup
		for j, pkt := range packets {
			pkt.SeqNo = uint64(i*len(packets) + j)
			pkt.PeerID[0] = byte(i % 256)
			_, _, _ = fa.AddFragment(pkt)
		}
	}
}
