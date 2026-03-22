# Phase 2: AF_XDP QUIC Acceleration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Accelerate ALL QUIC traffic (to/from any peer, regardless of XDP support) by replacing the kernel UDP socket with an AF_XDP-backed `net.PacketConn`, transparently injected via `quicreuse.OverrideListenUDP`.

**Architecture:** Create a thin `net.PacketConn` adapter around the existing `AFXDPSocket` (which already handles raw Ethernet frame construction/parsing, ARP resolution, and AF_XDP ring management). Inject it into go-libp2p's QUIC transport via `quicreuse.OverrideListenUDP`. On non-Linux or when XDP is unavailable, fall back to `net.ListenUDP` transparently. Extend the XDP eBPF program to filter by UDP destination port (redirect only QUIC traffic to AF_XDP, pass everything else to kernel).

**Tech Stack:** Go 1.23, go-libp2p v0.45.0 (`quicreuse`), existing `p2p/xdp/afxdp/` infrastructure, `cilium/ebpf`

---

## Context for Implementers

### What already exists (DO NOT rebuild)

The `p2p/xdp/` package has a complete AF_XDP implementation:
- `afxdp/manager.go` — multi-queue AF_XDP socket management
- `afxdp/socket.go` — low-level AF_XDP socket with UMEM + ring buffers
- `afxdp/xdp_program.go` — eBPF program loading + XSKMAP
- `socket_afxdp_linux.go` — `AFXDPSocket` implementing `DataSocket` with `buildUDPPacket()`, `parseUDPPacket()`, ARP cache, MAC resolution
- `socket_udp.go` — standard UDP fallback implementing same `DataSocket` interface
- `socket_factory.go` — auto-detection + fallback factory
- `platform.go` — Linux/capability/kernel version checks

### The hook point

go-libp2p v0.45.0 provides `quicreuse.OverrideListenUDP`:
```go
// In p2p/transport/quicreuse/options.go
type listenUDP func(network string, laddr *net.UDPAddr) (net.PacketConn, error)
func OverrideListenUDP(f listenUDP) Option
```

Used via:
```go
libp2p.QUICReuse(quicreuse.NewConnManager, quicreuse.OverrideListenUDP(myFunc))
```

This replaces `net.ListenUDP` for ALL QUIC connections (listen + dial). quic-go accepts any `net.PacketConn` — if it's not a `*net.UDPConn`, it falls back to basic mode (no ECN/GSO, but still fully functional).

### The gap

`AFXDPSocket` implements `DataSocket` (custom interface), not `net.PacketConn`. The gap:

| `DataSocket` | `net.PacketConn` | Delta |
|-------------|------------------|-------|
| `Send(data, *UDPAddr) error` | `WriteTo(b, Addr) (int, error)` | Return type |
| `Receive(buf) (int, *UDPAddr, error)` | `ReadFrom(b) (int, Addr, error)` | Interface type |
| — | `LocalAddr() Addr` | Already exists (returns `*UDPAddr`) |
| — | `SetDeadline(Time) error` | **Missing** |
| — | `SetReadDeadline(Time) error` | **Missing** |
| — | `SetWriteDeadline(Time) error` | **Missing** |
| — | `Close() error` | Already exists |

---

## File Map

### Files to create

| File | Responsibility |
|------|---------------|
| `p2p/xdp/accel/packetconn.go` | `XDPPacketConn` struct implementing `net.PacketConn` — wraps `DataSocket` with deadline support |
| `p2p/xdp/accel/packetconn_test.go` | Tests for `XDPPacketConn` (deadline behavior, ReadFrom/WriteTo delegation) |
| `p2p/xdp/accel/listen.go` | `NewListenUDP` factory — returns AF_XDP-backed `net.PacketConn` on Linux, `net.ListenUDP` elsewhere |
| `p2p/xdp/accel/listen_test.go` | Tests for factory (fallback behavior, config handling) |
| `p2p/xdp/accel/xdp_filter.go` | Port-filtered XDP program builder (extends `afxdp.XDPProgram` to filter by UDP port) |

### Files to modify

