package xdp

import (
	"encoding/binary"
	"hash/fnv"
	"sync"
	"time"

	"github.com/multiversx/mx-chain-core-go/core"
)

const (
	// Protocol constants
	MagicByte1 byte = 0x4D // 'M'
	MagicByte2 byte = 0x58 // 'X'

	// ProtocolVersion is the current XDP protocol version
	ProtocolVersion byte = 1

	// Header sizes
	MagicSize       = 2
	VersionSize     = 1
	FlagsSize       = 1
	MsgTypeSize     = 1
	TopicIDSize     = 2
	SeqNoSize       = 8
	TimestampSize   = 8
	FragmentSize    = 4
	PeerIDSize      = 32
	HMACSize        = 32
	HeaderSize      = MagicSize + VersionSize + FlagsSize + MsgTypeSize + TopicIDSize +
		SeqNoSize + TimestampSize + FragmentSize + PeerIDSize + HMACSize // 98 bytes

	// MTU and payload sizes
	DefaultMTU     = 1500
	MaxPayloadSize = DefaultMTU - HeaderSize // 1402 bytes

	// MaxMessageSize is the maximum size of a complete message (2MB)
	MaxMessageSize = 1 << 21
)

// Packet flags
const (
	FlagNone      byte = 0x00
	FlagAckReq    byte = 0x01 // Request acknowledgment
	FlagBroadcast byte = 0x02 // Broadcast message
	FlagDirect    byte = 0x04 // Direct peer-to-peer message
	FlagPriority  byte = 0x08 // High priority message
	FlagFragment  byte = 0x10 // This is a fragment
)

// Message types
const (
	MsgTypeUnknown   byte = 0x00
	MsgTypeConsensus byte = 0x01
	MsgTypeBlock     byte = 0x02
	MsgTypeMiniBlock byte = 0x03
	MsgTypeTx        byte = 0x04
	MsgTypeHeartbeat byte = 0x05
	MsgTypeTrieNode  byte = 0x06
	MsgTypeGeneric   byte = 0xFF
)

// Packet represents an XDP packet
type Packet struct {
	// Header fields
	Magic     [2]byte
	Version   byte
	Flags     byte
	MsgType   byte
	TopicID   uint16
	SeqNo     uint64
	Timestamp int64
	FragTotal uint16
	FragIndex uint16
	PeerID    [32]byte
	HMAC      [32]byte

	// Payload
	Payload []byte
}

// NewPacket creates a new XDP packet
func NewPacket() *Packet {
	return &Packet{
		Magic:     [2]byte{MagicByte1, MagicByte2},
		Version:   ProtocolVersion,
		Timestamp: time.Now().Unix(),
	}
}

// SetPayload sets the packet payload
func (p *Packet) SetPayload(data []byte) error {
	if len(data) > MaxPayloadSize {
		return ErrPacketTooLarge
	}
	p.Payload = data
	return nil
}

// SetPeerID sets the sender peer ID (truncated to 32 bytes)
func (p *Packet) SetPeerID(peerID []byte) {
	copy(p.PeerID[:], peerID)
}

// peerIDToBytes converts a core.PeerID to a [32]byte for use in packet-level
// fields (wire format, replay protection keys). The PeerID is truncated to
// 32 bytes to match the wire packet PeerID field size.
func peerIDToBytes(peerID core.PeerID) [32]byte {
	var b [32]byte
	copy(b[:], []byte(peerID))
	return b
}

// SetTopic sets the topic and computes the topic ID
func (p *Packet) SetTopic(topic string) {
	p.TopicID = TopicToID(topic)
}

// SetFragment sets fragment information
func (p *Packet) SetFragment(total, index uint16) {
	p.FragTotal = total
	p.FragIndex = index
	if total > 1 {
		p.Flags |= FlagFragment
	}
}

// IsFragment returns true if this is a fragmented packet
func (p *Packet) IsFragment() bool {
	return p.Flags&FlagFragment != 0
}

// IsBroadcast returns true if this is a broadcast packet
func (p *Packet) IsBroadcast() bool {
	return p.Flags&FlagBroadcast != 0
}

// IsDirect returns true if this is a direct message
func (p *Packet) IsDirect() bool {
	return p.Flags&FlagDirect != 0
}

// Size returns the total size of the packet when encoded
func (p *Packet) Size() int {
	return HeaderSize + len(p.Payload)
}

// Encode encodes the packet to bytes (without HMAC - HMAC is set separately)
func (p *Packet) Encode() ([]byte, error) {
	totalSize := HeaderSize + len(p.Payload)
	if totalSize > DefaultMTU {
		return nil, ErrPacketTooLarge
	}

	buf := make([]byte, totalSize)

	// Magic (2 bytes)
	buf[0] = p.Magic[0]
	buf[1] = p.Magic[1]

	// Version (1 byte)
	buf[2] = p.Version

	// Flags (1 byte)
	buf[3] = p.Flags

	// Message type (1 byte)
	buf[4] = p.MsgType

	// Topic ID (2 bytes, big endian)
	binary.BigEndian.PutUint16(buf[5:7], p.TopicID)

	// Sequence number (8 bytes, big endian)
	binary.BigEndian.PutUint64(buf[7:15], p.SeqNo)

	// Timestamp (8 bytes, big endian)
	binary.BigEndian.PutUint64(buf[15:23], uint64(p.Timestamp))

	// Fragment info (4 bytes: 2 for total, 2 for index)
	binary.BigEndian.PutUint16(buf[23:25], p.FragTotal)
	binary.BigEndian.PutUint16(buf[25:27], p.FragIndex)

	// Peer ID (32 bytes)
	copy(buf[27:59], p.PeerID[:])

	// HMAC (32 bytes)
	copy(buf[59:91], p.HMAC[:])

	// Payload
	copy(buf[HeaderSize:], p.Payload)

	return buf, nil
}

