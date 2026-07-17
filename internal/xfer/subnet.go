// internal/xfer/subnet.go
package xfer

import "net"

// IsSameSubnet returns true if deviceIP shares a subnet with any local network interface.
// Returns false on any error (safe default: fallback to printf).
func IsSameSubnet(deviceIP string) bool {
	devIP := net.ParseIP(deviceIP)
	if devIP == nil {
		return false
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ipNet.Contains(devIP) {
				return true
			}
		}
	}

	return false
}
