package xdp

import (
	"runtime"

	"github.com/multiversx/mx-chain-communication-go/p2p"
)

// NewDataSocket creates the best available socket implementation
// On Linux with AF_XDP support and UseRealXDP=true, it attempts to create
// a real AF_XDP socket with kernel bypass. If that fails or is disabled,
// it falls back to a standard UDP socket.
func NewDataSocket(config Config, log p2p.Logger) (DataSocket, error) {
	if log == nil {
		return nil, ErrNilLogger
	}

	// Only attempt AF_XDP on Linux with proper support
	if runtime.GOOS == "linux" && config.UseRealXDP {
		supported, reason := IsXDPSupported()
		if supported {
			sock, err := NewAFXDPSocket(config, log)
			if err == nil {
				log.Info("Using real AF_XDP socket for kernel bypass",
					"interface", config.Interface,
					"mode", config.XDPMode,
				)
				return sock, nil
			}
			log.Warn("AF_XDP socket creation failed, falling back to UDP",
				"error", err,
				"interface", config.Interface,
			)
		} else {
			log.Debug("AF_XDP not supported, using UDP socket",
				"reason", reason,
			)
		}
	}

	// Fallback to UDP socket
	sock, err := NewUDPSocket(config, log)
	if err != nil {
		return nil, err
	}

	log.Info("Using UDP socket",
		"address", sock.LocalAddr().String(),
		"interface", config.Interface,
	)

	return sock, nil
}

// MustNewDataSocket creates a socket or panics
// This is useful for initialization where failure should be fatal
func MustNewDataSocket(config Config, log p2p.Logger) DataSocket {
	sock, err := NewDataSocket(config, log)
	if err != nil {
		panic("failed to create data socket: " + err.Error())
	}
	return sock
}

// SocketInfo contains information about a created socket
type SocketInfo struct {
	Type      SocketType
	LocalAddr string
	Interface string
}

// GetSocketInfo returns information about the socket type in use
func GetSocketInfo(sock DataSocket) SocketInfo {
	info := SocketInfo{
		Type:      SocketTypeUDP,
		LocalAddr: sock.LocalAddr().String(),
	}

	// Check if it's an AF_XDP socket
	if _, ok := sock.(*AFXDPSocket); ok {
		info.Type = SocketTypeAFXDP
	}

	return info
}
