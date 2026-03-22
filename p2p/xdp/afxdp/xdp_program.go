//go:build linux

package afxdp

import (
	"fmt"
	"net"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/asm"
	"github.com/cilium/ebpf/link"
	"golang.org/x/sys/unix"
)

// XDPProgram manages the XDP program and XSKMAP for AF_XDP
type XDPProgram struct {
	// eBPF objects
	prog   *ebpf.Program
	xskMap *ebpf.Map

	// Link to interface
	link link.Link

	// Interface info
	ifname  string
	ifindex int

	// Configuration
	mode XDPMode
}

// XDP program bytecode (compiled from C)
// This is a minimal XDP program that redirects all UDP packets on the target port
// to the AF_XDP socket.
//
// The C source equivalent:
//   #include <linux/bpf.h>
//   #include <linux/if_ether.h>
//   #include <linux/ip.h>
//   #include <linux/udp.h>
//   #include <bpf/bpf_helpers.h>
//
//   struct bpf_map_def SEC("maps") xsks_map = {
//       .type = BPF_MAP_TYPE_XSKMAP,
//       .key_size = sizeof(int),
//       .value_size = sizeof(int),
//       .max_entries = 64,
//   };
//
//   SEC("xdp")
//   int xdp_redirect_xsk(struct xdp_md *ctx) {
//       int index = ctx->rx_queue_index;
//       if (bpf_map_lookup_elem(&xsks_map, &index))
//           return bpf_redirect_map(&xsks_map, index, XDP_PASS);
//       return XDP_PASS;
//   }

// XDPProgramSpec defines the XDP program specification for loading
type XDPProgramSpec struct {
	// Program bytecode (eBPF instructions)
	Instructions asm.Instructions

	// XSKMAP specification
	XSKMapSpec *ebpf.MapSpec
}

// NewXDPProgram creates and loads an XDP program
func NewXDPProgram(ifname string, mode XDPMode) (*XDPProgram, error) {
	// Get interface index
	iface, err := net.InterfaceByName(ifname)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInterfaceNotFound, err)
	}

	p := &XDPProgram{
		ifname:  ifname,
		ifindex: iface.Index,
		mode:    mode,
	}

	// Create XSKMAP
	mapSpec := &ebpf.MapSpec{
		Type:       ebpf.XSKMap,
		KeySize:    4, // int (queue index)
		ValueSize:  4, // int (socket fd)
		MaxEntries: 64, // Support up to 64 queues
		Name:       "xsks_map",
	}

	xskMap, err := ebpf.NewMap(mapSpec)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrXDPMapCreate, err)
	}
	p.xskMap = xskMap

	// Create XDP program that redirects to XSKMAP
	progSpec := &ebpf.ProgramSpec{
		Type:    ebpf.XDP,
		License: "GPL",
		Name:    "xdp_redirect",
		Instructions: buildXDPRedirectProgram(xskMap.FD()),
	}

	prog, err := ebpf.NewProgram(progSpec)
	if err != nil {
		xskMap.Close()
		return nil, fmt.Errorf("%w: %v", ErrXDPLoad, err)
	}
	p.prog = prog

	// Attach XDP program to interface
	if err := p.attach(); err != nil {
		prog.Close()
		xskMap.Close()
		return nil, err
	}

	return p, nil
}

// buildXDPRedirectProgram builds the XDP redirect program instructions
// This creates a minimal XDP program that:
// 1. Gets the RX queue index
// 2. Looks up the queue in XSKMAP
// 3. If found, redirects to the AF_XDP socket
// 4. Otherwise, passes the packet to the kernel
func buildXDPRedirectProgram(xskMapFd int) asm.Instructions {
	// eBPF instruction builder
	// This is the equivalent of the C program above
	return asm.Instructions{
		// r6 = ctx
		asm.Mov.Reg(asm.R6, asm.R1),

		// r1 = ctx->rx_queue_index (offset 16 in xdp_md)
		asm.LoadMem(asm.R1, asm.R6, 16, asm.Word),

		// Store queue index on stack (for map lookup)
		asm.StoreMem(asm.RFP, -4, asm.R1, asm.Word),

		// r2 = &queue_index (stack pointer)
		asm.Mov.Reg(asm.R2, asm.RFP),
		asm.Add.Imm(asm.R2, -4),

		// r1 = xsks_map fd (map lookup arg)
		asm.LoadMapPtr(asm.R1, xskMapFd),

		// call bpf_map_lookup_elem
		asm.BuiltinFunc(asm.FnMapLookupElem).Call(),

		// if (r0 == NULL) goto pass
		asm.JEq.Imm(asm.R0, 0, "pass"),

		// r2 = ctx->rx_queue_index (redirect index)
		asm.LoadMem(asm.R2, asm.R6, 16, asm.Word),

		// r3 = XDP_PASS (fallback action)
		asm.Mov.Imm(asm.R3, XDP_PASS),

		// r1 = xsks_map fd
		asm.LoadMapPtr(asm.R1, xskMapFd),

		// call bpf_redirect_map
		asm.BuiltinFunc(asm.FnRedirectMap).Call(),

		// return r0 (redirect result)
		asm.Return(),

		// pass: return XDP_PASS
		asm.Mov.Imm(asm.R0, XDP_PASS).WithSymbol("pass"),
		asm.Return(),
	}
}

