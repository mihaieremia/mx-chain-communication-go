package xdp

import (
	"sync"
	"time"
)

// TODO: Metrics is created in Engine but never wired to Sender/Receiver/Router — none of
// the recording methods are called in production code. Wire it into the hot paths or
// remove it once the XDP layer stabilises. Kept for now because XDPMetrics interface
// and Engine.GetMetrics() are part of the public API.

// Metrics collects XDP performance metrics
type Metrics struct {
	mu sync.RWMutex

	// Packet metrics
	packetsSent     uint64
	packetsReceived uint64
	bytesSent       uint64
	bytesReceived   uint64

	// Error metrics
	sendErrors    uint64
	receiveErrors uint64
	authFailures  uint64
	replayBlocked uint64

	// Latency tracking (in microseconds)
	sendLatencySum   uint64
	sendLatencyCount uint64
	recvLatencySum   uint64
	recvLatencyCount uint64

	// Fallback metrics
	xdpSends     uint64
	libp2pSends  uint64
	fallbackRate float64

	// Peer metrics
	xdpPeerCount int

	// Start time for rate calculations
	startTime time.Time
}

// NewMetrics creates a new metrics collector
func NewMetrics() *Metrics {
	return &Metrics{
		startTime: time.Now(),
	}
}

// RecordSend records a sent packet
func (m *Metrics) RecordSend(bytes int, latencyMicros uint64, viaXDP bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.packetsSent++
	m.bytesSent += uint64(bytes)

	if latencyMicros > 0 {
		m.sendLatencySum += latencyMicros
		m.sendLatencyCount++
	}

	if viaXDP {
		m.xdpSends++
	} else {
		m.libp2pSends++
	}
}

// RecordReceive records a received packet
func (m *Metrics) RecordReceive(bytes int, latencyMicros uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.packetsReceived++
	m.bytesReceived += uint64(bytes)

	if latencyMicros > 0 {
		m.recvLatencySum += latencyMicros
		m.recvLatencyCount++
	}
}

// RecordSendError records a send error
func (m *Metrics) RecordSendError() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendErrors++
}

// RecordReceiveError records a receive error
func (m *Metrics) RecordReceiveError() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.receiveErrors++
}

// RecordAuthFailure records an authentication failure
func (m *Metrics) RecordAuthFailure() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.authFailures++
}

// RecordReplayBlocked records a blocked replay attempt
func (m *Metrics) RecordReplayBlocked() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.replayBlocked++
}

// UpdatePeerCount updates the XDP peer count
func (m *Metrics) UpdatePeerCount(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.xdpPeerCount = count
}

// GetSnapshot returns a snapshot of all metrics
func (m *Metrics) GetSnapshot() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snapshot := MetricsSnapshot{
		PacketsSent:     m.packetsSent,
		PacketsReceived: m.packetsReceived,
		BytesSent:       m.bytesSent,
		BytesReceived:   m.bytesReceived,
		SendErrors:      m.sendErrors,
		ReceiveErrors:   m.receiveErrors,
		AuthFailures:    m.authFailures,
		ReplayBlocked:   m.replayBlocked,
		XDPSends:        m.xdpSends,
		LibP2PSends:     m.libp2pSends,
		XDPPeerCount:    m.xdpPeerCount,
		UptimeSeconds:   uint64(time.Since(m.startTime).Seconds()),
	}

	// Calculate averages
	if m.sendLatencyCount > 0 {
		snapshot.AvgSendLatencyMicros = float64(m.sendLatencySum) / float64(m.sendLatencyCount)
	}
	if m.recvLatencyCount > 0 {
		snapshot.AvgRecvLatencyMicros = float64(m.recvLatencySum) / float64(m.recvLatencyCount)
	}

	// Calculate rates
	totalSends := m.xdpSends + m.libp2pSends
	if totalSends > 0 {
		snapshot.XDPUsageRate = float64(m.xdpSends) / float64(totalSends) * 100
	}

	// Calculate throughput
	if snapshot.UptimeSeconds > 0 {
		snapshot.SendBytesPerSec = float64(m.bytesSent) / float64(snapshot.UptimeSeconds)
		snapshot.RecvBytesPerSec = float64(m.bytesReceived) / float64(snapshot.UptimeSeconds)
		snapshot.PacketsPerSec = float64(m.packetsSent+m.packetsReceived) / float64(snapshot.UptimeSeconds)
	}

	return snapshot
}

// MetricsSnapshot contains a point-in-time snapshot of metrics
type MetricsSnapshot struct {
	// Counts
	PacketsSent     uint64
	PacketsReceived uint64
	BytesSent       uint64
	BytesReceived   uint64

	// Errors
	SendErrors    uint64
	ReceiveErrors uint64
	AuthFailures  uint64
	ReplayBlocked uint64

	// Routing
	XDPSends    uint64
	LibP2PSends uint64

	// Latency (microseconds)
	AvgSendLatencyMicros float64
	AvgRecvLatencyMicros float64

	// Rates
	XDPUsageRate    float64 // Percentage of sends via XDP
	SendBytesPerSec float64
	RecvBytesPerSec float64
	PacketsPerSec   float64

	// Peers
	XDPPeerCount int

	// Uptime
	UptimeSeconds uint64
}

// Reset resets all metrics
func (m *Metrics) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.packetsSent = 0
	m.packetsReceived = 0
	m.bytesSent = 0
	m.bytesReceived = 0
	m.sendErrors = 0
	m.receiveErrors = 0
	m.authFailures = 0
	m.replayBlocked = 0
	m.sendLatencySum = 0
	m.sendLatencyCount = 0
	m.recvLatencySum = 0
	m.recvLatencyCount = 0
	m.xdpSends = 0
	m.libp2pSends = 0
	m.startTime = time.Now()
}

// PrometheusMetrics returns metrics formatted for Prometheus
// This can be used with a Prometheus exporter
func (m *Metrics) PrometheusMetrics() map[string]float64 {
	snapshot := m.GetSnapshot()

	return map[string]float64{
		"xdp_packets_sent_total":          float64(snapshot.PacketsSent),
		"xdp_packets_received_total":      float64(snapshot.PacketsReceived),
		"xdp_bytes_sent_total":            float64(snapshot.BytesSent),
		"xdp_bytes_received_total":        float64(snapshot.BytesReceived),
		"xdp_send_errors_total":           float64(snapshot.SendErrors),
		"xdp_receive_errors_total":        float64(snapshot.ReceiveErrors),
		"xdp_auth_failures_total":         float64(snapshot.AuthFailures),
		"xdp_replay_blocked_total":        float64(snapshot.ReplayBlocked),
		"xdp_sends_total":                 float64(snapshot.XDPSends),
		"xdp_libp2p_fallback_total":       float64(snapshot.LibP2PSends),
		"xdp_avg_send_latency_microseconds": snapshot.AvgSendLatencyMicros,
		"xdp_avg_recv_latency_microseconds": snapshot.AvgRecvLatencyMicros,
		"xdp_usage_rate_percent":          snapshot.XDPUsageRate,
		"xdp_send_bytes_per_second":       snapshot.SendBytesPerSec,
		"xdp_recv_bytes_per_second":       snapshot.RecvBytesPerSec,
		"xdp_packets_per_second":          snapshot.PacketsPerSec,
		"xdp_peer_count":                  float64(snapshot.XDPPeerCount),
	}
}
