package accel

import (
	"net"
	"testing"

	"github.com/multiversx/mx-chain-communication-go/p2p/xdp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewListenUDP_FallbackToStandard(t *testing.T) {
	cfg := xdp.Config{} // XDP not configured
	factory := NewListenUDP(cfg, nil)

	laddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	conn, err := factory("udp", laddr)
	require.NoError(t, err)
	defer conn.Close()

	assert.NotNil(t, conn.LocalAddr())
}

func TestNewListenUDP_SendReceive(t *testing.T) {
	cfg := xdp.Config{}
	factory := NewListenUDP(cfg, nil)

	laddr1, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	conn1, err := factory("udp", laddr1)
	require.NoError(t, err)
	defer conn1.Close()

	laddr2, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	conn2, err := factory("udp", laddr2)
	require.NoError(t, err)
	defer conn2.Close()

	msg := []byte("quic-accel-test")
	n, err := conn1.WriteTo(msg, conn2.LocalAddr())
	require.NoError(t, err)
	assert.Equal(t, len(msg), n)

	buf := make([]byte, 1500)
	n, addr, err := conn2.ReadFrom(buf)
	require.NoError(t, err)
	assert.Equal(t, msg, buf[:n])
	assert.Equal(t, conn1.LocalAddr().(*net.UDPAddr).Port, addr.(*net.UDPAddr).Port)
}
