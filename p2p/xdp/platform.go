package xdp

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// PlatformInfo contains information about XDP support on the current platform
type PlatformInfo struct {
	Supported     bool
	Reason        string
	KernelVersion string
	HasNetAdmin   bool
	Interfaces    []string
}

// IsXDPSupported checks if XDP is supported on the current platform
func IsXDPSupported() (bool, string) {
	info := GetPlatformInfo()
	return info.Supported, info.Reason
}

// GetPlatformInfo returns detailed information about XDP support
func GetPlatformInfo() PlatformInfo {
	info := PlatformInfo{
		Supported: false,
	}

	// Check OS - XDP is Linux only
	if runtime.GOOS != "linux" {
		info.Reason = fmt.Sprintf("XDP is only supported on Linux, current OS: %s", runtime.GOOS)
		return info
	}

	// Check kernel version
	kernelVersion, err := getKernelVersion()
	if err != nil {
		info.Reason = fmt.Sprintf("failed to get kernel version: %v", err)
		return info
	}
	info.KernelVersion = kernelVersion

	major, minor, err := parseKernelVersion(kernelVersion)
	if err != nil {
		info.Reason = fmt.Sprintf("failed to parse kernel version: %v", err)
		return info
	}

	// XDP requires kernel 4.18+
	if major < 4 || (major == 4 && minor < 18) {
		info.Reason = fmt.Sprintf("kernel version %s is too old, XDP requires 4.18+", kernelVersion)
		return info
	}

	// Check for CAP_NET_ADMIN
	info.HasNetAdmin = hasNetAdminCapability()
	if !info.HasNetAdmin {
		info.Reason = "CAP_NET_ADMIN capability is required for XDP"
		return info
	}

	// Get available interfaces
	info.Interfaces = getNetworkInterfaces()
	if len(info.Interfaces) == 0 {
		info.Reason = "no suitable network interfaces found"
		return info
	}

	info.Supported = true
	info.Reason = "XDP is supported"
	return info
}

// getKernelVersion reads the kernel version from /proc/version
func getKernelVersion() (string, error) {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return "", err
	}

	// Format: "Linux version X.Y.Z..."
	parts := strings.Fields(string(data))
	if len(parts) < 3 {
		return "", fmt.Errorf("unexpected /proc/version format")
	}

	return parts[2], nil
}

// parseKernelVersion extracts major and minor version numbers
func parseKernelVersion(version string) (int, int, error) {
	// Remove any suffix after the version number
	version = strings.Split(version, "-")[0]
	parts := strings.Split(version, ".")

	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("invalid kernel version format: %s", version)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("failed to parse major version: %v", err)
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("failed to parse minor version: %v", err)
	}

	return major, minor, nil
}

// hasNetAdminCapability checks if the process has CAP_NET_ADMIN
func hasNetAdminCapability() bool {
	// Try to read capabilities from /proc/self/status
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		// If running as root, assume we have the capability
		return os.Geteuid() == 0
	}

	// Look for CapEff (effective capabilities)
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "CapEff:") {
			// CAP_NET_ADMIN is bit 12 (0x1000)
			// The capability is represented as a hex number
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			caps, err := strconv.ParseUint(parts[1], 16, 64)
			if err != nil {
				continue
			}
			// Check if CAP_NET_ADMIN (bit 12) is set
			return (caps & (1 << 12)) != 0
		}
	}

	// Fallback: check if running as root
	return os.Geteuid() == 0
}

// getNetworkInterfaces returns a list of suitable network interfaces for XDP
func getNetworkInterfaces() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var result []string
	for _, iface := range interfaces {
		// Skip loopback and down interfaces
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if iface.Flags&net.FlagUp == 0 {
			continue
		}

		// Skip virtual interfaces (usually)
		name := iface.Name
		if strings.HasPrefix(name, "veth") ||
			strings.HasPrefix(name, "docker") ||
			strings.HasPrefix(name, "br-") ||
			strings.HasPrefix(name, "virbr") {
			continue
		}

		result = append(result, name)
	}

	return result
}

// GetDefaultInterface returns the default network interface for XDP
func GetDefaultInterface() (string, error) {
	interfaces := getNetworkInterfaces()
	if len(interfaces) == 0 {
		return "", ErrInterfaceNotFound
	}

	// Prefer eth0, ens*, enp* interfaces
	for _, iface := range interfaces {
		if iface == "eth0" ||
			strings.HasPrefix(iface, "ens") ||
			strings.HasPrefix(iface, "enp") {
			return iface, nil
		}
	}

	// Return the first available interface
	return interfaces[0], nil
}

// CheckInterfaceXDPSupport checks if a specific interface supports XDP
func CheckInterfaceXDPSupport(ifaceName string) error {
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return ErrInterfaceNotFound
	}

	if iface.Flags&net.FlagUp == 0 {
		return fmt.Errorf("interface %s is down", ifaceName)
	}

	// Additional driver-level checks would require ethtool or similar
	// For now, we assume support and let the socket creation fail if not supported

	return nil
}
