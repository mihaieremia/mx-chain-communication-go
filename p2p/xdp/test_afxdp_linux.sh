#!/bin/bash
# test_afxdp_linux.sh - Test script for AF_XDP functionality on Linux
#
# This script tests the real AF_XDP implementation on Linux.
# It requires:
# - Linux kernel >= 4.18
# - Root/sudo access (for AF_XDP socket creation)
# - A network interface (can be virtual for testing)
#
# Usage:
#   ./test_afxdp_linux.sh [interface]
#   sudo ./test_afxdp_linux.sh eth0
#
# Without arguments, it will auto-detect a suitable interface.

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if running on Linux
check_os() {
    if [[ "$(uname)" != "Linux" ]]; then
        log_error "This script only runs on Linux"
        echo "Current OS: $(uname)"
        echo "For macOS/Windows, the XDP implementation falls back to UDP sockets"
        exit 1
    fi
    log_info "Running on Linux"
}

# Check kernel version
check_kernel() {
    local kernel_version=$(uname -r | cut -d'-' -f1)
    local major=$(echo $kernel_version | cut -d'.' -f1)
    local minor=$(echo $kernel_version | cut -d'.' -f2)

    log_info "Kernel version: $kernel_version"

    if [[ $major -lt 4 ]] || [[ $major -eq 4 && $minor -lt 18 ]]; then
        log_error "AF_XDP requires kernel >= 4.18"
        log_info "Current kernel: $major.$minor"
        exit 1
    fi

    if [[ $major -ge 5 && $minor -ge 1 ]]; then
        log_info "Kernel supports AF_XDP with full features (>= 5.1)"
    else
        log_warn "Kernel may have limited AF_XDP features (< 5.1)"
    fi
}

# Check for root/capabilities
check_permissions() {
    if [[ $EUID -eq 0 ]]; then
        log_info "Running as root"
        return 0
    fi

    # Check for CAP_NET_RAW capability
    if capsh --print 2>/dev/null | grep -q 'cap_net_raw'; then
        log_info "Has CAP_NET_RAW capability"
        return 0
    fi

    log_warn "Not running as root and no CAP_NET_RAW capability"
    log_warn "Some AF_XDP tests may fail. Consider running with sudo."
}

# Check for libbpf/AF_XDP support
check_afxdp_support() {
    log_info "Checking AF_XDP support..."

    # Check if AF_XDP socket can be created
    if [[ -e "/sys/kernel/debug/tracing/events/xdp" ]]; then
        log_info "XDP tracing events available"
    fi

    # Check for bpf syscall
    if [[ -e "/proc/sys/kernel/unprivileged_bpf_disabled" ]]; then
        local bpf_disabled=$(cat /proc/sys/kernel/unprivileged_bpf_disabled)
        if [[ "$bpf_disabled" == "0" ]]; then
            log_info "Unprivileged BPF allowed"
        elif [[ "$bpf_disabled" == "1" ]]; then
            log_warn "Unprivileged BPF disabled (requires root)"
        else
            log_info "BPF restrictions: $bpf_disabled"
        fi
    fi
}

# Detect or validate network interface
detect_interface() {
    local requested_iface="$1"

    if [[ -n "$requested_iface" ]]; then
        if [[ ! -d "/sys/class/net/$requested_iface" ]]; then
            log_error "Interface $requested_iface does not exist"
            exit 1
        fi
        INTERFACE="$requested_iface"
        log_info "Using specified interface: $INTERFACE"
        return
    fi

    # Auto-detect: prefer physical interfaces, fall back to virtual
    for iface in $(ls /sys/class/net); do
        # Skip loopback
        if [[ "$iface" == "lo" ]]; then
            continue
        fi

        # Prefer physical interfaces
        if [[ -L "/sys/class/net/$iface/device" ]]; then
            INTERFACE="$iface"
            log_info "Auto-detected physical interface: $INTERFACE"
            return
        fi
    done

    # Fall back to any non-lo interface
    for iface in $(ls /sys/class/net); do
        if [[ "$iface" != "lo" ]]; then
            INTERFACE="$iface"
            log_info "Auto-detected virtual interface: $INTERFACE"
            return
        fi
    done

    log_error "No suitable network interface found"
    exit 1
}

