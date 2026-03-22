//go:build linux

package afxdp

import (
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/unix"
)

// UMEM represents User Memory - a shared memory region between kernel and userspace
// for AF_XDP packet data. The memory is divided into fixed-size frames.
type UMEM struct {
	// Memory region
	data     []byte
	dataPtr  unsafe.Pointer
	dataSize uint64

	// Frame management
	frameSize uint32
	numFrames uint32
	headroom  uint32

	// Free frame stack (lock-free)
	freeFrames []uint64 // Stack of free frame addresses
	freeTop    int64    // Top of stack index (atomic)
	mu         sync.Mutex

	// File descriptor for the UMEM
	fd int

	// State
	closed atomic.Bool
}

// NewUMEM creates a new UMEM region
func NewUMEM(config UMEMConfig) (*UMEM, error) {
	if config.NumFrames == 0 {
		config.NumFrames = DefaultNumFrames
	}
	if config.FrameSize == 0 {
		config.FrameSize = DefaultFrameSize
	}

	// Validate frame size is power of 2
	if !isPowerOfTwo(config.FrameSize) {
		return nil, ErrInvalidFrameSize
	}

	// Calculate total size
	totalSize := uint64(config.NumFrames) * uint64(config.FrameSize)

	// Allocate memory using mmap (aligned, suitable for DMA)
	data, err := unix.Mmap(-1, 0, int(totalSize),
		unix.PROT_READ|unix.PROT_WRITE,
		unix.MAP_PRIVATE|unix.MAP_ANONYMOUS|unix.MAP_POPULATE)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUmemMmap, err)
	}

	// Lock memory to prevent swapping (best effort)
	_ = unix.Mlock(data)

	u := &UMEM{
		data:       data,
		dataPtr:    unsafe.Pointer(&data[0]),
		dataSize:   totalSize,
		frameSize:  config.FrameSize,
		numFrames:  config.NumFrames,
		headroom:   config.Headroom,
		freeFrames: make([]uint64, config.NumFrames),
		freeTop:    int64(config.NumFrames),
	}

	// Initialize free frame stack with all frame addresses
	for i := uint32(0); i < config.NumFrames; i++ {
		u.freeFrames[i] = uint64(i) * uint64(config.FrameSize)
	}

	return u, nil
}

// Register registers the UMEM with an AF_XDP socket
func (u *UMEM) Register(sockFd int) error {
	reg := XDPUmemReg{
		Addr:      uint64(uintptr(u.dataPtr)),
		Len:       u.dataSize,
		ChunkSize: u.frameSize,
		Headroom:  u.headroom,
	}

	_, _, errno := unix.Syscall6(
		unix.SYS_SETSOCKOPT,
		uintptr(sockFd),
		SOL_XDP,
		XDP_UMEM_REG,
		uintptr(unsafe.Pointer(&reg)),
		unsafe.Sizeof(reg),
		0,
	)
	if errno != 0 {
		return fmt.Errorf("%w: %v", ErrUmemRegister, errno)
	}

	u.fd = sockFd
	return nil
}

// AllocFrame allocates a free frame and returns its address
// This is lock-free using atomic operations
func (u *UMEM) AllocFrame() (uint64, bool) {
	for {
		top := atomic.LoadInt64(&u.freeTop)
		if top <= 0 {
			return 0, false // No free frames
		}

		newTop := top - 1
		if atomic.CompareAndSwapInt64(&u.freeTop, top, newTop) {
			return u.freeFrames[newTop], true
		}
		// CAS failed, retry
	}
}

// AllocFrames allocates multiple frames at once
func (u *UMEM) AllocFrames(count int) ([]uint64, int) {
	u.mu.Lock()
	defer u.mu.Unlock()

	top := atomic.LoadInt64(&u.freeTop)
	available := int(top)
	if available == 0 {
		return nil, 0
	}

	if count > available {
		count = available
	}

	frames := make([]uint64, count)
	newTop := top - int64(count)

	for i := 0; i < count; i++ {
		frames[i] = u.freeFrames[newTop+int64(i)]
	}

	atomic.StoreInt64(&u.freeTop, newTop)
	return frames, count
}

