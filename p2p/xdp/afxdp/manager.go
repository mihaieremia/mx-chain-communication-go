//go:build linux

package afxdp

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Manager manages multiple AF_XDP sockets across NIC queues
// It provides a unified interface for sending/receiving packets
// and handles load balancing across queues.
type Manager struct {
	// Configuration
	config ManagerConfig

	// XDP program (shared across all sockets)
	xdpProg *XDPProgram

	// Sockets per queue
	sockets []*Socket
	numQueues int

	// Load balancing
	nextTxQueue atomic.Uint64

	// Receiver callback
	onReceive func(queueID int, data []byte)

	// State
	running atomic.Bool
	closed  atomic.Bool
	wg      sync.WaitGroup
	mu      sync.RWMutex
}

// NewManager creates a new AF_XDP manager
func NewManager(config ManagerConfig) (*Manager, error) {
	if config.Interface == "" {
		return nil, ErrInvalidInterface
	}

	// Detect number of queues if not specified
	if config.NumQueues <= 0 {
		numQueues, err := GetNumQueues(config.Interface)
		if err != nil {
			// Default to 1 queue
			numQueues = 1
		}
		config.NumQueues = numQueues
	}

	m := &Manager{
		config:    config,
		numQueues: config.NumQueues,
		sockets:   make([]*Socket, config.NumQueues),
	}

	// Load XDP program
	xdpProg, err := NewXDPProgram(config.Interface, config.XDPMode)
	if err != nil {
		return nil, fmt.Errorf("failed to load XDP program: %w", err)
	}
	m.xdpProg = xdpProg

	// Create sockets for each queue
	for i := 0; i < config.NumQueues; i++ {
		sockConfig := config.SocketConfig
		sockConfig.Interface = config.Interface
		sockConfig.QueueID = i

		sock, err := New(sockConfig)
		if err != nil {
			// Cleanup on failure
			m.closePartial(i)
			xdpProg.Close()
			return nil, fmt.Errorf("failed to create socket for queue %d: %w", i, err)
		}

		// Register socket in XSKMAP
		if err := xdpProg.RegisterSocket(i, sock.Fd()); err != nil {
			sock.Close()
			m.closePartial(i)
			xdpProg.Close()
			return nil, fmt.Errorf("failed to register socket for queue %d: %w", i, err)
		}

		m.sockets[i] = sock
	}

	return m, nil
}

// closePartial closes sockets up to index n during initialization failure
func (m *Manager) closePartial(n int) {
	for i := 0; i < n; i++ {
		if m.sockets[i] != nil {
			m.xdpProg.UnregisterSocket(i)
			m.sockets[i].Close()
		}
	}
}

// Start starts the receive workers for all queues
func (m *Manager) Start(onReceive func(queueID int, data []byte)) error {
	if m.running.Swap(true) {
		return nil // Already running
	}

	m.onReceive = onReceive

	// Start receive worker for each queue
	for i := 0; i < m.numQueues; i++ {
		m.wg.Add(1)
		go m.receiveWorker(i)
	}

	return nil
}

// receiveWorker processes received packets for a specific queue
func (m *Manager) receiveWorker(queueID int) {
	defer m.wg.Done()

	sock := m.sockets[queueID]
	packets := make([][]byte, m.config.SocketConfig.BatchSize)

	for m.running.Load() {
		// Poll for incoming packets
		canRead, _, err := sock.Poll(m.config.PollTimeout)
		if err != nil {
			continue
		}

		if !canRead {
			continue
		}

		// Receive packets
		n, err := sock.Receive(packets)
		if err != nil {
			continue
		}

		// Process received packets
		m.mu.RLock()
		callback := m.onReceive
		m.mu.RUnlock()

		if callback != nil {
			for i := 0; i < n; i++ {
				callback(queueID, packets[i])
			}
		}
	}
}

// Send sends a packet through the manager (load-balanced across queues)
func (m *Manager) Send(data []byte) error {
	if m.closed.Load() {
		return ErrSocketClosed
	}

	// Round-robin load balancing
	queueID := int(m.nextTxQueue.Add(1) % uint64(m.numQueues))
	return m.sockets[queueID].SendOne(data)
}

