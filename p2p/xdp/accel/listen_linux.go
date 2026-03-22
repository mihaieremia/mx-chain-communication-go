//go:build linux

package accel

import (
	"net"
	"os"
	"sync/atomic"
	"time"

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
			log.Debug("AF_XDP not supported for QUIC acceleration, using kernel UDP", "reason", reason)
		}
		return defaultListenUDP(network, laddr)
	}

	xdpCfg := cfg
	if laddr != nil && laddr.Port > 0 {
		xdpCfg.Port = uint16(laddr.Port)
	}

	sock, err := xdp.NewAFXDPSocket(xdpCfg, log)
	if err != nil {
		if log != nil {
			log.Warn("AF_XDP socket failed for QUIC, falling back to kernel UDP", "error", err)
		}
		return defaultListenUDP(network, laddr)
	}

	if log != nil {
		log.Info("QUIC transport using AF_XDP kernel bypass", "interface", cfg.Interface, "port", xdpCfg.Port)
	}

	return &XDPPacketConn{
		conn:    newDataSocketAdapter(sock, laddr),
		isAFXDP: true,
	}, nil
}

// dataSocketAdapter adapts xdp.DataSocket to net.PacketConn.
// It stores read/write deadlines so that quic-go idle-timeout and draining
// logic works correctly.  When a deadline has already expired at the time
// ReadFrom is called, os.ErrDeadlineExceeded is returned immediately.
type dataSocketAdapter struct {
	sock          xdp.DataSocket
	localAddr     *net.UDPAddr
	readDeadline  atomic.Value // stores time.Time
	writeDeadline atomic.Value // stores time.Time
}

func newDataSocketAdapter(sock xdp.DataSocket, laddr *net.UDPAddr) *dataSocketAdapter {
	return &dataSocketAdapter{sock: sock, localAddr: laddr}
}

func (d *dataSocketAdapter) ReadFrom(b []byte) (int, net.Addr, error) {
	if dl, ok := d.readDeadline.Load().(time.Time); ok && !dl.IsZero() {
		if time.Now().After(dl) {
			return 0, nil, os.ErrDeadlineExceeded
		}
	}
	n, addr, err := d.sock.Receive(b)
	return n, addr, err
}

func (d *dataSocketAdapter) WriteTo(b []byte, addr net.Addr) (int, error) {
	if dl, ok := d.writeDeadline.Load().(time.Time); ok && !dl.IsZero() {
		if time.Now().After(dl) {
			return 0, os.ErrDeadlineExceeded
		}
	}
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		return 0, &net.OpError{Op: "write", Net: "udp", Err: net.InvalidAddrError("not a UDP address")}
	}
	if err := d.sock.Send(b, udpAddr); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (d *dataSocketAdapter) Close() error        { return d.sock.Close() }
func (d *dataSocketAdapter) LocalAddr() net.Addr  { return d.localAddr }

func (d *dataSocketAdapter) SetDeadline(t time.Time) error {
	d.readDeadline.Store(t)
	d.writeDeadline.Store(t)
	return nil
}

func (d *dataSocketAdapter) SetReadDeadline(t time.Time) error {
	d.readDeadline.Store(t)
	return nil
}

func (d *dataSocketAdapter) SetWriteDeadline(t time.Time) error {
	d.writeDeadline.Store(t)
	return nil
}