// FreeFrame returns a frame to the free pool
func (u *UMEM) FreeFrame(addr uint64) bool {
	for {
		top := atomic.LoadInt64(&u.freeTop)
		if top >= int64(u.numFrames) {
			return false // Stack is full (shouldn't happen)
		}

		// Write the frame address BEFORE the CAS so that a concurrent
		// AllocFrame that sees the new freeTop will always find the
		// address already in place. If the CAS fails, the write is
		// harmless (it will be overwritten on the next successful CAS).
		u.freeFrames[top] = addr

		newTop := top + 1
		if atomic.CompareAndSwapInt64(&u.freeTop, top, newTop) {
			return true
		}
		// CAS failed, retry
	}
}

// FreeFrames returns multiple frames to the free pool
func (u *UMEM) FreeFrames(addrs []uint64) {
	u.mu.Lock()
	defer u.mu.Unlock()

	top := atomic.LoadInt64(&u.freeTop)

	for _, addr := range addrs {
		if top >= int64(u.numFrames) {
			break
		}
		u.freeFrames[top] = addr
		top++
	}

	atomic.StoreInt64(&u.freeTop, top)
}

// GetFrame returns a pointer to the frame data at the given address
func (u *UMEM) GetFrame(addr uint64) []byte {
	if addr >= u.dataSize {
		return nil
	}
	return u.data[addr : addr+uint64(u.frameSize)]
}

// GetFrameData returns the packet data within a frame (after headroom)
func (u *UMEM) GetFrameData(addr uint64) []byte {
	if addr >= u.dataSize {
		return nil
	}
	start := addr + uint64(u.headroom)
	return u.data[start : addr+uint64(u.frameSize)]
}

// WriteToFrame writes data to a frame at the given address
func (u *UMEM) WriteToFrame(addr uint64, data []byte) int {
	if addr >= u.dataSize {
		return 0
	}

	frame := u.data[addr+uint64(u.headroom):]
	maxLen := int(u.frameSize) - int(u.headroom)
	if len(data) > maxLen {
		data = data[:maxLen]
	}

	return copy(frame, data)
}

// ReadFromFrame reads data from a frame at the given address
func (u *UMEM) ReadFromFrame(addr uint64, length uint32) []byte {
	if addr >= u.dataSize {
		return nil
	}

	start := addr + uint64(u.headroom)
	end := start + uint64(length)
	if end > addr+uint64(u.frameSize) {
		end = addr + uint64(u.frameSize)
	}

	// Return a copy to avoid data races
	result := make([]byte, end-start)
	copy(result, u.data[start:end])
	return result
}

// FreeCount returns the number of free frames
func (u *UMEM) FreeCount() int {
	return int(atomic.LoadInt64(&u.freeTop))
}

// FrameSize returns the size of each frame
func (u *UMEM) FrameSize() uint32 {
	return u.frameSize
}

// NumFrames returns the total number of frames
func (u *UMEM) NumFrames() uint32 {
	return u.numFrames
}

// Headroom returns the headroom size
func (u *UMEM) Headroom() uint32 {
	return u.headroom
}

// DataPtr returns the base pointer of the UMEM data
func (u *UMEM) DataPtr() unsafe.Pointer {
	return u.dataPtr
}

// Close releases the UMEM memory
func (u *UMEM) Close() error {
	if u.closed.Swap(true) {
		return nil // Already closed
	}

	// Unlock memory
	_ = unix.Munlock(u.data)

	// Unmap memory
	if err := unix.Munmap(u.data); err != nil {
		return fmt.Errorf("failed to unmap UMEM: %w", err)
	}

	u.data = nil
	return nil
}
