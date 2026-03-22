package xdp

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/multiversx/mx-chain-communication-go/p2p"
	"github.com/multiversx/mx-chain-communication-go/p2p/xdp/crypto"
	"github.com/multiversx/mx-chain-communication-go/p2p/xdp/peer"
	"github.com/multiversx/mx-chain-core-go/core"
)

// MessageHandler is a callback function for handling received XDP messages
type MessageHandler func(topic string, data []byte, peerID core.PeerID)

// Receiver handles XDP packet reception
type Receiver struct {
	mu sync.RWMutex

	socket          *Socket
	peerManager     *peer.Manager
	fragmenter      *FragmentAssembler
	topicRegistry   *TopicRegistry
	replayProtector *ReplayProtector
	authenticator   *crypto.Authenticator

	// Message handler callback
	messageHandler MessageHandler

	// Worker configuration
	numWorkers int
	workQueue  chan *receivedPacket

	// State
	running atomic.Bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// Stats
	messagesReceived atomic.Uint64
	messagesDropped  atomic.Uint64
	authFailures     atomic.Uint64
	replayBlocked    atomic.Uint64

	log p2p.Logger
}

// receivedPacket represents a received packet pending processing
type receivedPacket struct {
	data []byte
	addr *net.UDPAddr
}

// ReceiverConfig holds receiver configuration
type ReceiverConfig struct {
	NumWorkers    int
	QueueSize     int
	BufferSize    int
}

// DefaultReceiverConfig returns default receiver configuration
func DefaultReceiverConfig() ReceiverConfig {
	return ReceiverConfig{
		NumWorkers:    4,
		QueueSize:     10000,
		BufferSize:    65536,
	}
}

// NewReceiver creates a new XDP receiver
func NewReceiver(
	socket *Socket,
	peerManager *peer.Manager,
	sessionManager *crypto.SessionManager,
	topicRegistry *TopicRegistry,
	replayProtector *ReplayProtector,
	config ReceiverConfig,
	log p2p.Logger,
) (*Receiver, error) {
	if socket == nil {
		return nil, ErrSocketNotInitialized
	}
	if peerManager == nil {
		return nil, ErrNilPeerManager
	}
	if log == nil {
		return nil, ErrNilLogger
	}

	ctx, cancel := context.WithCancel(context.Background())

	r := &Receiver{
		socket:          socket,
		peerManager:     peerManager,
		fragmenter:      NewFragmentAssembler(DefaultFragmentTimeout),
		topicRegistry:   topicRegistry,
		replayProtector: replayProtector,
		authenticator:   crypto.NewAuthenticator(sessionManager),
		numWorkers:      config.NumWorkers,
		workQueue:       make(chan *receivedPacket, config.QueueSize),
		ctx:             ctx,
		cancel:          cancel,
		log:             log,
	}

	return r, nil
}

// SetMessageHandler sets the message handler callback
func (r *Receiver) SetMessageHandler(handler MessageHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messageHandler = handler
}

// Start starts the receiver
func (r *Receiver) Start() error {
	if r.running.Swap(true) {
		return nil // Already running
	}

	// Start workers
	for i := 0; i < r.numWorkers; i++ {
		r.wg.Add(1)
		go func(id int) {
			defer r.wg.Done()
			r.worker(id)
		}(i)
	}

	// Start receive loop
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.receiveLoop()
	}()

	r.log.Info("XDP receiver started", "workers", r.numWorkers)
	return nil
}

// receiveLoop continuously receives packets from the socket
func (r *Receiver) receiveLoop() {
	buf := make([]byte, DefaultMTU)
	consecutiveErrors := 0

	for r.running.Load() {
		select {
		case <-r.ctx.Done():
			return
		default:
		}

		n, addr, err := r.socket.Receive(buf)
		if err != nil {
			if r.running.Load() {
				r.log.Trace("receive error", "error", err)
			}
			consecutiveErrors++
			delay := time.Duration(consecutiveErrors) * 10 * time.Millisecond
			if delay > 100*time.Millisecond {
				delay = 100 * time.Millisecond
			}
			select {
			case <-r.ctx.Done():
				return
			case <-time.After(delay):
			}
			continue
		}

		// Successful receive: reset error counter
		consecutiveErrors = 0

		// Copy data for processing
		data := make([]byte, n)
		copy(data, buf[:n])

		// Queue for processing
		select {
		case r.workQueue <- &receivedPacket{data: data, addr: addr}:
		default:
			r.messagesDropped.Add(1)
			r.log.Trace("receive queue full, dropping packet")
		}
	}
}

