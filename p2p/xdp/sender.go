package xdp

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/multiversx/mx-chain-communication-go/p2p"
	"github.com/multiversx/mx-chain-communication-go/p2p/xdp/crypto"
	"github.com/multiversx/mx-chain-communication-go/p2p/xdp/peer"
	"github.com/multiversx/mx-chain-core-go/core"
)

// Compile-time check for unused import
var _ = fmt.Errorf

// Sender handles XDP packet transmission
type Sender struct {
	mu sync.RWMutex

	socket        *Socket
	peerManager   *peer.Manager
	fragmenter    *FragmentAssembler
	topicRegistry *TopicRegistry
	authenticator *crypto.Authenticator

	// Sequence number counter per destination
	seqCounters sync.Map

	// Batch sending
	batchSize  int
	batchQueue chan *sendRequest
	stopChan   chan struct{}

	// Graceful shutdown
	wg        sync.WaitGroup
	closeOnce sync.Once

	// Stats
	messagesSent    uint64
	messagesDropped uint64

	log p2p.Logger
}

// sendRequest represents a pending send request
type sendRequest struct {
	topic    string
	data     []byte
	peerID   core.PeerID
	peerAddr *net.UDPAddr
	msgType  byte
	flags    byte
}

// SenderConfig holds sender configuration
type SenderConfig struct {
	BatchSize     int
	QueueSize     int
	FlushInterval time.Duration
}

// DefaultSenderConfig returns default sender configuration
func DefaultSenderConfig() SenderConfig {
	return SenderConfig{
		BatchSize:     64,
		QueueSize:     10000,
		FlushInterval: time.Millisecond,
	}
}

// NewSender creates a new XDP sender
func NewSender(
	socket *Socket,
	peerManager *peer.Manager,
	sessionManager *crypto.SessionManager,
	topicRegistry *TopicRegistry,
	config SenderConfig,
	log p2p.Logger,
) (*Sender, error) {
	if socket == nil {
		return nil, ErrSocketNotInitialized
	}
	if peerManager == nil {
		return nil, ErrNilPeerManager
	}
	if log == nil {
		return nil, ErrNilLogger
	}

	s := &Sender{
		socket:        socket,
		peerManager:   peerManager,
		fragmenter:    NewFragmentAssembler(DefaultFragmentTimeout),
		topicRegistry: topicRegistry,
		authenticator: crypto.NewAuthenticator(sessionManager),
		batchSize:     config.BatchSize,
		batchQueue:    make(chan *sendRequest, config.QueueSize),
		stopChan:      make(chan struct{}),
		log:           log,
	}

	// Start batch sender goroutine with WaitGroup tracking
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.batchSendLoop(config.FlushInterval)
	}()

	return s, nil
}

// Send sends a message to a specific peer
func (s *Sender) Send(topic string, data []byte, peerID core.PeerID) error {
	// Get peer's XDP address
	addr, ok := s.peerManager.GetXDPAddress(peerID)
	if !ok {
		return ErrPeerNotXDPCapable
	}

	return s.sendDirect(topic, data, peerID, addr, FlagDirect, MsgTypeGeneric)
}

// SendWithType sends a message with a specific message type
func (s *Sender) SendWithType(topic string, data []byte, peerID core.PeerID, msgType byte) error {
	addr, ok := s.peerManager.GetXDPAddress(peerID)
	if !ok {
		return ErrPeerNotXDPCapable
	}

	return s.sendDirect(topic, data, peerID, addr, FlagDirect, msgType)
}