// SendToQueue sends a packet through a specific queue
func (m *Manager) SendToQueue(queueID int, data []byte) error {
	if m.closed.Load() {
		return ErrSocketClosed
	}

	if queueID < 0 || queueID >= m.numQueues {
		return ErrInvalidQueueID
	}

	return m.sockets[queueID].SendOne(data)
}

// SendBatch sends multiple packets (load-balanced)
func (m *Manager) SendBatch(packets [][]byte) (int, error) {
	if m.closed.Load() {
		return 0, ErrSocketClosed
	}

	// Round-robin load balancing
	queueID := int(m.nextTxQueue.Add(1) % uint64(m.numQueues))
	return m.sockets[queueID].Send(packets)
}

// GetStats returns aggregated statistics from all sockets
func (m *Manager) GetStats() ManagerStats {
	stats := ManagerStats{
		NumQueues: m.numQueues,
		PerQueue:  make([]Stats, m.numQueues),
	}

	for i, sock := range m.sockets {
		s := sock.GetStats()
		stats.PerQueue[i] = s
		stats.Total.RxPackets += s.RxPackets
		stats.Total.TxPackets += s.TxPackets
		stats.Total.RxBytes += s.RxBytes
		stats.Total.TxBytes += s.TxBytes
		stats.Total.RxDrops += s.RxDrops
		stats.Total.TxDrops += s.TxDrops
		stats.Total.FreeFrames += s.FreeFrames
	}

	return stats
}

// Stop stops all receive workers
func (m *Manager) Stop() {
	if !m.running.Swap(false) {
		return // Not running
	}

	m.wg.Wait()
}

// Close stops the manager and releases all resources
func (m *Manager) Close() error {
	if m.closed.Swap(true) {
		return nil // Already closed
	}

	// Stop receive workers
	m.Stop()

	// Unregister and close all sockets
	for i, sock := range m.sockets {
		if sock != nil {
			m.xdpProg.UnregisterSocket(i)
			sock.Close()
		}
	}

	// Close XDP program
	if m.xdpProg != nil {
		m.xdpProg.Close()
	}

	return nil
}

// GetNumQueues returns the number of RX/TX queues for an interface
func GetNumQueues(ifname string) (int, error) {
	// Try to read from sysfs
	// Path: /sys/class/net/<iface>/queues/
	queuePath := filepath.Join("/sys/class/net", ifname, "queues")

	entries, err := os.ReadDir(queuePath)
	if err != nil {
		return 0, fmt.Errorf("failed to read queue info: %w", err)
	}

	// Count rx-* directories
	rxQueues := 0
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "rx-") {
			rxQueues++
		}
	}

	if rxQueues == 0 {
		return 0, fmt.Errorf("no queues found for interface %s", ifname)
	}

	return rxQueues, nil
}

// SetNumQueues configures the number of NIC queues using ethtool
// Note: This requires root privileges
func SetNumQueues(ifname string, combined int) error {
	// This would typically be done via:
	// ethtool -L <iface> combined <n>
	//
	// In Go, you'd use netlink or exec ethtool
	// For now, return an error indicating manual configuration is needed
	return fmt.Errorf("automatic queue configuration not implemented, use: ethtool -L %s combined %d", ifname, combined)
}

// GetInterfaceInfo returns information about a network interface
func GetInterfaceInfo(ifname string) (*InterfaceInfo, error) {
	iface, err := net.InterfaceByName(ifname)
	if err != nil {
		return nil, err
	}

	info := &InterfaceInfo{
		Name:   iface.Name,
		Index:  iface.Index,
		MTU:    iface.MTU,
		HWAddr: iface.HardwareAddr.String(),
	}

	// Get number of queues
	info.NumQueues, _ = GetNumQueues(ifname)

	// Check XDP support (basic check)
	info.XDPSupport = checkXDPSupport(ifname)

	return info, nil
}

// checkXDPSupport checks if interface supports XDP
func checkXDPSupport(ifname string) bool {
	// Check for XDP driver support via sysfs
	// This is a basic check - real check would try attaching a program
	driverPath := filepath.Join("/sys/class/net", ifname, "device/driver")
	_, err := os.Readlink(driverPath)
	if err != nil {
		return false
	}

	// Most modern NICs support at least generic XDP
	return true
}

