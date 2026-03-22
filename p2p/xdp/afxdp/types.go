// Package afxdp provides a high-performance AF_XDP socket implementation
// for kernel-bypass networking on Linux.
//
// AF_XDP (Address Family XDP) is a socket type that provides raw packet access
// with kernel bypass capabilities, enabling high-throughput, low-latency networking.
//
// Key concepts:
// - UMEM: User-space memory region shared between kernel and userspace
// - Ring buffers: Lock-free producer/consumer queues for packet descriptors
// - XDP program: eBPF program that redirects packets to AF_XDP sockets
package afxdp

import (
	"unsafe"
)

// AF_XDP socket constants
const (
	// Socket address family
	AF_XDP = 44

	// Socket type
	SOCK_RAW = 3

	// Socket options
	XDP_MMAP_OFFSETS      = 1
	XDP_RX_RING           = 2
	XDP_TX_RING           = 3
	XDP_UMEM_REG          = 4
	XDP_UMEM_FILL_RING    = 5
	XDP_UMEM_COMPLETION_RING = 6
	XDP_STATISTICS        = 7
	XDP_OPTIONS           = 8

	// Bind flags
	XDP_SHARED_UMEM       = 1 << 0
	XDP_COPY              = 1 << 1  // Force copy mode
	XDP_ZEROCOPY          = 1 << 2  // Force zero-copy mode
	XDP_USE_NEED_WAKEUP   = 1 << 3  // Use need wakeup flag

	// Ring flags
	XDP_RING_NEED_WAKEUP  = 1 << 0

	// Default sizes
	DefaultFrameSize      = 4096
	DefaultNumFrames      = 4096
	DefaultRingSize       = 2048
	DefaultFillRingSize   = 2048 * 2 // Fill ring should be larger for RX
	DefaultCompRingSize   = 2048
	DefaultBatchSize      = 64

	// XDP actions
	XDP_ABORTED = 0
	XDP_DROP    = 1
	XDP_PASS    = 2
	XDP_TX      = 3
	XDP_REDIRECT = 4

	// XDP program attach flags
	XDP_FLAGS_UPDATE_IF_NOEXIST = 1 << 0
	XDP_FLAGS_SKB_MODE          = 1 << 1
	XDP_FLAGS_DRV_MODE          = 1 << 2
	XDP_FLAGS_HW_MODE           = 1 << 3
	XDP_FLAGS_REPLACE           = 1 << 4

	// BPF map types
	BPF_MAP_TYPE_XSKMAP = 17

	// SOL_XDP is the socket option level for AF_XDP
	SOL_XDP = 283
)

// XDPUmemReg is the UMEM registration structure
type XDPUmemReg struct {
	Addr       uint64
	Len        uint64
	ChunkSize  uint32
	Headroom   uint32
	Flags      uint32
	_          uint32 // padding
}

// XDPMmapOffsets contains mmap offsets for the socket rings
type XDPMmapOffsets struct {
	Rx XDPRingOffset
	Tx XDPRingOffset
	Fr XDPRingOffset // Fill ring
	Cr XDPRingOffset // Completion ring
}

// XDPRingOffset contains offsets for a single ring
type XDPRingOffset struct {
	Producer uint64
	Consumer uint64
	Desc     uint64
	Flags    uint64
}

// XDPDesc is a descriptor in TX/RX rings
type XDPDesc struct {
	Addr    uint64
	Len     uint32
	Options uint32
}

// XDPStatistics contains socket statistics
type XDPStatistics struct {
	RxDropped       uint64
	RxInvalidDescs  uint64
	TxInvalidDescs  uint64
	RxRingFull      uint64
	RxFillRingEmptyDescs uint64
	TxRingEmptyDescs     uint64
}

// SockaddrXDP is the AF_XDP socket address structure
type SockaddrXDP struct {
	Family         uint16
	Flags          uint16
	Ifindex        uint32
	QueueID        uint32
	SharedUmemFD   uint32
}

// XDPOptions contains socket options
type XDPOptions struct {
	Flags uint32
}

// SizeOfXDPDesc is the size of an XDP descriptor
const SizeOfXDPDesc = int(unsafe.Sizeof(XDPDesc{}))

