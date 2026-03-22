//go:build linux

package afxdp

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Socket represents an AF_XDP socket for high-performance packet I/O
type Socket struct {
	// Socket file descriptor
	fd int

	// UMEM (shared memory)
	umem *UMEM

	// Ring buffers
	fillRing *FillRing
	compRing *CompletionRing
	rxRing   *RxRing
	txRing   *TxRing

	// Configuration
	config Config

	// Interface info
	ifindex  int
	ifname   string
	queueID  int

	// Batching buffers (pre-allocated to avoid GC)
	rxDescs    []XDPDesc
	txDescs    []XDPDesc
	fillAddrs  []uint64
	compAddrs  []uint64

	// State
	closed atomic.Bool
	mu     sync.Mutex

	// Statistics
	rxPackets atomic.Uint64
	txPackets atomic.Uint64
	rxBytes   atomic.Uint64
	txBytes   atomic.Uint64
	rxDrops   atomic.Uint64
	txDrops   atomic.Uint64
}

// New creates a new AF_XDP socket
func New(config Config) (*Socket, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	// Get interface index
	iface, err := net.InterfaceByName(config.Interface)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInterfaceNotFound, err)
	}

	if iface.Flags&net.FlagUp == 0 {
		return nil, ErrInterfaceDown
	}

	s := &Socket{
		fd:      -1,
		config:  config,
		ifindex: iface.Index,
		ifname:  config.Interface,
		queueID: config.QueueID,
	}

	// Create AF_XDP socket
	fd, err := unix.Socket(AF_XDP, unix.SOCK_RAW, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSocketCreate, err)
	}
	s.fd = fd

	// Create UMEM
	umemConfig := UMEMConfig{
		NumFrames: config.NumFrames,
		FrameSize: config.FrameSize,
	}
	s.umem, err = NewUMEM(umemConfig)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}

	// Register UMEM with socket
	if err := s.umem.Register(fd); err != nil {
		s.umem.Close()
		unix.Close(fd)
		return nil, err
	}

	// Create rings
	if err := s.createRings(); err != nil {
		s.umem.Close()
		unix.Close(fd)
		return nil, err
	}

	// Bind socket to interface and queue
	if err := s.bind(); err != nil {
		s.closeRings()
		s.umem.Close()
		unix.Close(fd)
		return nil, err
	}

	// Pre-allocate batching buffers
	s.rxDescs = make([]XDPDesc, config.BatchSize)
	s.txDescs = make([]XDPDesc, config.BatchSize)
	s.fillAddrs = make([]uint64, config.BatchSize)
	s.compAddrs = make([]uint64, config.BatchSize)

	// Pre-fill the fill ring with frames
	if err := s.prefillRxBuffers(); err != nil {
		s.Close()
		return nil, err
	}

	return s, nil
}

// createRings creates all the ring buffers
func (s *Socket) createRings() error {
	var err error

	// Create fill ring
	s.fillRing, err = NewFillRing(s.fd, s.config.FillRingSize)
	if err != nil {
		return err
	}

	// Create completion ring
	s.compRing, err = NewCompletionRing(s.fd, s.config.CompRingSize)
	if err != nil {
		s.fillRing.Close()
		s.fillRing = nil
		return err
	}

	// Create RX ring
	s.rxRing, err = NewRxRing(s.fd, s.config.RxRingSize)
	if err != nil {
		s.compRing.Close()
		s.compRing = nil
		s.fillRing.Close()
		s.fillRing = nil
		return err
	}

	// Create TX ring
	s.txRing, err = NewTxRing(s.fd, s.config.TxRingSize)
	if err != nil {
		s.rxRing.Close()
		s.rxRing = nil
		s.compRing.Close()
		s.compRing = nil
		s.fillRing.Close()
		s.fillRing = nil
		return err
	}

	return nil
}