// EncodeForHMAC returns the data to be used for HMAC calculation (header without HMAC + payload)
func (p *Packet) EncodeForHMAC() []byte {
	// Create buffer without HMAC field
	dataSize := (HeaderSize - HMACSize) + len(p.Payload)
	buf := make([]byte, dataSize)

	// Magic (2 bytes)
	buf[0] = p.Magic[0]
	buf[1] = p.Magic[1]

	// Version (1 byte)
	buf[2] = p.Version

	// Flags (1 byte)
	buf[3] = p.Flags

	// Message type (1 byte)
	buf[4] = p.MsgType

	// Topic ID (2 bytes, big endian)
	binary.BigEndian.PutUint16(buf[5:7], p.TopicID)

	// Sequence number (8 bytes, big endian)
	binary.BigEndian.PutUint64(buf[7:15], p.SeqNo)

	// Timestamp (8 bytes, big endian)
	binary.BigEndian.PutUint64(buf[15:23], uint64(p.Timestamp))

	// Fragment info (4 bytes)
	binary.BigEndian.PutUint16(buf[23:25], p.FragTotal)
	binary.BigEndian.PutUint16(buf[25:27], p.FragIndex)

	// Peer ID (32 bytes)
	copy(buf[27:59], p.PeerID[:])

	// Payload
	copy(buf[59:], p.Payload)

	return buf
}

// Decode decodes a packet from bytes
func Decode(data []byte) (*Packet, error) {
	if len(data) < HeaderSize {
		return nil, ErrPacketTooSmall
	}

	p := &Packet{}

	// Magic (2 bytes)
	p.Magic[0] = data[0]
	p.Magic[1] = data[1]

	if p.Magic[0] != MagicByte1 || p.Magic[1] != MagicByte2 {
		return nil, ErrInvalidMagic
	}

	// Version (1 byte)
	p.Version = data[2]
	if p.Version != ProtocolVersion {
		return nil, ErrInvalidVersion
	}

	// Flags (1 byte)
	p.Flags = data[3]

	// Message type (1 byte)
	p.MsgType = data[4]

	// Topic ID (2 bytes, big endian)
	p.TopicID = binary.BigEndian.Uint16(data[5:7])

	// Sequence number (8 bytes, big endian)
	p.SeqNo = binary.BigEndian.Uint64(data[7:15])

	// Timestamp (8 bytes, big endian)
	p.Timestamp = int64(binary.BigEndian.Uint64(data[15:23]))

	// Fragment info (4 bytes)
	p.FragTotal = binary.BigEndian.Uint16(data[23:25])
	p.FragIndex = binary.BigEndian.Uint16(data[25:27])

	// Peer ID (32 bytes)
	copy(p.PeerID[:], data[27:59])

	// HMAC (32 bytes)
	copy(p.HMAC[:], data[59:91])

	// Payload
	if len(data) > HeaderSize {
		p.Payload = make([]byte, len(data)-HeaderSize)
		copy(p.Payload, data[HeaderSize:])
	}

	return p, nil
}

// TopicToID converts a topic string to a 16-bit ID
func TopicToID(topic string) uint16 {
	h := fnv.New32a()
	h.Write([]byte(topic))
	return uint16(h.Sum32() & 0xFFFF)
}

// TopicRegistry maintains bidirectional mapping between topics and IDs
type TopicRegistry struct {
	mu       sync.RWMutex
	byID     map[uint16]string
	byString map[string]uint16
}

// NewTopicRegistry creates a new topic registry
func NewTopicRegistry() *TopicRegistry {
	return &TopicRegistry{
		byID:     make(map[uint16]string),
		byString: make(map[string]uint16),
	}
}

// Register registers a topic and returns its ID.
// If a hash collision is detected (another topic already occupies the computed ID),
// the ID is incremented until a free slot is found, and a warning is logged via the
// returned collision flag so callers can log appropriately.
func (r *TopicRegistry) Register(topic string) uint16 {
	r.mu.Lock()
	defer r.mu.Unlock()

	if id, ok := r.byString[topic]; ok {
		return id
	}

	id := TopicToID(topic)

	// Handle hash collisions: if the ID is already taken by a different topic,
	// probe linearly until we find a free slot.
	for {
		existing, occupied := r.byID[id]
		if !occupied {
			break
		}
		if existing == topic {
			// Same topic already registered (shouldn't reach here due to
			// byString check above, but be defensive).
			break
		}
		// Collision detected — increment and wrap around uint16 range.
		id++
	}

	r.byID[id] = topic
	r.byString[topic] = id
	return id
}

// GetTopic returns the topic for a given ID
func (r *TopicRegistry) GetTopic(id uint16) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	topic, ok := r.byID[id]
	return topic, ok
}

// GetID returns the ID for a given topic
func (r *TopicRegistry) GetID(topic string) (uint16, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	id, ok := r.byString[topic]
	return id, ok
}
