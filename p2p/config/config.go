package config

// P2PConfig will hold all the P2P settings
type P2PConfig struct {
	Node                NodeConfig
	KadDhtPeerDiscovery KadDhtPeerDiscoveryConfig
	Sharding            ShardingConfig
}

// NodeConfig will hold basic p2p settings
type NodeConfig struct {
	Port                            string
	MaximumExpectedPeerCount        uint64
	ThresholdMinConnectedPeers      uint32
	MinNumPeersToWaitForOnBootstrap uint32
	Transports                      TransportConfig
	ResourceLimiter                 ResourceLimiterConfig
}

// TransportConfig specifies the supported protocols by the node
type TransportConfig struct {
	TCP                 TCPProtocolConfig
	QUICAddress         string
	WebSocketAddress    string
	WebTransportAddress string
	XDP                 XDPTransportConfig
}

// TCPProtocolConfig specifies the TCP protocol config
type TCPProtocolConfig struct {
	ListenAddress    string
	PreventPortReuse bool
}

// XDPTransportConfig specifies the XDP (eXpress Data Path) transport config
// XDP provides two acceleration modes:
//   - Phase 1: Custom fast-path protocol between XDP-capable peers (kernel bypass)
//   - Phase 2 (AccelerateQUIC): AF_XDP acceleration for standard QUIC traffic
//
// The XDP port is derived from the global Node.Port range. No separate port config needed.
// Requires Linux with kernel 4.18+ and CAP_BPF+CAP_NET_ADMIN for AF_XDP.
// Falls back gracefully to standard networking on unsupported platforms.
type XDPTransportConfig struct {
	Enabled        bool
	AccelerateQUIC bool
	Interface      string // Network interface for AF_XDP (empty = auto-detect)
	QueueSize      uint32 // AF_XDP ring buffer size (power of 2, default: 2048)
	BatchSize      uint32 // Batch size for send operations (default: 64)
	Security       XDPSecurityConfig
}

// XDPSecurityConfig holds XDP security settings
type XDPSecurityConfig struct {
	KeyRotationIntervalSec uint32
	ReplayWindowSize       int
	TimestampToleranceSec  uint32
}

// ResourceLimiterConfig specifies the resource limiter configuration
type ResourceLimiterConfig struct {
	Type                   string
	ManualSystemMemoryInMB int64
	ManualMaximumFD        int
	Ipv4ConnLimit          []ConnLimitConfig
	Ipv6ConnLimit          []ConnLimitConfig
}

// ConnLimitConfig specifies the limit that will be set for an ip on libp2p connection limiter
type ConnLimitConfig struct {
	PrefixLength int
	ConnCount    int
}

// KadDhtPeerDiscoveryConfig will hold the kad-dht discovery config settings
type KadDhtPeerDiscoveryConfig struct {
	Enabled                          bool
	Type                             string
	RefreshIntervalInSec             uint32
	ProtocolIDs                      []string
	InitialPeerList                  []string
	BucketSize                       uint32
	RoutingTableRefreshIntervalInSec uint32
}

// ShardingConfig will hold the network sharding config settings
type ShardingConfig struct {
	TargetPeerCount         uint32
	MaxIntraShardValidators uint32
	MaxCrossShardValidators uint32
	MaxIntraShardObservers  uint32
	MaxCrossShardObservers  uint32
	MaxSeeders              uint32
	Type                    string
}

// XDPConfig is a convenience alias for backward compatibility.
// Prefer using P2PConfig.Node.Transports.XDP directly.
type XDPConfig = XDPTransportConfig