// bind binds the socket to the interface and queue
func (s *Socket) bind() error {
	sa := SockaddrXDP{
		Family:  AF_XDP,
		Ifindex: uint32(s.ifindex),
		QueueID: uint32(s.queueID),
	}

	// Set bind flags
	if s.config.ZeroCopy {
		sa.Flags |= XDP_ZEROCOPY
	} else {
		sa.Flags |= XDP_COPY
	}

	if s.config.NeedWakeup {
		sa.Flags |= XDP_USE_NEED_WAKEUP
	}

	_, _, errno := unix.Syscall(
		unix.SYS_BIND,
		uintptr(s.fd),
		uintptr(unsafe.Pointer(&sa)),
		unsafe.Sizeof(sa),
	)
	if errno != 0 {
		return fmt.Errorf("%w: %v", ErrSocketBind, errno)
	}

	return nil
}

// prefillRxBuffers pre-fills the fill ring with buffers for receiving
func (s *Socket) prefillRxBuffers() error {
	// Allocate frames for RX
	numFrames := int(s.config.FillRingSize)
	frames, allocated := s.umem.AllocFrames(numFrames)
	if allocated == 0 {
		return ErrNoFrames
	}

	// Add frames to fill ring
	produced := s.fillRing.Produce(frames)
	if produced == 0 {
		// Return frames to pool
		s.umem.FreeFrames(frames)
		return ErrRingFull
	}

	// If we didn't produce all, return the rest
	if produced < allocated {
		s.umem.FreeFrames(frames[produced:])
	}

	return nil
}

// Receive receives packets from the socket
// Returns the number of packets received
func (s *Socket) Receive(packets [][]byte) (int, error) {
	if s.closed.Load() {
		return 0, ErrSocketClosed
	}

	// Consume from RX ring
	count := s.rxRing.Consume(s.rxDescs[:len(packets)])
	if count == 0 {
		// Check if we need to wakeup kernel
		if s.config.NeedWakeup && s.fillRing.NeedWakeup() {
			if err := s.wakeup(); err != nil {
				return 0, err
			}
		}
		return 0, nil
	}

	// Copy packet data
	for i := 0; i < count; i++ {
		desc := s.rxDescs[i]
		data := s.umem.ReadFromFrame(desc.Addr, desc.Len)
		packets[i] = data
		s.rxBytes.Add(uint64(desc.Len))
	}

	s.rxPackets.Add(uint64(count))

	// Return frames to fill ring for reuse
	s.refillRxBuffers(count)

	return count, nil
}

// ReceiveOne receives a single packet (convenience method)
func (s *Socket) ReceiveOne() ([]byte, error) {
	packets := make([][]byte, 1)
	n, err := s.Receive(packets)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}
	return packets[0], nil
}

// refillRxBuffers refills the fill ring with frames
func (s *Socket) refillRxBuffers(count int) {
	// Get addresses from completed RX
	for i := 0; i < count; i++ {
		s.fillAddrs[i] = s.rxDescs[i].Addr
	}

	// Add back to fill ring
	s.fillRing.Produce(s.fillAddrs[:count])
}

// Send sends packets through the socket
// Returns the number of packets sent
func (s *Socket) Send(packets [][]byte) (int, error) {
	if s.closed.Load() {
		return 0, ErrSocketClosed
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// First, reclaim completed TX frames
	s.reclaimTxFrames()

	// Allocate frames for TX
	count := len(packets)
	frames, allocated := s.umem.AllocFrames(count)
	if allocated == 0 {
		s.txDrops.Add(uint64(count))
		return 0, ErrNoFrames
	}

	if allocated < count {
		count = allocated
		s.txDrops.Add(uint64(len(packets) - count))
	}

	// Prepare TX descriptors
	for i := 0; i < count; i++ {
		// Write packet data to frame
		n := s.umem.WriteToFrame(frames[i], packets[i])

		s.txDescs[i] = XDPDesc{
			Addr: frames[i],
			Len:  uint32(n),
		}
		s.txBytes.Add(uint64(n))
	}

	// Submit to TX ring
	produced := s.txRing.Produce(s.txDescs[:count])
	if produced == 0 {
		// Return frames to pool
		s.umem.FreeFrames(frames[:count])
		return 0, ErrRingFull
	}

	// If we didn't produce all, return the rest
	if produced < count {
		s.umem.FreeFrames(frames[produced:count])
	}

	s.txPackets.Add(uint64(produced))

	// Kick the kernel to transmit
	if s.config.NeedWakeup && s.txRing.NeedWakeup() {
		if err := s.wakeup(); err != nil {
			return produced, err
		}
	}

	return produced, nil
}

// SendOne sends a single packet (convenience method)
func (s *Socket) SendOne(data []byte) error {
	packets := [][]byte{data}
	n, err := s.Send(packets)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrSendFailed
	}
	return nil
}

