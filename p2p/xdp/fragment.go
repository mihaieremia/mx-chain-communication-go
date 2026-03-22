package xdp

import (
	"sync"
	"time"
)

const (
	// DefaultFragmentTimeout is the default timeout for fragment reassembly
	DefaultFragmentTimeout = 30 * time.Second

	// MaxFragments is the maximum number of fragments per message
	MaxFragments = (MaxMessageSize / MaxPayloadSize) + 1
)

// fragmentKey uniquely identifies a fragmented message
type fragmentKey struct {
	peerID [32]byte
	seqNo  uint64
}

// fragmentState tracks the state of fragment reassembly
type fragmentState struct {
	total     uint16
	received  uint16
	fragments [][]byte
	timestamp time.Time
	complete  bool
}

// FragmentAssembler handles message fragmentation and reassembly
type FragmentAssembler struct {
	mu        sync.RWMutex
	states    map[fragmentKey]*fragmentState
	timeout   time.Duration
	stopChan  chan struct{}
	closeOnce sync.Once
}

// NewFragmentAssembler creates a new fragment assembler
func NewFragmentAssembler(timeout time.Duration) *FragmentAssembler {
	if timeout <= 0 {
		timeout = DefaultFragmentTimeout
	}

	fa := &FragmentAssembler{
		states:   make(map[fragmentKey]*fragmentState),
		timeout:  timeout,
		stopChan: make(chan struct{}),
	}

	go fa.cleanupLoop()

	return fa
}

// Fragment splits a message into multiple fragments
func Fragment(data []byte, seqNo uint64, peerID [32]byte) ([]*Packet, error) {
	if len(data) == 0 {
		return nil, ErrInvalidPacket
	}

	if len(data) > MaxMessageSize {
		return nil, ErrPacketTooLarge
	}

	// If the data fits in a single packet, no fragmentation needed
	if len(data) <= MaxPayloadSize {
		p := NewPacket()
		p.SeqNo = seqNo
		p.PeerID = peerID
		p.FragTotal = 1
		p.FragIndex = 0
		if err := p.SetPayload(data); err != nil {
			return nil, err
		}
		return []*Packet{p}, nil
	}

	// Calculate number of fragments needed
	numFragments := (len(data) + MaxPayloadSize - 1) / MaxPayloadSize
	if numFragments > MaxFragments {
		return nil, ErrPacketTooLarge
	}

	packets := make([]*Packet, numFragments)

	for i := 0; i < numFragments; i++ {
		start := i * MaxPayloadSize
		end := start + MaxPayloadSize
		if end > len(data) {
			end = len(data)
		}

		p := NewPacket()
		p.SeqNo = seqNo
		p.PeerID = peerID
		p.SetFragment(uint16(numFragments), uint16(i))

		if err := p.SetPayload(data[start:end]); err != nil {
			return nil, err
		}

		packets[i] = p
	}

	return packets, nil
}

// AddFragment adds a fragment and returns the complete message if all fragments are received
func (fa *FragmentAssembler) AddFragment(p *Packet) ([]byte, bool, error) {
	if p.FragTotal == 0 {
		return nil, false, ErrInvalidFragment
	}

	// Single packet message (no fragmentation)
	if p.FragTotal == 1 && p.FragIndex == 0 {
		return p.Payload, true, nil
	}

	if p.FragIndex >= p.FragTotal {
		return nil, false, ErrInvalidFragment
	}

	if p.FragTotal > MaxFragments {
		return nil, false, ErrPacketTooLarge
	}

	key := fragmentKey{
		peerID: p.PeerID,
		seqNo:  p.SeqNo,
	}

	fa.mu.Lock()
	defer fa.mu.Unlock()

	state, exists := fa.states[key]
	if !exists {
		state = &fragmentState{
			total:     p.FragTotal,
			fragments: make([][]byte, p.FragTotal),
			timestamp: time.Now(),
		}
		fa.states[key] = state
	}

	// Check for consistency
	if state.total != p.FragTotal {
		return nil, false, ErrInvalidFragment
	}

	// Store fragment if not already received
	if state.fragments[p.FragIndex] == nil {
		state.fragments[p.FragIndex] = make([]byte, len(p.Payload))
		copy(state.fragments[p.FragIndex], p.Payload)
		state.received++
	}

	// Check if complete
	if state.received == state.total {
		// Calculate total size
		totalSize := 0
		for _, frag := range state.fragments {
			totalSize += len(frag)
		}

		// Reassemble
		data := make([]byte, 0, totalSize)
		for _, frag := range state.fragments {
			data = append(data, frag...)
		}

		// Clean up
		delete(fa.states, key)

		return data, true, nil
	}

	return nil, false, nil
}

// cleanupLoop periodically removes timed-out fragment states
func (fa *FragmentAssembler) cleanupLoop() {
	ticker := time.NewTicker(fa.timeout / 2)
	defer ticker.Stop()

	for {
		select {
		case <-fa.stopChan:
			return
		case <-ticker.C:
			fa.cleanup()
		}
	}
}

// cleanup removes timed-out fragment states
func (fa *FragmentAssembler) cleanup() {
	fa.mu.Lock()
	defer fa.mu.Unlock()

	now := time.Now()
	for key, state := range fa.states {
		if now.Sub(state.timestamp) > fa.timeout {
			delete(fa.states, key)
		}
	}
}

// PendingCount returns the number of pending fragment assemblies
func (fa *FragmentAssembler) PendingCount() int {
	fa.mu.RLock()
	defer fa.mu.RUnlock()
	return len(fa.states)
}

// Close stops the cleanup goroutine
func (fa *FragmentAssembler) Close() error {
	fa.closeOnce.Do(func() { close(fa.stopChan) })
	return nil
}
