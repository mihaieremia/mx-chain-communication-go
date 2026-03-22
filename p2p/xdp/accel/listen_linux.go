//go:build linux

package accel

import (
	"net"
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

// dataSocketAdapter adapts xdp.DataSocket to net.PacketConn
type dataSocketAdapter struct {
	sock      xdp.DataSocket
	localAddr *net.UDPAddr
}

func newDataSocketAdapter(sock xdp.DataSocket, laddr *net.UDPAddr) *dataSocketAdapter {
	return &dataSocketAdapter{sock: sock, localAddr: laddr}
}

func (d *dataSocketAdapter) ReadFrom(b []byte) (int, net.Addr, error) {
	n, addr, err := d.sock.Receive(b)
	return n, addr, err
}

func (d *dataSocketAdapter) WriteTo(b []byte, addr net.Addr) (int, error) {
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		return 0, &net.OpError{Op: "write", Net: "udp", Err: net.InvalidAddrError("not a UDP address")}
	}
	if err := d.sock.Send(b, udpAddr); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (d *dataSocketAdapter) Close() error             { return d.sock.Close() }
func (d *dataSocketAdapter) LocalAddr() net.Addr      { return d.localAddr }
func (d *dataSocketAdapter) SetDeadline(_ time.Time) error      { return nil }
func (d *dataSocketAdapter) SetReadDeadline(_ time.Time) error  { return nil }
func (d *dataSocketAdapter) SetWriteDeadline(_ time.Time) error { return nil }
