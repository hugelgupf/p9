// Package vmdriver implements a u-root/pkg/qemu.Device for networking.
package vmdriver

import (
	"fmt"
	"net"

	"github.com/hugelgupf/vmtest/qemu"
)

// HostNetwork provides QEMU user-mode networking to the host.
//
// Net must be an IPv4 network.
func HostNetwork(net *net.IPNet) qemu.Fn {
	return func(alloc *qemu.IDAllocator, opts *qemu.Options) error {
		if net.IP.To4() == nil {
			return fmt.Errorf("HostNetwork must be configured with an IPv4 address")
		}

		netdevID := alloc.ID("netdev")
		opts.AppendQEMU(
			"-device", fmt.Sprintf("e1000,netdev=%s", netdevID),
			"-netdev", fmt.Sprintf("user,id=%s,net=%s,dhcpstart=%s", netdevID, net, nthIP(net, 8)),
		)
		return nil
	}
}

func inc(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

func nthIP(nt *net.IPNet, n int) net.IP {
	ip := make(net.IP, net.IPv4len)
	copy(ip, nt.IP.To4())
	for i := 0; i < n; i++ {
		inc(ip)
	}
	if !nt.Contains(ip) {
		return nil
	}
	return ip
}