// GetRSSSetting reads current RSS (Receive Side Scaling) settings
func GetRSSSetting(ifname string) (int, error) {
	// Read from /sys/class/net/<iface>/queues/rx-0/rps_cpus
	// Or use ethtool -x <iface>
	path := filepath.Join("/sys/class/net", ifname, "device", "msi_irqs")
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

// ConfigureRSSForXDP configures RSS to work optimally with AF_XDP
// This pins IRQs to CPUs for best performance
func ConfigureRSSForXDP(ifname string, numQueues int) error {
	// This would:
	// 1. Set number of queues
	// 2. Configure IRQ affinity
	// 3. Set flow steering rules if needed
	//
	// Typically done via:
	// - ethtool -L <iface> combined <n>
	// - echo <mask> > /proc/irq/<irq>/smp_affinity
	// - ethtool -N <iface> flow-type <type> action <queue>

	return fmt.Errorf("RSS configuration not implemented")
}

// GetDriverInfo returns information about the NIC driver
func GetDriverInfo(ifname string) (*DriverInfo, error) {
	info := &DriverInfo{}

	// Read driver name from sysfs
	driverPath := filepath.Join("/sys/class/net", ifname, "device/driver")
	link, err := os.Readlink(driverPath)
	if err == nil {
		info.Name = filepath.Base(link)
	}

	// Check for XDP native support based on known drivers
	nativeDrivers := map[string]bool{
		"i40e":    true,
		"ixgbe":   true,
		"mlx5":    true,
		"mlx4":    true,
		"bnxt":    true,
		"nfp":     true,
		"virtio":  true,
		"veth":    true,
		"ena":     true,
	}
	info.XDPNative = nativeDrivers[info.Name]

	// Zero-copy requires native support
	zeroCopyDrivers := map[string]bool{
		"i40e":  true,
		"ixgbe": true,
		"mlx5":  true,
	}
	info.XDPZeroCopy = zeroCopyDrivers[info.Name]

	// Read firmware version if available
	fwPath := filepath.Join("/sys/class/net", ifname, "device/firmware_version")
	if data, err := os.ReadFile(fwPath); err == nil {
		info.FWVersion = strings.TrimSpace(string(data))
	}

	// Read bus info
	busPath := filepath.Join("/sys/class/net", ifname, "device")
	if link, err := os.Readlink(busPath); err == nil {
		info.BusInfo = filepath.Base(link)
	}

	return info, nil
}

// BenchmarkSocket runs a quick benchmark on the socket to estimate performance
func BenchmarkSocket(sock *Socket, packetSize int, duration int) (uint64, uint64, error) {
	// Create test packet
	data := make([]byte, packetSize)
	for i := range data {
		data[i] = byte(i)
	}

	start := getNanoTime()
	end := start + int64(duration)*1000000000 // Convert seconds to nanoseconds

	var txCount, rxCount uint64
	packets := make([][]byte, 64)
	for i := range packets {
		packets[i] = make([]byte, packetSize)
	}

	for getNanoTime() < end {
		// Try to receive
		n, _ := sock.Receive(packets)
		rxCount += uint64(n)

		// Send
		sent, _ := sock.Send([][]byte{data})
		txCount += uint64(sent)
	}

	return txCount, rxCount, nil
}

func getNanoTime() int64 {
	return time.Now().UnixNano()
}

// GetIRQInfo returns IRQ information for an interface
func GetIRQInfo(ifname string) ([]IRQInfo, error) {
	var irqs []IRQInfo

	// Read /proc/interrupts and find IRQs for this interface
	data, err := os.ReadFile("/proc/interrupts")
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.Contains(line, ifname) {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}

			irqStr := strings.TrimSuffix(fields[0], ":")
			irq, err := strconv.Atoi(irqStr)
			if err != nil {
				continue
			}

			info := IRQInfo{
				IRQ:       irq,
				Interface: ifname,
			}

			// Parse queue number from IRQ name if present
			for _, f := range fields {
				if strings.Contains(f, ifname) {
					parts := strings.Split(f, "-")
					if len(parts) >= 2 {
						if q, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
							info.Queue = q
						}
					}
				}
			}

			// Read affinity
			affinityPath := fmt.Sprintf("/proc/irq/%d/smp_affinity", irq)
			if aff, err := os.ReadFile(affinityPath); err == nil {
				info.Affinity = strings.TrimSpace(string(aff))
			}

			irqs = append(irqs, info)
		}
	}

	return irqs, nil
}