// attach attaches the XDP program to the interface
func (p *XDPProgram) attach() error {
	var flags link.XDPAttachFlags

	switch p.mode {
	case XDPModeNative:
		flags = link.XDPDriverMode
	case XDPModeSKB:
		flags = link.XDPGenericMode
	case XDPModeHW:
		flags = link.XDPOffloadMode
	default:
		// Auto mode: try native first, then SKB
		flags = 0
	}

	l, err := link.AttachXDP(link.XDPOptions{
		Program:   p.prog,
		Interface: p.ifindex,
		Flags:     flags,
	})
	if err != nil {
		// If auto mode failed, try SKB mode
		if p.mode == XDPModeAuto {
			l, err = link.AttachXDP(link.XDPOptions{
				Program:   p.prog,
				Interface: p.ifindex,
				Flags:     link.XDPGenericMode,
			})
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrXDPAttach, err)
		}
	}

	p.link = l
	return nil
}

// RegisterSocket registers an AF_XDP socket in the XSKMAP
func (p *XDPProgram) RegisterSocket(queueID, socketFd int) error {
	if err := p.xskMap.Put(uint32(queueID), uint32(socketFd)); err != nil {
		return fmt.Errorf("%w: queue %d: %v", ErrXDPMapUpdate, queueID, err)
	}
	return nil
}

// UnregisterSocket removes an AF_XDP socket from the XSKMAP
func (p *XDPProgram) UnregisterSocket(queueID int) error {
	if err := p.xskMap.Delete(uint32(queueID)); err != nil {
		return fmt.Errorf("failed to delete from XSKMAP: queue %d: %v", queueID, err)
	}
	return nil
}

// Close detaches the XDP program and releases resources
func (p *XDPProgram) Close() error {
	var errs []error

	if p.link != nil {
		if err := p.link.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to detach XDP: %w", err))
		}
	}

	if p.prog != nil {
		if err := p.prog.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close program: %w", err))
		}
	}

	if p.xskMap != nil {
		if err := p.xskMap.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close map: %w", err))
		}
	}

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// IsNativeMode returns true if the program is attached in native/driver mode
func (p *XDPProgram) IsNativeMode() bool {
	// Would need to query the link to determine the actual mode
	return p.mode == XDPModeNative
}

// LoadXDPProgramFromFile loads an XDP program from an ELF file
// This is useful for custom XDP programs
func LoadXDPProgramFromFile(path string, ifname string, mode XDPMode) (*XDPProgram, error) {
	iface, err := net.InterfaceByName(ifname)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInterfaceNotFound, err)
	}

	// Load collection from ELF file
	spec, err := ebpf.LoadCollectionSpec(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load eBPF spec: %w", err)
	}

	// Find the XDP program
	var progSpec *ebpf.ProgramSpec
	for _, ps := range spec.Programs {
		if ps.Type == ebpf.XDP {
			progSpec = ps
			break
		}
	}
	if progSpec == nil {
		return nil, fmt.Errorf("no XDP program found in %s", path)
	}

	// Find the XSKMAP
	var mapSpec *ebpf.MapSpec
	for _, ms := range spec.Maps {
		if ms.Type == ebpf.XSKMap {
			mapSpec = ms
			break
		}
	}
	if mapSpec == nil {
		return nil, fmt.Errorf("no XSKMAP found in %s", path)
	}

	p := &XDPProgram{
		ifname:  ifname,
		ifindex: iface.Index,
		mode:    mode,
	}

	// Create the map
	xskMap, err := ebpf.NewMap(mapSpec)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrXDPMapCreate, err)
	}
	p.xskMap = xskMap

	// Create the program
	prog, err := ebpf.NewProgram(progSpec)
	if err != nil {
		xskMap.Close()
		return nil, fmt.Errorf("%w: %v", ErrXDPLoad, err)
	}
	p.prog = prog

	// Attach
	if err := p.attach(); err != nil {
		prog.Close()
		xskMap.Close()
		return nil, err
	}

	return p, nil
}

// GetXDPStats returns XDP-level statistics from the kernel
func (p *XDPProgram) GetXDPStats() (map[string]uint64, error) {
	// This would query the /sys/class/net/IFACE/xdp_stats
	// or use netlink to get XDP stats
	stats := make(map[string]uint64)

	// Read from sysfs (best effort)
	// Real implementation would use netlink
	return stats, nil
}

// detachXDPRaw detaches XDP program using raw netlink
// This is a fallback if cilium/ebpf link doesn't work
func detachXDPRaw(ifindex int) error {
	// Use netlink to detach XDP
	// This is a simplified version - real implementation would use
	// the rtnetlink package
	_, _, errno := unix.Syscall6(
		unix.SYS_IOCTL,
		uintptr(ifindex),
		0, // SIOCETHTOOL equivalent for XDP detach
		0, 0, 0, 0,
	)
	if errno != 0 {
		return fmt.Errorf("failed to detach XDP: %v", errno)
	}
	return nil
}
