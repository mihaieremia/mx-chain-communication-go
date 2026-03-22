package xdp

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/multiversx/mx-chain-communication-go/p2p"
	"github.com/multiversx/mx-chain-communication-go/p2p/config"
	"github.com/multiversx/mx-chain-communication-go/p2p/xdp/crypto"
	"github.com/multiversx/mx-chain-communication-go/p2p/xdp/peer"
	"github.com/multiversx/mx-chain-core-go/core"
)

// Engine is the main XDP engine that coordinates all components
type Engine struct {
	mu sync.RWMutex

	// Configuration
	config Config

	// Components
	socket            *Socket
	sender            *Sender
	receiver          *Receiver
	router            *Router
	broadcaster       *Broadcaster
	mesh              *Mesh
	peerManager       *peer.Manager
	capabilityHandler *peer.CapabilityHandler
	sessionManager    *crypto.SessionManager
	replayProtector   *ReplayProtector
	topicRegistry     *TopicRegistry
	metrics           *Metrics

	// State
	enabled atomic.Bool
	running atomic.Bool

	// Context
	ctx        context.Context
	cancelFunc context.CancelFunc

	// Logger
	log p2p.Logger
}

// EngineArgs holds arguments for creating an XDP engine
type EngineArgs struct {
	Config     config.XDPTransportConfig
	Port       uint16 // Resolved port from global Node.Port range
	Host       host.Host
	Marshaller p2p.Marshaller
	Logger     p2p.Logger
}

// NewEngine creates a new XDP engine
func NewEngine(args EngineArgs) (*Engine, error) {
	if args.Logger == nil {
		return nil, ErrNilLogger
	}

	log := args.Logger

	// Check if XDP is supported
	supported, reason := IsXDPSupported()
	if !supported {
		log.Debug("XDP not natively supported", "reason", reason)
	}

	// Convert config
	xdpConfig := Config{
		Enabled:   args.Config.Enabled,
		Port:      args.Port,
		Interface: args.Config.Interface,
		QueueSize: args.Config.QueueSize,
		BatchSize: args.Config.BatchSize,
		Security: SecurityConfig{
			KeyRotationInterval: time.Duration(args.Config.Security.KeyRotationIntervalSec) * time.Second,
			ReplayWindowSize:    args.Config.Security.ReplayWindowSize,
			TimestampTolerance:  time.Duration(args.Config.Security.TimestampToleranceSec) * time.Second,
			MaxSeqNoGap:         10000,
		},
	}

	// Apply defaults if not set
	if xdpConfig.Port == 0 {
		xdpConfig.Port = 37374
	}
	if xdpConfig.QueueSize == 0 {
		xdpConfig.QueueSize = 2048
	}
	if xdpConfig.BatchSize == 0 {
		xdpConfig.BatchSize = 64
	}
	if xdpConfig.Security.KeyRotationInterval == 0 {
		xdpConfig.Security.KeyRotationInterval = 24 * time.Hour
	}
	if xdpConfig.Security.ReplayWindowSize == 0 {
		xdpConfig.Security.ReplayWindowSize = 100000
	}
	if xdpConfig.Security.TimestampTolerance == 0 {
		xdpConfig.Security.TimestampTolerance = 60 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	e := &Engine{
		config:        xdpConfig,
		topicRegistry: NewTopicRegistry(),
		metrics:       NewMetrics(),
		ctx:           ctx,
		cancelFunc:    cancel,
		log:           log,
	}

	if !args.Config.Enabled {
		log.Info("XDP is disabled in configuration")
		return e, nil
	}

	// Track cleanups for rollback on error
	var cleanups []func()
	succeeded := false
	defer func() {
		if !succeeded {
			for i := len(cleanups) - 1; i >= 0; i-- {
				cleanups[i]()
			}
		}
	}()

	// Create socket
	socket, err := NewSocket(xdpConfig, log)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create XDP socket: %w", err)
	}
	e.socket = socket
	cleanups = append(cleanups, func() { socket.Close() })

	// Create session manager
	e.sessionManager = crypto.NewSessionManager(
		xdpConfig.Security.KeyRotationInterval,
		10*time.Minute, // idle timeout
	)
	cleanups = append(cleanups, func() { e.sessionManager.Close() })

	// Create replay protector
	replayProtector, err := NewReplayProtector(
		xdpConfig.Security.ReplayWindowSize,
		xdpConfig.Security.TimestampTolerance,
		xdpConfig.Security.MaxSeqNoGap,
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create replay protector: %w", err)
	}
	e.replayProtector = replayProtector

	// Create peer manager
	peerManager, err := peer.NewManager(peer.DefaultManagerConfig(), e.sessionManager, log)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create peer manager: %w", err)
	}
	e.peerManager = peerManager
	cleanups = append(cleanups, func() { peerManager.Close() })

	// Create capability handler if host is provided
	if args.Host != nil {
		e.capabilityHandler = peer.NewCapabilityHandler(
			args.Host,
			peerManager,
			xdpConfig.Port,
			log,
		)
	}

	// Create mesh
	e.mesh = NewMesh(peerManager, DefaultMeshConfig(), log)

	// Create sender
	sender, err := NewSender(
		socket,
		peerManager,
		e.sessionManager,
		e.topicRegistry,
		DefaultSenderConfig(),
		log,
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create sender: %w", err)
	}
	e.sender = sender
	cleanups = append(cleanups, func() { sender.Close() })

	// Create receiver
	receiver, err := NewReceiver(
		socket,
		peerManager,
		e.sessionManager,
		e.topicRegistry,
		replayProtector,
		DefaultReceiverConfig(),
		log,
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create receiver: %w", err)
	}
	e.receiver = receiver

	// Create broadcaster
	e.broadcaster = NewBroadcaster(sender, e.mesh, peerManager, e.topicRegistry, log)

	// Create router
	e.router = NewRouter(sender, e.broadcaster, peerManager, DefaultRouterConfig(), log)

	succeeded = true
	e.enabled.Store(true)
	log.Info("XDP engine created",
		"port", xdpConfig.Port,
		"interface", xdpConfig.Interface,
		"nativeXDP", supported,
	)

	return e, nil
}

