//go:build linux

package afxdp

import (
	"fmt"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Ring represents an AF_XDP ring buffer (fill, completion, rx, or tx)
// Rings use a producer/consumer model with lock-free access via memory barriers.
type Ring struct {
	// Ring memory region (mmap'd)
	data []byte

	// Pointers into the ring
	producer  *uint32 // Producer index
	consumer  *uint32 // Consumer index
	flags     *uint32 // Ring flags (e.g., NEED_WAKEUP)
	ring      unsafe.Pointer // Base of the descriptor array

	// Ring configuration
	mask      uint32 // Size - 1 for fast modulo
	size      uint32
	cachedProd uint32 // Cached producer for batching
	cachedCons uint32 // Cached consumer for batching
}

// FillRing is used to provide empty frames to the kernel for receiving packets
type FillRing struct {
	Ring
}

// CompletionRing is used to receive frames back after TX completion
type CompletionRing struct {
	Ring
}

// RxRing is used to receive packet descriptors from the kernel
type RxRing struct {
	Ring
}

// TxRing is used to submit packet descriptors to the kernel for transmission
type TxRing struct {
	Ring
}

// newRing creates a new ring from mmap'd memory
func newRing(data []byte, offset XDPRingOffset, size uint32) *Ring {
	r := &Ring{
		data: data,
		size: size,
		mask: size - 1,
	}

	r.producer = (*uint32)(unsafe.Pointer(&data[offset.Producer]))
	r.consumer = (*uint32)(unsafe.Pointer(&data[offset.Consumer]))
	r.flags = (*uint32)(unsafe.Pointer(&data[offset.Flags]))
	r.ring = unsafe.Pointer(&data[offset.Desc])

	return r
}

// NewFillRing creates a fill ring from mmap'd memory
func NewFillRing(sockFd int, size uint32) (*FillRing, error) {
	// Set fill ring size
	if err := setSockoptInt(sockFd, SOL_XDP, XDP_UMEM_FILL_RING, int(size)); err != nil {
		return nil, fmt.Errorf("failed to set fill ring size: %w", err)
	}

	// Get mmap offsets
	offsets, err := getMmapOffsets(sockFd)
	if err != nil {
		return nil, err
	}

	// Calculate mmap size
	mmapSize := offsets.Fr.Desc + uint64(size)*8 // uint64 per frame address

	// Mmap the fill ring
	data, err := unix.Mmap(sockFd, int64(unix.XDP_UMEM_PGOFF_FILL_RING),
		int(mmapSize), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED|unix.MAP_POPULATE)
	if err != nil {
		return nil, fmt.Errorf("%w: fill ring: %v", ErrRingMmap, err)
	}

	return &FillRing{Ring: *newRing(data, offsets.Fr, size)}, nil
}

// NewCompletionRing creates a completion ring from mmap'd memory
func NewCompletionRing(sockFd int, size uint32) (*CompletionRing, error) {
	// Set completion ring size
	if err := setSockoptInt(sockFd, SOL_XDP, XDP_UMEM_COMPLETION_RING, int(size)); err != nil {
		return nil, fmt.Errorf("failed to set completion ring size: %w", err)
	}

	// Get mmap offsets
	offsets, err := getMmapOffsets(sockFd)
	if err != nil {
		return nil, err
	}

	// Calculate mmap size
	mmapSize := offsets.Cr.Desc + uint64(size)*8

	// Mmap the completion ring
	data, err := unix.Mmap(sockFd, int64(unix.XDP_UMEM_PGOFF_COMPLETION_RING),
		int(mmapSize), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED|unix.MAP_POPULATE)
	if err != nil {
		return nil, fmt.Errorf("%w: completion ring: %v", ErrRingMmap, err)
	}

	return &CompletionRing{Ring: *newRing(data, offsets.Cr, size)}, nil
}

// NewRxRing creates an RX ring from mmap'd memory
func NewRxRing(sockFd int, size uint32) (*RxRing, error) {
	// Set RX ring size
	if err := setSockoptInt(sockFd, SOL_XDP, XDP_RX_RING, int(size)); err != nil {
		return nil, fmt.Errorf("failed to set RX ring size: %w", err)
	}

	// Get mmap offsets
	offsets, err := getMmapOffsets(sockFd)
	if err != nil {
		return nil, err
	}

	// Calculate mmap size
	mmapSize := offsets.Rx.Desc + uint64(size)*uint64(SizeOfXDPDesc)

	// Mmap the RX ring
	data, err := unix.Mmap(sockFd, int64(unix.XDP_PGOFF_RX_RING),
		int(mmapSize), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED|unix.MAP_POPULATE)
	if err != nil {
		return nil, fmt.Errorf("%w: RX ring: %v", ErrRingMmap, err)
	}

	return &RxRing{Ring: *newRing(data, offsets.Rx, size)}, nil
}

// NewTxRing creates a TX ring from mmap'd memory
func NewTxRing(sockFd int, size uint32) (*TxRing, error) {
	// Set TX ring size
	if err := setSockoptInt(sockFd, SOL_XDP, XDP_TX_RING, int(size)); err != nil {
		return nil, fmt.Errorf("failed to set TX ring size: %w", err)
	}

	// Get mmap offsets
	offsets, err := getMmapOffsets(sockFd)
	if err != nil {
		return nil, err
	}

	// Calculate mmap size
	mmapSize := offsets.Tx.Desc + uint64(size)*uint64(SizeOfXDPDesc)

	// Mmap the TX ring
	data, err := unix.Mmap(sockFd, int64(unix.XDP_PGOFF_TX_RING),
		int(mmapSize), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED|unix.MAP_POPULATE)
	if err != nil {
		return nil, fmt.Errorf("%w: TX ring: %v", ErrRingMmap, err)
	}

	return &TxRing{Ring: *newRing(data, offsets.Tx, size)}, nil
}

// getMmapOffsets retrieves mmap offsets from the socket
func getMmapOffsets(sockFd int) (*XDPMmapOffsets, error) {
	var offsets XDPMmapOffsets
	size := uint32(unsafe.Sizeof(offsets))

	_, _, errno := unix.Syscall6(
		unix.SYS_GETSOCKOPT,
		uintptr(sockFd),
		SOL_XDP,
		XDP_MMAP_OFFSETS,
		uintptr(unsafe.Pointer(&offsets)),
		uintptr(unsafe.Pointer(&size)),
		0,
	)
	if errno != 0 {
		return nil, fmt.Errorf("getsockopt XDP_MMAP_OFFSETS failed: %v", errno)
	}

	return &offsets, nil
}

// setSockoptInt sets an integer socket option
func setSockoptInt(fd int, level, opt, value int) error {
	return unix.SetsockoptInt(fd, level, opt, value)
}

// FillRing methods

// Produce adds frame addresses to the fill ring for the kernel to use
func (r *FillRing) Produce(addrs []uint64) int {
	// Load consumer with acquire semantics
	cons := atomic.LoadUint32(r.consumer)
	prod := r.cachedProd

	// Calculate free space
	free := r.size - (prod - cons)
	if free == 0 {
		return 0
	}

	count := len(addrs)
	if uint32(count) > free {
		count = int(free)
	}

	// Get pointer to the ring entries (uint64 array for fill ring)
	ring := (*[1 << 20]uint64)(r.ring)

	// Write frame addresses
	for i := 0; i < count; i++ {
		idx := (prod + uint32(i)) & r.mask
		ring[idx] = addrs[i]
	}

	// Memory barrier before updating producer
	atomic.StoreUint32(r.producer, prod+uint32(count))
	r.cachedProd = prod + uint32(count)

	return count
}

// NeedWakeup returns true if the kernel needs to be woken up
func (r *FillRing) NeedWakeup() bool {
	return atomic.LoadUint32(r.flags)&XDP_RING_NEED_WAKEUP != 0
}

// CompletionRing methods

// Consume retrieves completed frame addresses from the completion ring
func (r *CompletionRing) Consume(addrs []uint64) int {
	// Load producer with acquire semantics
	prod := atomic.LoadUint32(r.producer)
	cons := r.cachedCons

	// Calculate available entries
	available := prod - cons
	if available == 0 {
		return 0
	}

	count := len(addrs)
	if uint32(count) > available {
		count = int(available)
	}

	// Get pointer to the ring entries
	ring := (*[1 << 20]uint64)(r.ring)

	// Read frame addresses
	for i := 0; i < count; i++ {
		idx := (cons + uint32(i)) & r.mask
		addrs[i] = ring[idx]
	}

	// Memory barrier before updating consumer
	atomic.StoreUint32(r.consumer, cons+uint32(count))
	r.cachedCons = cons + uint32(count)

	return count
}

// RxRing methods

// Consume retrieves received packet descriptors from the RX ring
func (r *RxRing) Consume(descs []XDPDesc) int {
	// Load producer with acquire semantics
	prod := atomic.LoadUint32(r.producer)
	cons := r.cachedCons

	// Calculate available entries
	available := prod - cons
	if available == 0 {
		return 0
	}

	count := len(descs)
	if uint32(count) > available {
		count = int(available)
	}

	// Get pointer to the ring entries
	ring := (*[1 << 20]XDPDesc)(r.ring)

	// Read descriptors
	for i := 0; i < count; i++ {
		idx := (cons + uint32(i)) & r.mask
		descs[i] = ring[idx]
	}

	// Memory barrier before updating consumer
	atomic.StoreUint32(r.consumer, cons+uint32(count))
	r.cachedCons = cons + uint32(count)

	return count
}

// NeedWakeup returns true if the kernel needs to be woken up
func (r *RxRing) NeedWakeup() bool {
	return atomic.LoadUint32(r.flags)&XDP_RING_NEED_WAKEUP != 0
}

// TxRing methods

// Produce submits packet descriptors to the TX ring
func (r *TxRing) Produce(descs []XDPDesc) int {
	// Load consumer with acquire semantics
	cons := atomic.LoadUint32(r.consumer)
	prod := r.cachedProd

	// Calculate free space
	free := r.size - (prod - cons)
	if free == 0 {
		return 0
	}

	count := len(descs)
	if uint32(count) > free {
		count = int(free)
	}

	// Get pointer to the ring entries
	ring := (*[1 << 20]XDPDesc)(r.ring)

	// Write descriptors
	for i := 0; i < count; i++ {
		idx := (prod + uint32(i)) & r.mask
		ring[idx] = descs[i]
	}

	// Memory barrier before updating producer
	atomic.StoreUint32(r.producer, prod+uint32(count))
	r.cachedProd = prod + uint32(count)

	return count
}

// NeedWakeup returns true if the kernel needs to be woken up
func (r *TxRing) NeedWakeup() bool {
	return atomic.LoadUint32(r.flags)&XDP_RING_NEED_WAKEUP != 0
}

// Close unmaps the ring memory
func (r *Ring) Close() error {
	if r.data != nil {
		if err := unix.Munmap(r.data); err != nil {
			return fmt.Errorf("failed to unmap ring: %w", err)
		}
		r.data = nil
	}
	return nil
}
