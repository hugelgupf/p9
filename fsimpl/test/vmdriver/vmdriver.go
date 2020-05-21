// Package vmdriver implements a u-root/pkg/qemu.Device for networking.
package vmdriver

import (
	"fmt"
	"net"
)

// HostNetwork provides QEMU user-mode networking to the host. HostNetwork
// implements u-root/pkg/qemu.Device.
type HostNetwork struct {
	// Net must be an IPv4 network.
	Net *net.IPNet
}

// Cmdline implements qemu.Device.Cmdline.
func (h HostNetwork) Cmdline() []string {
	return []string{
		"-device", "e1000,netdev=host0",
		"-netdev", fmt.Sprintf("user,id=host0,net=%s,dhcpstart=%s", h.Net, nthIP(h.Net, 8)),
	}
}

// KArgs implements qemu.Device.KArgs.
func (h HostNetwork) KArgs() []string {
	return nil
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