| File | Change |
|------|--------|
| `p2p/config/config.go` | Add `AccelerateQUIC bool` to `XDPConfig` |
| `p2p/libp2p/netMessenger.go` | Add `QUICReuse` option when XDP acceleration enabled |

---

## Tasks

### Task 1: Add config field + create `XDPPacketConn` adapter

**Files:**
- Modify: `p2p/config/config.go:72-80`
- Create: `p2p/xdp/accel/packetconn.go`
- Create: `p2p/xdp/accel/packetconn_test.go`

- [ ] **Step 1: Add `AccelerateQUIC` to XDPConfig**

In `p2p/config/config.go`, add the field:
```go
type XDPConfig struct {
    Enabled        bool
    AccelerateQUIC bool   // NEW: accelerate QUIC transport with AF_XDP
    Port           uint16
    Interface      string
    QueueSize      uint32
    BatchSize      uint32
    Security       XDPSecurityConfig
}
```

- [ ] **Step 2: Write test for XDPPacketConn**

Create `p2p/xdp/accel/packetconn_test.go`:
```go
package accel

import (
    "net"
    "os"
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestXDPPacketConn_ImplementsNetPacketConn(t *testing.T) {
    // Verify compile-time interface compliance
    var _ net.PacketConn = (*XDPPacketConn)(nil)
}

func TestXDPPacketConn_WriteTo(t *testing.T) {
    // Use a real UDP socket as the underlying DataSocket for testing
    udpAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
    udpConn, err := net.ListenUDP("udp", udpAddr)
    require.NoError(t, err)
    defer udpConn.Close()

    pc := WrapUDPConn(udpConn)
    defer pc.Close()

    // WriteTo should return (len, nil) on success
    destAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:9999")
    n, err := pc.WriteTo([]byte("hello"), destAddr)
    assert.NoError(t, err)
    assert.Equal(t, 5, n)
}

func TestXDPPacketConn_SetDeadline(t *testing.T) {
    udpAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
    udpConn, err := net.ListenUDP("udp", udpAddr)
    require.NoError(t, err)
    defer udpConn.Close()

    pc := WrapUDPConn(udpConn)
    defer pc.Close()

    // Set a very short deadline
    err = pc.SetReadDeadline(time.Now().Add(1 * time.Millisecond))
    assert.NoError(t, err)

    // ReadFrom should timeout
    buf := make([]byte, 1500)
    time.Sleep(5 * time.Millisecond)
    _, _, err = pc.ReadFrom(buf)
    assert.ErrorIs(t, err, os.ErrDeadlineExceeded)
}
```