// sendDirect sends a message directly without queueing
func (s *Sender) sendDirect(topic string, data []byte, peerID core.PeerID, addr *net.UDPAddr, flags byte, msgType byte) error {
	// Get peer ID bytes (truncated to 32 bytes)
	var peerIDBytes [32]byte
	copy(peerIDBytes[:], []byte(peerID))

	// Get next sequence number for this peer
	seqNo := s.nextSeqNo(peerID)

	// Fragment if necessary
	packets, err := Fragment(data, seqNo, peerIDBytes)
	if err != nil {
		return err
	}

	// Get topic ID
	topicID := s.topicRegistry.Register(topic)

	// Send each packet
	for _, pkt := range packets {
		pkt.TopicID = topicID
		pkt.MsgType = msgType
		pkt.Flags |= flags

		// Sign the packet using the full PeerID for correct session lookup
		hmacData := pkt.EncodeForHMAC()
		hmac, signErr := s.authenticator.SignWithPeerID(peerID, hmacData)
		if signErr != nil {
			s.log.Trace("failed to sign packet", "error", signErr, "peer", peerID.Pretty())
			return signErr
		}
		pkt.HMAC = hmac

		// Encode and send
		encoded, err := pkt.Encode()
		if err != nil {
			s.log.Trace("failed to encode packet", "error", err)
			continue
		}

		if err := s.socket.Send(encoded, addr); err != nil {
			s.log.Trace("failed to send packet", "error", err, "peer", peerID.Pretty())
			return err
		}
	}

	s.mu.Lock()
	s.messagesSent++
	s.mu.Unlock()

	return nil
}

// SendAsync queues a message for batch sending
func (s *Sender) SendAsync(topic string, data []byte, peerID core.PeerID) error {
	addr, ok := s.peerManager.GetXDPAddress(peerID)
	if !ok {
		return ErrPeerNotXDPCapable
	}

	req := &sendRequest{
		topic:    topic,
		data:     data,
		peerID:   peerID,
		peerAddr: addr,
		msgType:  MsgTypeGeneric,
		flags:    FlagDirect,
	}

	select {
	case s.batchQueue <- req:
		return nil
	default:
		s.mu.Lock()
		s.messagesDropped++
		s.mu.Unlock()
		return fmt.Errorf("send queue full")
	}
}

// Broadcast sends a message to multiple peers
func (s *Sender) Broadcast(topic string, data []byte, peerIDs []core.PeerID) error {
	var lastErr error

	for _, peerID := range peerIDs {
		addr, ok := s.peerManager.GetXDPAddress(peerID)
		if !ok {
			continue
		}

		if err := s.sendDirect(topic, data, peerID, addr, FlagBroadcast, MsgTypeGeneric); err != nil {
			lastErr = err
			s.log.Trace("broadcast send failed", "peer", peerID.Pretty(), "error", err)
		}
	}

	return lastErr
}

// batchSendLoop processes the batch queue
func (s *Sender) batchSendLoop(flushInterval time.Duration) {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	batch := make([]*sendRequest, 0, s.batchSize)

	for {
		select {
		case <-s.stopChan:
			// Flush remaining
			s.processBatch(batch)
			return

		case req := <-s.batchQueue:
			batch = append(batch, req)
			if len(batch) >= s.batchSize {
				s.processBatch(batch)
				batch = batch[:0]
			}

		case <-ticker.C:
			if len(batch) > 0 {
				s.processBatch(batch)
				batch = batch[:0]
			}
		}
	}
}

// processBatch sends a batch of messages
func (s *Sender) processBatch(batch []*sendRequest) {
	for _, req := range batch {
		if err := s.sendDirect(req.topic, req.data, req.peerID, req.peerAddr, req.flags, req.msgType); err != nil {
			s.log.Trace("batch send failed", "error", err)
		}
	}
}

// nextSeqNo returns the next sequence number for a peer
func (s *Sender) nextSeqNo(peerID core.PeerID) uint64 {
	key := string(peerID)

	// LoadOrStore returns the existing counter or creates a new one
	val, _ := s.seqCounters.LoadOrStore(key, new(uint64))
	counter := val.(*uint64)

	// Use atomic increment directly on the counter value
	// This is correct because we're always operating on the same pointer
	return atomic.AddUint64(counter, 1)
}

// GetStats returns sender statistics
func (s *Sender) GetStats() SenderStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return SenderStats{
		MessagesSent:    s.messagesSent,
		MessagesDropped: s.messagesDropped,
		QueueLength:     len(s.batchQueue),
	}
}

// SenderStats contains sender statistics
type SenderStats struct {
	MessagesSent    uint64
	MessagesDropped uint64
	QueueLength     int
}

// Close closes the sender and waits for all goroutines to finish
func (s *Sender) Close() error {
	s.closeOnce.Do(func() { close(s.stopChan) })

	// Wait for batchSendLoop goroutine to finish (safe to call multiple times)
	s.wg.Wait()

	// fragmenter.Close() is idempotent (also guarded by sync.Once)
	s.fragmenter.Close()
	return nil
}