// reclaimTxFrames reclaims completed TX frames
func (s *Socket) reclaimTxFrames() {
	// Consume from completion ring
	count := s.compRing.Consume(s.compAddrs[:s.config.BatchSize])
	if count > 0 {
		// Return frames to UMEM pool
		s.umem.FreeFrames(s.compAddrs[:count])
	}
}

// wakeup wakes up the kernel (for NEED_WAKEUP mode)
func (s *Socket) wakeup() error {
	_, err := unix.Sendto(s.fd, nil, unix.MSG_DONTWAIT, nil)
	if err != nil && err != unix.EAGAIN && err != unix.ENOBUFS {
		return fmt.Errorf("wakeup sendto failed: %w", err)
	}
	return nil
}

// Poll polls for events with timeout (milliseconds)
func (s *Socket) Poll(timeout int) (bool, bool, error) {
	if s.closed.Load() {
		return false, false, ErrSocketClosed
	}

	fds := []unix.PollFd{{
		Fd:     int32(s.fd),
		Events: unix.POLLIN | unix.POLLOUT,
	}}

	n, err := unix.Poll(fds, timeout)
	if err != nil {
		if err == unix.EINTR {
			return false, false, nil // Interrupted, not an error
		}
		return false, false, fmt.Errorf("%w: %v", ErrPollFailed, err)
	}

	if n == 0 {
		return false, false, nil // Timeout
	}

	canRead := fds[0].Revents&unix.POLLIN != 0
	canWrite := fds[0].Revents&unix.POLLOUT != 0

	return canRead, canWrite, nil
}

// Fd returns the socket file descriptor (for XSK map registration)
func (s *Socket) Fd() int {
	return s.fd
}

// InterfaceIndex returns the interface index
func (s *Socket) InterfaceIndex() int {
	return s.ifindex
}

// QueueID returns the queue ID
func (s *Socket) QueueID() int {
	return s.queueID
}

// GetStats returns current statistics
func (s *Socket) GetStats() Stats {
	return Stats{
		RxPackets:  s.rxPackets.Load(),
		TxPackets:  s.txPackets.Load(),
		RxBytes:    s.rxBytes.Load(),
		TxBytes:    s.txBytes.Load(),
		RxDrops:    s.rxDrops.Load(),
		TxDrops:    s.txDrops.Load(),
		FreeFrames: s.umem.FreeCount(),
	}
}

// closeRings closes all ring buffers
func (s *Socket) closeRings() {
	if s.fillRing != nil {
		s.fillRing.Close()
	}
	if s.compRing != nil {
		s.compRing.Close()
	}
	if s.rxRing != nil {
		s.rxRing.Close()
	}
	if s.txRing != nil {
		s.txRing.Close()
	}
}

// Close closes the socket and releases all resources
func (s *Socket) Close() error {
	if s.closed.Swap(true) {
		return nil // Already closed
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Close rings first
	s.closeRings()

	// Close UMEM
	if s.umem != nil {
		s.umem.Close()
	}

	// Close socket fd
	if s.fd >= 0 {
		unix.Close(s.fd)
		s.fd = -1
	}

	return nil
}