// Config holds AF_XDP socket configuration
type Config struct {
	// Interface is the network interface name (required)
	Interface string

	// QueueID is the NIC queue to bind to (default 0)
	QueueID int

	// NumFrames is the number of UMEM frames
	NumFrames uint32

	// FrameSize is the size of each UMEM frame (must be power of 2)
	FrameSize uint32

	// RxRingSize is the size of the RX ring
	RxRingSize uint32

	// TxRingSize is the size of the TX ring
	TxRingSize uint32

	// FillRingSize is the size of the fill ring
	FillRingSize uint32

	// CompRingSize is the size of the completion ring
	CompRingSize uint32

	// BatchSize is the number of packets to process per batch
	BatchSize int

	// ZeroCopy enables zero-copy mode (requires driver support)
	ZeroCopy bool

	// NeedWakeup enables the need_wakeup optimization
	NeedWakeup bool

	// XDPMode specifies the XDP attach mode
	XDPMode XDPMode

	// PollTimeout is the timeout for poll() in milliseconds
	PollTimeout int
}

// XDPMode specifies how the XDP program is attached
type XDPMode int

const (
	// XDPModeAuto tries native mode first, falls back to SKB mode
	XDPModeAuto XDPMode = iota
	// XDPModeNative uses native driver mode (requires driver support)
	XDPModeNative
	// XDPModeSKB uses generic SKB mode (works on any NIC)
	XDPModeSKB
	// XDPModeHW uses hardware offload mode (requires NIC support)
	XDPModeHW
)

// DefaultConfig returns the default AF_XDP configuration
func DefaultConfig() Config {
	return Config{
		QueueID:      0,
		NumFrames:    DefaultNumFrames,
		FrameSize:    DefaultFrameSize,
		RxRingSize:   DefaultRingSize,
		TxRingSize:   DefaultRingSize,
		FillRingSize: DefaultFillRingSize,
		CompRingSize: DefaultCompRingSize,
		BatchSize:    DefaultBatchSize,
		ZeroCopy:     false, // Start with copy mode for compatibility
		NeedWakeup:   true,  // Enable wakeup optimization
		XDPMode:      XDPModeAuto,
		PollTimeout:  1000,
	}
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Interface == "" {
		return ErrInvalidInterface
	}

	if c.NumFrames == 0 || !isPowerOfTwo(c.NumFrames) {
		return ErrInvalidNumFrames
	}

	if c.FrameSize == 0 || !isPowerOfTwo(c.FrameSize) {
		return ErrInvalidFrameSize
	}

	if c.RxRingSize == 0 || !isPowerOfTwo(c.RxRingSize) {
		return ErrInvalidRingSize
	}

	if c.TxRingSize == 0 || !isPowerOfTwo(c.TxRingSize) {
		return ErrInvalidRingSize
	}

	return nil
}

// isPowerOfTwo checks if n is a power of two
func isPowerOfTwo(n uint32) bool {
	return n > 0 && (n&(n-1)) == 0
}

// UMEMConfig holds UMEM configuration
type UMEMConfig struct {
	// NumFrames is the number of frames in the UMEM
	NumFrames uint32

	// FrameSize is the size of each frame (must be power of 2)
	FrameSize uint32

	// Headroom is the number of bytes reserved at the start of each frame
	Headroom uint32
}

// Stats contains socket statistics
type Stats struct {
	RxPackets  uint64
	TxPackets  uint64
	RxBytes    uint64
	TxBytes    uint64
	RxDrops    uint64
	TxDrops    uint64
	FreeFrames int
}

// ManagerConfig holds configuration for the AF_XDP manager
type ManagerConfig struct {
	// Interface is the network interface name
	Interface string

	// NumQueues is the number of NIC queues to use (0 = auto-detect)
	NumQueues int

	// SocketConfig is the configuration for each socket
	SocketConfig Config

	// XDPMode specifies the XDP attach mode
	XDPMode XDPMode

	// PollTimeout is the timeout for poll() in milliseconds
	PollTimeout int
}

// DefaultManagerConfig returns the default manager configuration
func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		NumQueues:    0, // Auto-detect
		SocketConfig: DefaultConfig(),
		XDPMode:      XDPModeAuto,
		PollTimeout:  1000,
	}
}

// ManagerStats contains aggregated statistics from all sockets
type ManagerStats struct {
	NumQueues int
	PerQueue  []Stats
	Total     Stats
}

// InterfaceInfo holds information about a network interface
type InterfaceInfo struct {
	Name       string
	Index      int
	MTU        int
	HWAddr     string
	NumQueues  int
	XDPSupport bool
}

// DriverInfo contains information about the NIC driver
type DriverInfo struct {
	Name        string
	Version     string
	FWVersion   string
	BusInfo     string
	XDPNative   bool
	XDPZeroCopy bool
}

// IRQInfo holds information about an IRQ
type IRQInfo struct {
	IRQ       int
	CPU       int
	Affinity  string
	Interface string
	Queue     int
}