// worker processes received packets
func (r *Receiver) worker(id int) {
	for {
		select {
		case <-r.ctx.Done():
			return
		case pkt := <-r.workQueue:
			r.processPacket(pkt)
		}
	}
}

// processPacket processes a received packet
func (r *Receiver) processPacket(received *receivedPacket) {
	// Decode packet
	pkt, err := Decode(received.data)
	if err != nil {
		r.log.Trace("failed to decode packet", "error", err)
		return
	}

	// Look up peer by address
	peerID, ok := r.peerManager.GetPeerByAddress(received.addr.String())
	if !ok {
		r.log.Trace("unknown peer address", "addr", received.addr.String())
		return
	}

	// Verify HMAC using the trusted peerID resolved from the source address,
	// not the attacker-controllable pkt.PeerID from the wire
	hmacData := pkt.EncodeForHMAC()
	if !r.authenticator.VerifyWithPeerID(peerID, hmacData, pkt.HMAC) {
		r.authFailures.Add(1)
		r.log.Trace("HMAC verification failed", "peer", peerID.Pretty())
		return
	}

	// Check replay protection using a [32]byte derived from the trusted peerID
	if r.replayProtector != nil {
		trustedPeerIDBytes := peerIDToBytes(peerID)
		if err := r.replayProtector.ValidateAndRecord(trustedPeerIDBytes, pkt.SeqNo, pkt.Timestamp); err != nil {
			r.replayBlocked.Add(1)
			r.log.Trace("replay protection blocked message", "error", err, "peer", peerID.Pretty())
			return
		}
	}

	// Handle fragmentation
	var messageData []byte
	if pkt.IsFragment() {
		data, complete, err := r.fragmenter.AddFragment(pkt)
		if err != nil {
			r.log.Trace("fragment error", "error", err)
			return
		}
		if !complete {
			return // Wait for more fragments
		}
		messageData = data
	} else {
		messageData = pkt.Payload
	}

	// Get topic from registry
	topic, ok := r.topicRegistry.GetTopic(pkt.TopicID)
	if !ok {
		r.log.Trace("unknown topic ID", "topicID", pkt.TopicID)
		return
	}

	// Update peer's last seen
	r.peerManager.TouchPeer(peerID)

	// Call message handler
	r.mu.RLock()
	handler := r.messageHandler
	r.mu.RUnlock()

	if handler != nil {
		handler(topic, messageData, peerID)
	}

	r.messagesReceived.Add(1)
}

// GetStats returns receiver statistics
func (r *Receiver) GetStats() ReceiverStats {
	return ReceiverStats{
		MessagesReceived: r.messagesReceived.Load(),
		MessagesDropped:  r.messagesDropped.Load(),
		AuthFailures:     r.authFailures.Load(),
		ReplayBlocked:    r.replayBlocked.Load(),
		QueueLength:      len(r.workQueue),
		FragmentsPending: r.fragmenter.PendingCount(),
	}
}

// ReceiverStats contains receiver statistics
type ReceiverStats struct {
	MessagesReceived uint64
	MessagesDropped  uint64
	AuthFailures     uint64
	ReplayBlocked    uint64
	QueueLength      int
	FragmentsPending int
}

// Stop stops the receiver
func (r *Receiver) Stop() error {
	if !r.running.Swap(false) {
		return nil // Already stopped
	}

	r.cancel()
	r.wg.Wait()
	r.fragmenter.Close()

	r.log.Info("XDP receiver stopped")
	return nil
}

// Close is an alias for Stop
func (r *Receiver) Close() error {
	return r.Stop()
}