// Start starts the XDP engine
func (e *Engine) Start() error {
	if !e.enabled.Load() {
		return nil // XDP not enabled
	}

	if e.running.Swap(true) {
		return nil // Already running
	}

	// Start receiver
	if e.receiver != nil {
		if err := e.receiver.Start(); err != nil {
			e.running.Store(false)
			return fmt.Errorf("failed to start receiver: %w", err)
		}
	}

	e.log.Info("XDP engine started")
	return nil
}

// Stop stops the XDP engine
func (e *Engine) Stop() error {
	if !e.running.Swap(false) {
		return nil // Not running
	}

	e.cancelFunc()

	var errs []error

	if e.receiver != nil {
		if err := e.receiver.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if e.sender != nil {
		if err := e.sender.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if e.router != nil {
		if err := e.router.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if e.peerManager != nil {
		if err := e.peerManager.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if e.sessionManager != nil {
		if err := e.sessionManager.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if e.capabilityHandler != nil {
		if err := e.capabilityHandler.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if e.socket != nil {
		if err := e.socket.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors during shutdown: %v", errs)
	}

	e.log.Info("XDP engine stopped")
	return nil
}

// IsEnabled returns true if XDP is enabled
func (e *Engine) IsEnabled() bool {
	return e.enabled.Load()
}

// IsRunning returns true if the engine is running
func (e *Engine) IsRunning() bool {
	return e.running.Load()
}

// GetRouter returns the XDP router
func (e *Engine) GetRouter() *Router {
	return e.router
}

// GetBroadcaster returns the XDP broadcaster
func (e *Engine) GetBroadcaster() *Broadcaster {
	return e.broadcaster
}

// GetSender returns the XDP sender
func (e *Engine) GetSender() *Sender {
	return e.sender
}

// GetReceiver returns the XDP receiver
func (e *Engine) GetReceiver() *Receiver {
	return e.receiver
}

// GetPeerManager returns the XDP peer manager
func (e *Engine) GetPeerManager() *peer.Manager {
	return e.peerManager
}

// GetMesh returns the XDP mesh
func (e *Engine) GetMesh() *Mesh {
	return e.mesh
}

// GetTopicRegistry returns the topic registry
func (e *Engine) GetTopicRegistry() *TopicRegistry {
	return e.topicRegistry
}

// GetMetrics returns the metrics collector
func (e *Engine) GetMetrics() *Metrics {
	return e.metrics
}

// SetMessageHandler sets the message handler for received messages
func (e *Engine) SetMessageHandler(handler MessageHandler) {
	if e.receiver != nil {
		e.receiver.SetMessageHandler(handler)
	}
}

// SetLibP2PFallbacks sets the libp2p fallback handlers
func (e *Engine) SetLibP2PFallbacks(sender LibP2PDirectSender, broadcaster LibP2PBroadcaster) {
	if e.router != nil {
		e.router.SetLibP2PFallbacks(sender, broadcaster)
	}
}

// SubscribeTopic subscribes to a topic
func (e *Engine) SubscribeTopic(topic string) {
	if e.broadcaster != nil {
		e.broadcaster.SubscribeTopic(topic)
	}
}

// UnsubscribeTopic unsubscribes from a topic
func (e *Engine) UnsubscribeTopic(topic string) {
	if e.broadcaster != nil {
		e.broadcaster.UnsubscribeTopic(topic)
	}
}

// Send sends a message to a peer
func (e *Engine) Send(topic string, data []byte, peerID core.PeerID) error {
	if !e.enabled.Load() || e.router == nil {
		return ErrXDPNotEnabled
	}
	return e.router.Send(topic, data, peerID)
}

// Broadcast broadcasts a message
func (e *Engine) Broadcast(topic string, data []byte) {
	if !e.enabled.Load() || e.router == nil {
		return
	}
	e.router.Broadcast(topic, data)
}

// BroadcastOnChannel broadcasts on a specific channel
func (e *Engine) BroadcastOnChannel(channel, topic string, data []byte) {
	if !e.enabled.Load() || e.router == nil {
		return
	}
	e.router.BroadcastOnChannel(channel, topic, data)
}

// HasXDP checks if a peer supports XDP
func (e *Engine) HasXDP(peerID core.PeerID) bool {
	if e.peerManager == nil {
		return false
	}
	return e.peerManager.HasXDP(peerID)
}

// GetXDPPeers returns all XDP-capable peers
func (e *Engine) GetXDPPeers() []core.PeerID {
	if e.peerManager == nil {
		return nil
	}
	return e.peerManager.GetXDPPeers()
}

// ExchangeCapability initiates capability exchange with a peer
func (e *Engine) ExchangeCapability(ctx context.Context, peerID core.PeerID) error {
	if e.capabilityHandler == nil {
		return ErrXDPNotEnabled
	}
	_, err := e.capabilityHandler.ExchangeCapability(ctx, peerID)
	return err
}

// Close closes the engine
func (e *Engine) Close() error {
	return e.Stop()
}
