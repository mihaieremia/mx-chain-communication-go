package xdp

import (
	"time"
)

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

// Config holds the XDP configuration
type Config struct {
	// Enabled determines if XDP is enabled
	Enabled bool

	// Port is the UDP port for XDP communication
	Port uint16

	// Interface is the network interface to bind to (empty for auto-detect)
	Interface string

	// QueueID is the NIC queue ID to use
	QueueID int

	// NumQueues is the number of NIC queues to use (0 = auto-detect)
	NumQueues int

	// QueueSize is the size of the XDP ring buffers
	QueueSize uint32

	// BatchSize is the number of packets to process in a batch
	BatchSize uint32

	// UseRealXDP enables real AF_XDP kernel bypass when available
	// When false, always uses UDP fallback regardless of platform support
	UseRealXDP bool

	// XDPMode specifies the XDP attach mode (native, skb, hw, auto)
	XDPMode XDPMode

	// Security holds security-related configuration
	Security SecurityConfig
}

// SecurityConfig holds security configuration for XDP
type SecurityConfig struct {
	// KeyRotationInterval is how often to rotate session keys
	KeyRotationInterval time.Duration

	// ReplayWindowSize is the size of the replay protection window
	ReplayWindowSize int

	// TimestampTolerance is the acceptable time difference for messages
	TimestampTolerance time.Duration

	// MaxSeqNoGap is the maximum acceptable sequence number gap
	MaxSeqNoGap uint64
}

// DefaultConfig returns a default XDP configuration
func DefaultConfig() Config {
	return Config{
		Enabled:    false,
		Port:       37374,
		Interface:  "",
		QueueID:    0,
		NumQueues:  0, // Auto-detect
		QueueSize:  2048,
		BatchSize:  64,
		UseRealXDP: true, // Use real AF_XDP when available
		XDPMode:    XDPModeAuto,
		Security: SecurityConfig{
			KeyRotationInterval: 24 * time.Hour,
			ReplayWindowSize:    100000,
			TimestampTolerance:  60 * time.Second,
			MaxSeqNoGap:         10000,
		},
	}
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if !c.Enabled {
		return nil
	}

	if c.Port == 0 {
		return ErrInvalidPort
	}

	if c.QueueSize == 0 {
		return ErrInvalidQueueSize
	}

	if c.BatchSize == 0 {
		return ErrInvalidBatchSize
	}

	if c.Security.ReplayWindowSize <= 0 {
		return ErrInvalidReplayWindowSize
	}

	if c.Security.TimestampTolerance <= 0 {
		return ErrInvalidTimestampTolerance
	}

	return nil
}