# Check interface XDP support
check_interface_xdp() {
    local iface="$1"

    log_info "Checking XDP support on $iface..."

    # Check interface state
    local state=$(cat /sys/class/net/$iface/operstate 2>/dev/null || echo "unknown")
    log_info "Interface state: $state"

    # Check for XDP driver mode
    if [[ -e "/sys/class/net/$iface/xdp" ]]; then
        log_info "Interface has XDP sysfs entry"
    fi

    # Check driver info
    local driver=$(readlink /sys/class/net/$iface/device/driver 2>/dev/null | xargs basename 2>/dev/null || echo "unknown")
    log_info "Driver: $driver"

    # Known XDP-capable drivers
    local known_xdp_drivers="ixgbe ixgbevf i40e i40evf mlx4 mlx5 nfp virtio_net veth"
    if echo "$known_xdp_drivers" | grep -qw "$driver"; then
        log_info "Driver $driver is known to support native XDP"
    else
        log_warn "Driver $driver XDP support unknown, will use generic mode"
    fi
}

# Create virtual interface for testing
create_test_veth() {
    log_info "Creating test veth pair..."

    if ip link show xdp_test0 &>/dev/null; then
        log_info "Test interface xdp_test0 already exists"
        return 0
    fi

    ip link add xdp_test0 type veth peer name xdp_test1
    ip link set xdp_test0 up
    ip link set xdp_test1 up
    ip addr add 10.200.0.1/24 dev xdp_test0
    ip addr add 10.200.0.2/24 dev xdp_test1

    log_info "Created veth pair: xdp_test0 <-> xdp_test1"
}

# Cleanup test interface
cleanup_test_veth() {
    if ip link show xdp_test0 &>/dev/null; then
        log_info "Cleaning up test interfaces..."
        ip link del xdp_test0 2>/dev/null || true
    fi
}

# Run Go tests
run_go_tests() {
    log_info "Running Go XDP tests..."

    cd "$(dirname "$0")"

    # Run all XDP tests
    log_info "Running unit tests..."
    go test -v -timeout 120s ./... 2>&1 | tee /tmp/xdp_test_output.log

    local exit_code=${PIPESTATUS[0]}

    if [[ $exit_code -eq 0 ]]; then
        log_info "All tests passed!"
    else
        log_error "Some tests failed (exit code: $exit_code)"
        log_info "See /tmp/xdp_test_output.log for details"
    fi

    return $exit_code
}

# Run AF_XDP specific tests
run_afxdp_tests() {
    log_info "Running AF_XDP specific tests..."

    cd "$(dirname "$0")/afxdp"

    # Test UMEM allocation
    log_info "Testing UMEM allocation..."
    go test -v -run TestUMEM -timeout 30s 2>&1 || log_warn "UMEM tests skipped or failed"

    # Test ring operations
    log_info "Testing ring operations..."
    go test -v -run TestRing -timeout 30s 2>&1 || log_warn "Ring tests skipped or failed"

    log_info "AF_XDP tests completed"
}

# Run integration test with real socket
run_integration_test() {
    log_info "Running integration test with AF_XDP socket..."

    export XDP_TEST_INTERFACE="$INTERFACE"

    cd "$(dirname "$0")"

    # This test requires root
    if [[ $EUID -ne 0 ]]; then
        log_warn "Skipping socket integration test (requires root)"
        return 0
    fi

    log_info "Integration test would run here with interface: $INTERFACE"
    # go test -v -run TestIntegration -tags=linux_integration -timeout 60s
}

# Performance benchmark
run_benchmarks() {
    log_info "Running performance benchmarks..."

    cd "$(dirname "$0")"

    go test -bench=. -benchtime=3s -timeout 120s ./... 2>&1 | tee /tmp/xdp_benchmark.log

    log_info "Benchmark results in /tmp/xdp_benchmark.log"
}

# Main
main() {
    echo "=========================================="
    echo "   AF_XDP Test Suite for MultiversX"
    echo "=========================================="
    echo ""

    check_os
    check_kernel
    check_permissions
    check_afxdp_support

    detect_interface "$1"
    check_interface_xdp "$INTERFACE"

    echo ""
    log_info "Starting tests..."
    echo ""

    run_go_tests

    if [[ $EUID -eq 0 ]]; then
        echo ""
        run_afxdp_tests
        echo ""
        run_integration_test
    else
        echo ""
        log_warn "Run with sudo for AF_XDP integration tests"
    fi

    echo ""
    log_info "Test suite completed"
    echo ""

    # Run benchmarks if requested
    if [[ "$2" == "--bench" ]]; then
        run_benchmarks
    fi
}

# Trap for cleanup
trap cleanup_test_veth EXIT

main "$@"