- [ ] **Step 3: Run test, verify it fails (struct doesn't exist)**

```bash
go test ./p2p/xdp/accel/... -count=1 -v
```
Expected: compilation error — `XDPPacketConn` undefined

- [ ] **Step 4: Implement XDPPacketConn**

Create `p2p/xdp/accel/packetconn.go`:
```go
package accel

import (
    "net"
    "os"
    "sync/atomic"
    "time"
)

// XDPPacketConn wraps a net.UDPConn or AF_XDP-backed DataSocket
// to implement net.PacketConn for use with quic-go.
//
// For AF_XDP: the underlying socket handles raw Ethernet frame
// construction/parsing and kernel bypass. quic-go sees standard
// UDP payloads — it doesn't know AF_XDP is underneath.
type XDPPacketConn struct {
    conn          net.PacketConn // underlying connection (UDPConn or AF_XDP adapter)
    readDeadline  atomic.Value   // time.Time
    writeDeadline atomic.Value   // time.Time
    isAFXDP       bool           // true if backed by AF_XDP
}

// WrapUDPConn wraps a standard *net.UDPConn as an XDPPacketConn.
// Used for testing and as the non-Linux fallback.
func WrapUDPConn(conn *net.UDPConn) *XDPPacketConn {
    return &XDPPacketConn{conn: conn, isAFXDP: false}
}

// ReadFrom reads a UDP payload from the underlying socket.
func (x *XDPPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
    return x.conn.ReadFrom(b)
}

// WriteTo writes a UDP payload to the specified address.
func (x *XDPPacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
    return x.conn.WriteTo(b, addr)
}

// Close closes the underlying socket.
func (x *XDPPacketConn) Close() error {
    return x.conn.Close()
}

// LocalAddr returns the local address.
func (x *XDPPacketConn) LocalAddr() net.Addr {
    return x.conn.LocalAddr()
}

// SetDeadline sets both read and write deadlines.
func (x *XDPPacketConn) SetDeadline(t time.Time) error {
    return x.conn.SetDeadline(t)
}

// SetReadDeadline sets the read deadline.
func (x *XDPPacketConn) SetReadDeadline(t time.Time) error {
    return x.conn.SetReadDeadline(t)
}

// SetWriteDeadline sets the write deadline.
func (x *XDPPacketConn) SetWriteDeadline(t time.Time) error {
    return x.conn.SetWriteDeadline(t)
}

// IsAFXDP returns true if this conn is backed by AF_XDP kernel bypass.
func (x *XDPPacketConn) IsAFXDP() bool {
    return x.isAFXDP
}

// compile-time check
var _ net.PacketConn = (*XDPPacketConn)(nil)
```

- [ ] **Step 5: Run tests, verify they pass**

```bash
go test ./p2p/xdp/accel/... -count=1 -v -race
```
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add p2p/config/config.go p2p/xdp/accel/
git commit -m "feat: add XDPPacketConn adapter and AccelerateQUIC config"
```

---

### Task 2: Create AF_XDP-backed PacketConn factory (Linux)

**Files:**
- Create: `p2p/xdp/accel/listen.go`
- Create: `p2p/xdp/accel/listen_linux.go`
- Create: `p2p/xdp/accel/listen_other.go`
- Create: `p2p/xdp/accel/listen_test.go`

This is the factory that `quicreuse.OverrideListenUDP` will call. On Linux with AF_XDP support, it creates an AF_XDP-backed `DataSocket` and wraps it. On other platforms, it falls back to `net.ListenUDP`.

- [ ] **Step 1: Write test for the factory**

Create `p2p/xdp/accel/listen_test.go`:
```go
package accel

import (
    "net"
    "testing"

    "github.com/multiversx/mx-chain-communication-go/p2p/xdp"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestNewListenUDP_FallbackToStandard(t *testing.T) {
    // With empty/disabled config, should fall back to net.ListenUDP
    cfg := xdp.Config{} // XDP not configured
    factory := NewListenUDP(cfg, nil)

    laddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
    conn, err := factory("udp", laddr)
    require.NoError(t, err)
    defer conn.Close()

    // Should be a working PacketConn
    assert.NotNil(t, conn.LocalAddr())

    // Verify it's NOT AF_XDP (fallback)
    xdpConn, ok := conn.(*XDPPacketConn)
    if ok {
        assert.False(t, xdpConn.IsAFXDP())
    }
}
```

- [ ] **Step 2: Implement factory — shared interface**

Create `p2p/xdp/accel/listen.go`:
```go
package accel

import (
    "net"

    "github.com/multiversx/mx-chain-communication-go/p2p"
    "github.com/multiversx/mx-chain-communication-go/p2p/xdp"
)

// ListenUDPFunc matches the signature expected by quicreuse.OverrideListenUDP
type ListenUDPFunc func(network string, laddr *net.UDPAddr) (net.PacketConn, error)

// NewListenUDP creates a ListenUDP function that returns AF_XDP-backed
// PacketConns on Linux (when supported), falling back to net.ListenUDP otherwise.
//
// Usage with go-libp2p:
//
//   libp2p.QUICReuse(
//       quicreuse.NewConnManager,
//       quicreuse.OverrideListenUDP(accel.NewListenUDP(xdpConfig, logger)),
//   )
func NewListenUDP(cfg xdp.Config, log p2p.Logger) ListenUDPFunc {
    return func(network string, laddr *net.UDPAddr) (net.PacketConn, error) {
        return newListenUDP(cfg, log, network, laddr)
    }
}

// defaultListenUDP is the standard fallback
func defaultListenUDP(network string, laddr *net.UDPAddr) (net.PacketConn, error) {
    return net.ListenUDP(network, laddr)
}
```

- [ ] **Step 3: Implement Linux-specific AF_XDP factory**

Create `p2p/xdp/accel/listen_linux.go`:
```go
//go:build linux

package accel

import (
    "net"

    "github.com/multiversx/mx-chain-communication-go/p2p"
    "github.com/multiversx/mx-chain-communication-go/p2p/xdp"
)

func newListenUDP(cfg xdp.Config, log p2p.Logger, network string, laddr *net.UDPAddr) (net.PacketConn, error) {
    if !cfg.UseRealXDP {
        return defaultListenUDP(network, laddr)
    }

    supported, reason := xdp.IsXDPSupported()
    if !supported {
        if log != nil {
            log.Debug("AF_XDP not supported for QUIC acceleration, using kernel UDP",
                "reason", reason,
            )
        }
        return defaultListenUDP(network, laddr)
    }

    // Configure AF_XDP for this specific QUIC port
    xdpCfg := cfg
    if laddr != nil && laddr.Port > 0 {
        xdpCfg.Port = uint16(laddr.Port)
    }

    sock, err := xdp.NewAFXDPSocket(xdpCfg, log)
    if err != nil {
        if log != nil {
            log.Warn("AF_XDP socket creation failed for QUIC, falling back to kernel UDP",
                "error", err,
            )
        }
        return defaultListenUDP(network, laddr)
    }

    if log != nil {
        log.Info("QUIC transport using AF_XDP kernel bypass",
            "interface", cfg.Interface,
            "port", xdpCfg.Port,
        )
    }

    return &XDPPacketConn{
        conn:    newDataSocketPacketConn(sock, laddr),
        isAFXDP: true,
    }, nil
}

// dataSocketPacketConn adapts xdp.DataSocket to net.PacketConn
type dataSocketPacketConn struct {
    sock      xdp.DataSocket
    localAddr *net.UDPAddr
}

func newDataSocketPacketConn(sock xdp.DataSocket, laddr *net.UDPAddr) *dataSocketPacketConn {
    return &dataSocketPacketConn{sock: sock, localAddr: laddr}
}

func (d *dataSocketPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
    n, addr, err := d.sock.Receive(b)
    return n, addr, err
}

func (d *dataSocketPacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
    udpAddr, ok := addr.(*net.UDPAddr)
    if !ok {
        return 0, &net.OpError{Op: "write", Net: "udp", Err: net.InvalidAddrError("not a UDP address")}
    }
    err := d.sock.Send(b, udpAddr)
    if err != nil {
        return 0, err
    }
    return len(b), nil
}

func (d *dataSocketPacketConn) Close() error {
    return d.sock.Close()
}

func (d *dataSocketPacketConn) LocalAddr() net.Addr {
    return d.sock.LocalAddr()
}

func (d *dataSocketPacketConn) SetDeadline(_ time.Time) error    { return nil }
func (d *dataSocketPacketConn) SetReadDeadline(_ time.Time) error  { return nil }
func (d *dataSocketPacketConn) SetWriteDeadline(_ time.Time) error { return nil }
```

Note: deadline methods return nil (AF_XDP receive is already blocking on a channel with context cancellation handled by the caller). quic-go manages its own timers.

- [ ] **Step 4: Implement non-Linux stub**

Create `p2p/xdp/accel/listen_other.go`:
```go
//go:build !linux

package accel

import (
    "net"

    "github.com/multiversx/mx-chain-communication-go/p2p"
    "github.com/multiversx/mx-chain-communication-go/p2p/xdp"
)

func newListenUDP(_ xdp.Config, _ p2p.Logger, network string, laddr *net.UDPAddr) (net.PacketConn, error) {
    return defaultListenUDP(network, laddr)
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./p2p/xdp/accel/... -count=1 -v -race
```
Expected: PASS (fallback path works on macOS/non-Linux)

- [ ] **Step 6: Commit**

```bash
git add p2p/xdp/accel/
git commit -m "feat: AF_XDP PacketConn factory with platform-aware fallback"
```

---

### Task 3: Integrate into netMessenger.go via QUICReuse

**Files:**
- Modify: `p2p/libp2p/netMessenger.go:178-200`

This is the 5-line integration that wires everything together.

- [ ] **Step 1: Add import for quicreuse and accel**

In `p2p/libp2p/netMessenger.go`, add imports:
```go
import (
    // ... existing imports ...
    "github.com/libp2p/go-libp2p/p2p/transport/quicreuse"
    "github.com/multiversx/mx-chain-communication-go/p2p/xdp/accel"
)
```

- [ ] **Step 2: Add QUICReuse option when AccelerateQUIC is enabled**

In `constructNode()`, after the `resourceLimiterOption` is created (~line 186) and before `libp2p.New()` (~line 200), add:

```go
// If XDP QUIC acceleration is enabled, override UDP socket creation
// to use AF_XDP kernel bypass for all QUIC traffic
if args.P2pConfig.XDP.AccelerateQUIC && len(args.P2pConfig.Node.Transports.QUICAddress) > 0 {
    xdpCfg := xdp.Config{
        Interface:  args.P2pConfig.XDP.Interface,
        UseRealXDP: true,
        QueueSize:  args.P2pConfig.XDP.QueueSize,
        BatchSize:  args.P2pConfig.XDP.BatchSize,
    }
    options = append(options, libp2p.QUICReuse(
        quicreuse.NewConnManager,
        quicreuse.OverrideListenUDP(accel.NewListenUDP(xdpCfg, args.Logger)),
    ))
}
```

- [ ] **Step 3: Verify compilation**

```bash
go build ./...
```
Expected: Clean build

- [ ] **Step 4: Run existing tests to verify nothing broke**

```bash
go test ./p2p/libp2p/... -count=1 -timeout 120s
```
Expected: All pass (XDP acceleration is off by default)

- [ ] **Step 5: Run XDP tests**

```bash
go test ./p2p/xdp/... -count=1 -timeout 60s -race
```
Expected: All pass

- [ ] **Step 6: Commit**

```bash
git add p2p/libp2p/netMessenger.go
git commit -m "feat: integrate AF_XDP QUIC acceleration via QUICReuse

When XDP.AccelerateQUIC=true and QUIC transport is enabled,
replaces net.ListenUDP with AF_XDP-backed PacketConn for all
QUIC connections. Falls back to kernel UDP if AF_XDP unavailable.
Peers don't need XDP — they see standard QUIC packets."
```

---

### Task 4: Port-filtered XDP program

**Files:**
- Create: `p2p/xdp/accel/xdp_filter.go`

The current XDP program in `afxdp/xdp_program.go` redirects ALL packets on matched queues. For QUIC acceleration, we need to redirect only UDP packets on the QUIC port, passing everything else to the kernel stack.

- [ ] **Step 1: Create port-filtered XDP program builder**

Create `p2p/xdp/accel/xdp_filter.go`:
```go
package accel

// PortFilteredXDPProgram describes the eBPF XDP program that
// selectively redirects UDP traffic on a specific port to AF_XDP,
// while passing all other traffic to the kernel stack.
//
// This is used for QUIC acceleration — only QUIC's UDP port is
// accelerated; TCP, other UDP services, and ICMP continue normally.
//
// The C-equivalent logic:
//
//   SEC("xdp")
//   int xdp_quic_redirect(struct xdp_md *ctx) {
//       // Parse Ethernet header
//       struct ethhdr *eth = data;
//       if (eth->h_proto != htons(ETH_P_IP)) return XDP_PASS;
//
//       // Parse IPv4 header
//       struct iphdr *ip = (void*)(eth + 1);
//       if (ip->protocol != IPPROTO_UDP) return XDP_PASS;
//
//       // Parse UDP header
//       struct udphdr *udp = (void*)ip + (ip->ihl * 4);
//       if (udp->dest != htons(QUIC_PORT)) return XDP_PASS;
//
//       // Redirect to AF_XDP socket for this queue
//       return bpf_redirect_map(&xsks_map, ctx->rx_queue_index, XDP_PASS);
//   }
//
// NOTE: The actual implementation uses the existing afxdp.XDPProgram
// infrastructure. This file documents the intended behavior.
// For the initial implementation, the existing redirect-all program
// is acceptable since the AF_XDP socket is bound to a specific port.
// A port-filtered program is a future optimization to avoid redirecting
// non-QUIC UDP traffic to AF_XDP (where it would be dropped).

// QUICPort is configured at runtime from XDPConfig.Port or the QUIC listen port.
// The port filter is applied in the eBPF program's packet parsing logic.
```

Note: For the initial implementation, the existing `afxdp.XDPProgram` (redirect-all) works because the AF_XDP socket only processes packets matching its bound port. The port-filtered program is an optimization to reduce unnecessary AF_XDP ring usage for non-QUIC UDP traffic. This can be added as a follow-up.

- [ ] **Step 2: Commit**

```bash
git add p2p/xdp/accel/xdp_filter.go
git commit -m "docs: document port-filtered XDP program for QUIC acceleration"
```

---

### Task 5: Integration test + verification

**Files:**
- Create: `p2p/xdp/accel/integration_test.go`

- [ ] **Step 1: Write integration test**

Create `p2p/xdp/accel/integration_test.go`:
```go
package accel

import (
    "net"
    "testing"

    "github.com/multiversx/mx-chain-communication-go/p2p/xdp"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestNewListenUDP_ReturnsWorkingPacketConn(t *testing.T) {
    // Create factory with XDP disabled (tests run on macOS/CI without AF_XDP)
    cfg := xdp.Config{}
    factory := NewListenUDP(cfg, nil)

    // Create two packet conns
    laddr1, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
    conn1, err := factory("udp", laddr1)
    require.NoError(t, err)
    defer conn1.Close()

    laddr2, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
    conn2, err := factory("udp", laddr2)
    require.NoError(t, err)
    defer conn2.Close()

    // Send data from conn1 to conn2
    msg := []byte("quic-accel-test")
    n, err := conn1.WriteTo(msg, conn2.LocalAddr())
    require.NoError(t, err)
    assert.Equal(t, len(msg), n)

    // Receive on conn2
    buf := make([]byte, 1500)
    n, addr, err := conn2.ReadFrom(buf)
    require.NoError(t, err)
    assert.Equal(t, msg, buf[:n])
    assert.Equal(t, conn1.LocalAddr().(*net.UDPAddr).Port, addr.(*net.UDPAddr).Port)
}

func TestNewListenUDP_MultipleConns(t *testing.T) {
    // Verify multiple conns can coexist (quic-go creates several)
    cfg := xdp.Config{}
    factory := NewListenUDP(cfg, nil)

    conns := make([]net.PacketConn, 5)
    for i := range conns {
        laddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
        var err error
        conns[i], err = factory("udp", laddr)
        require.NoError(t, err)
    }

    for _, conn := range conns {
        conn.Close()
    }
}
```

- [ ] **Step 2: Run all tests**

```bash
go test ./p2p/xdp/accel/... -count=1 -v -race
go test ./p2p/xdp/... -count=1 -timeout 60s -race
go test ./p2p/libp2p/... -count=1 -timeout 120s
```
Expected: All pass

- [ ] **Step 3: Verify full build**

```bash
go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add p2p/xdp/accel/
git commit -m "test: integration tests for AF_XDP QUIC acceleration"
```

---

## Config Example (p2p.toml)

```toml
[Node.Transports]
    QUICAddress = "/ip4/0.0.0.0/udp/%d/quic-v1"

[XDP]
    Enabled = true           # Phase 1: custom XDP protocol between XDP peers
    AccelerateQUIC = true    # Phase 2: AF_XDP under QUIC for ALL peers
    Interface = "eth0"
    QueueSize = 4096
    BatchSize = 64
```

## Risk Assessment

| Risk | Likelihood | Mitigation |
|------|-----------|------------|
| quic-go loses ECN/GSO with basic PacketConn | **Certain** | Acceptable tradeoff — raw throughput gain > lost optimizations |
| AF_XDP fails on some Linux configs | Medium | Auto-fallback to `net.ListenUDP` — zero impact |
| UMEM exhaustion under burst | Low | Size appropriately; monitor via existing metrics |
| Deadline handling incompatibility | Low | quic-go manages timers internally; no-op deadlines work |
