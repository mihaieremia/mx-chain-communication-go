package accel_test

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/multiversx/mx-chain-communication-go/p2p/xdp/accel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// compile-time interface check in test package as well
var _ net.PacketConn = (*accel.XDPPacketConn)(nil)

func newUDPPair(t *testing.T) (client *net.UDPConn, server *net.UDPConn) {
	t.Helper()

	serverAddr, err := net.ResolveUDPAddr("udp4", "127.0.0.1:0")
	require.NoError(t, err)

	server, err = net.ListenUDP("udp4", serverAddr)
	require.NoError(t, err)

	clientAddr, err := net.ResolveUDPAddr("udp4", "127.0.0.1:0")
	require.NoError(t, err)

	client, err = net.ListenUDP("udp4", clientAddr)
	require.NoError(t, err)

	return client, server
}

func TestWrapUDPConn_IsAFXDP_ReturnsFalse(t *testing.T) {
	t.Parallel()

	_, server := newUDPPair(t)
	defer func() { _ = server.Close() }()

	conn := accel.WrapUDPConn(server)
	assert.False(t, conn.IsAFXDP())
}

func TestWrapUDPConn_WriteTo_DelegatesCorrectly(t *testing.T) {
	t.Parallel()

	client, server := newUDPPair(t)
	defer func() {
		_ = client.Close()
		_ = server.Close()
	}()

	wrapped := accel.WrapUDPConn(client)

	msg := []byte("hello xdp")
	n, err := wrapped.WriteTo(msg, server.LocalAddr())
	require.NoError(t, err)
	assert.Equal(t, len(msg), n)
}

func TestWrapUDPConn_ReadFrom_DelegatesCorrectly(t *testing.T) {
	t.Parallel()

	client, server := newUDPPair(t)
	defer func() {
		_ = client.Close()
		_ = server.Close()
	}()

	wrapped := accel.WrapUDPConn(server)

	msg := []byte("hello xdp")
	_, err := client.WriteTo(msg, server.LocalAddr())
	require.NoError(t, err)

	buf := make([]byte, 64)
	require.NoError(t, wrapped.SetReadDeadline(time.Now().Add(2*time.Second)))
	n, addr, err := wrapped.ReadFrom(buf)
	require.NoError(t, err)
	assert.Equal(t, msg, buf[:n])
	assert.NotNil(t, addr)
}

func TestWrapUDPConn_SetReadDeadline_ExpiredCausesDeadlineExceeded(t *testing.T) {
	t.Parallel()

	_, server := newUDPPair(t)
	defer func() { _ = server.Close() }()

	wrapped := accel.WrapUDPConn(server)

	// set a deadline in the past so any ReadFrom call immediately times out
	err := wrapped.SetReadDeadline(time.Now().Add(-time.Second))
	require.NoError(t, err)

	buf := make([]byte, 64)
	_, _, readErr := wrapped.ReadFrom(buf)
	require.Error(t, readErr)
	assert.True(t, errors.Is(readErr, os.ErrDeadlineExceeded),
		"expected os.ErrDeadlineExceeded, got %v", readErr)
}

func TestWrapUDPConn_LocalAddr_NotNil(t *testing.T) {
	t.Parallel()

	_, server := newUDPPair(t)
	defer func() { _ = server.Close() }()

	wrapped := accel.WrapUDPConn(server)
	assert.NotNil(t, wrapped.LocalAddr())
}

func TestWrapUDPConn_Close_Succeeds(t *testing.T) {
	t.Parallel()

	_, server := newUDPPair(t)

	wrapped := accel.WrapUDPConn(server)
	assert.NoError(t, wrapped.Close())
}
