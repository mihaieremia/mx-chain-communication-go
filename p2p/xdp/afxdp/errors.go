package afxdp

import "errors"

// AF_XDP specific errors
var (
	// Configuration errors
	ErrInvalidInterface   = errors.New("invalid interface name")
	ErrInvalidNumFrames   = errors.New("number of frames must be a power of 2")
	ErrInvalidFrameSize   = errors.New("frame size must be a power of 2")
	ErrInvalidRingSize    = errors.New("ring size must be a power of 2")
	ErrInvalidQueueID     = errors.New("invalid queue ID")

	// Socket errors
	ErrSocketCreate       = errors.New("failed to create AF_XDP socket")
	ErrSocketBind         = errors.New("failed to bind AF_XDP socket")
	ErrSocketClosed       = errors.New("socket is closed")
	ErrSocketOption       = errors.New("failed to set socket option")

	// UMEM errors
	ErrUmemCreate         = errors.New("failed to create UMEM")
	ErrUmemRegister       = errors.New("failed to register UMEM with socket")
	ErrUmemMmap           = errors.New("failed to mmap UMEM")
	ErrUmemFull           = errors.New("UMEM is full, no free frames available")

	// Ring errors
	ErrRingMmap           = errors.New("failed to mmap ring")
	ErrRingFull           = errors.New("ring is full")
	ErrRingEmpty          = errors.New("ring is empty")

	// XDP program errors
	ErrXDPLoad            = errors.New("failed to load XDP program")
	ErrXDPAttach          = errors.New("failed to attach XDP program")
	ErrXDPDetach          = errors.New("failed to detach XDP program")
	ErrXDPMapCreate       = errors.New("failed to create XSKMAP")
	ErrXDPMapUpdate       = errors.New("failed to update XSKMAP")

	// Operation errors
	ErrNotSupported       = errors.New("AF_XDP not supported on this platform")
	ErrNoFrames           = errors.New("no frames available for transmission")
	ErrPollFailed         = errors.New("poll failed")
	ErrSendFailed         = errors.New("send failed")
	ErrReceiveFailed      = errors.New("receive failed")
	ErrInterfaceNotFound  = errors.New("interface not found")
	ErrInterfaceDown      = errors.New("interface is down")
)
