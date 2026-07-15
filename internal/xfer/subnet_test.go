// internal/xfer/subnet_test.go
package xfer

import (
	"net"
	"testing"
)

func TestIsSameSubnet_Same(t *testing.T) {
	// This test relies on actual network interfaces.
	// Skip in CI without network.
	if testing.Short() {
		t.Skip("skipping network-dependent test in short mode")
	}

	// Find a local IP and test against it
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
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
			ip := ipNet.IP
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			if ip.To4() != nil {
				// Test with the interface's own IP — must be same subnet
				if !IsSameSubnet(ip.String()) {
					t.Errorf("IsSameSubnet(%s) should be true (own interface IP)", ip.String())
				}
				return
			}
		}
	}
	t.Skip("no suitable IPv4 interface found")
}

func TestIsSameSubnet_NoInterfaces(t *testing.T) {
	// 8.8.8.8 is not in any local subnet
	if IsSameSubnet("8.8.8.8") {
		t.Error("IsSameSubnet(8.8.8.8) should be false")
	}
}
