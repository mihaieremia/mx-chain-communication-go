package xdp

import (
	"errors"

	"github.com/multiversx/mx-chain-communication-go/p2p/xdp/afxdp"
)

// ErrXDPNotSupported is returned when XDP is not supported on the platform
var ErrXDPNotSupported = errors.New("XDP is not supported on this platform")

// ErrXDPNotEnabled is returned when XDP is not enabled
var ErrXDPNotEnabled = errors.New("XDP is not enabled")

// ErrInvalidPort is returned when the port is invalid
var ErrInvalidPort = errors.New("invalid XDP port")

// ErrInvalidQueueSize is returned when the queue size is invalid
var ErrInvalidQueueSize = errors.New("invalid XDP queue size")

// ErrInvalidBatchSize is returned when the batch size is invalid
var ErrInvalidBatchSize = errors.New("invalid XDP batch size")

// ErrInvalidReplayWindowSize is returned when the replay window size is invalid
var ErrInvalidReplayWindowSize = errors.New("invalid replay window size")

// ErrInvalidTimestampTolerance is returned when the timestamp tolerance is invalid
var ErrInvalidTimestampTolerance = errors.New("invalid timestamp tolerance")

// ErrInvalidPacket is returned when a packet is malformed
var ErrInvalidPacket = errors.New("invalid XDP packet")

// ErrInvalidMagic is returned when the magic bytes are incorrect
var ErrInvalidMagic = errors.New("invalid XDP packet magic")

// ErrInvalidVersion is returned when the protocol version is not supported
var ErrInvalidVersion = errors.New("unsupported XDP protocol version")

// ErrPacketTooLarge is returned when a packet exceeds the maximum size
var ErrPacketTooLarge = errors.New("XDP packet too large")

// ErrPacketTooSmall is returned when a packet is smaller than the header
var ErrPacketTooSmall = errors.New("XDP packet too small")

// ErrAuthenticationFailed is returned when HMAC verification fails
var ErrAuthenticationFailed = errors.New("XDP packet authentication failed")

// ErrReplayDetected is returned when a replayed message is detected
var ErrReplayDetected = errors.New("replay attack detected")

// ErrTimestampOutOfRange is returned when a message timestamp is too old or too new
var ErrTimestampOutOfRange = errors.New("message timestamp out of acceptable range")

// ErrSequenceNumberTooOld is returned when a sequence number is too old
var ErrSequenceNumberTooOld = errors.New("sequence number too old")

// ErrPeerNotFound is returned when a peer is not found in the XDP peer manager
var ErrPeerNotFound = errors.New("XDP peer not found")

// ErrPeerNotXDPCapable is returned when a peer does not support XDP
var ErrPeerNotXDPCapable = errors.New("peer does not support XDP")

// ErrNoSharedKey is returned when no shared key exists for a peer
var ErrNoSharedKey = errors.New("no shared key for peer")

// ErrSocketNotInitialized is returned when the XDP socket is not initialized
var ErrSocketNotInitialized = errors.New("XDP socket not initialized")

// ErrSocketClosed is returned when operations are attempted on a closed socket.
// Aliased to afxdp.ErrSocketClosed so errors.Is works across packages.
var ErrSocketClosed = afxdp.ErrSocketClosed

// ErrFragmentTimeout is returned when fragment reassembly times out
var ErrFragmentTimeout = errors.New("fragment reassembly timeout")

// ErrFragmentMissing is returned when fragments are missing
var ErrFragmentMissing = errors.New("missing fragments")

// ErrInvalidFragment is returned when a fragment is invalid
var ErrInvalidFragment = errors.New("invalid fragment")

// ErrNilLogger is returned when a nil logger is provided
var ErrNilLogger = errors.New("nil logger provided")

// ErrNilMarshaller is returned when a nil marshaller is provided
var ErrNilMarshaller = errors.New("nil marshaller provided")

// ErrNilPeerManager is returned when a nil peer manager is provided
var ErrNilPeerManager = errors.New("nil peer manager provided")

// ErrNilMessageHandler is returned when a nil message handler is provided
var ErrNilMessageHandler = errors.New("nil message handler provided")

// ErrNilRouter is returned when a nil router is provided
var ErrNilRouter = errors.New("nil router provided")

// ErrKernelVersionTooOld is returned when the kernel version doesn't support XDP
var ErrKernelVersionTooOld = errors.New("kernel version too old for XDP (requires 4.18+)")

// ErrNoNetAdmin is returned when CAP_NET_ADMIN capability is missing
var ErrNoNetAdmin = errors.New("CAP_NET_ADMIN capability required for XDP")

// ErrInterfaceNotFound is returned when the specified network interface is not found.
// Aliased to afxdp.ErrInterfaceNotFound so errors.Is works across packages.
var ErrInterfaceNotFound = afxdp.ErrInterfaceNotFound

// ErrDriverNotSupported is returned when the NIC driver doesn't support XDP
var ErrDriverNotSupported = errors.New("NIC driver does not support XDP")

// ErrSendQueueFull is returned when the async send queue is full
var ErrSendQueueFull = errors.New("send queue full")
