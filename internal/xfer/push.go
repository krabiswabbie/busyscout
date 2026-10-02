package xfer

import (
	"net"

	"github.com/joomcode/errorx"
	"github.com/krabiswabbie/busyscout/internal/telnet"
)

// Push uploads a local file to the remote device via fast TCP mode.
func Push(tc *telnet.TelnetClient, localPath, remotePath, isa, libc, hostIP string) error {
	ln, err := startLoader(tc, "push", remotePath, isa, libc, hostIP)
	if err != nil {
		return err
	}
	defer ln.Close()

	// Accept connection and push file
	if err := AcceptAndPush(ln, localPath); err != nil {
		return errorx.Decorate(err, "fast push failed")
	}

	return nil
}

// firstNonLoopbackIP returns the first non-loopback IPv4 address found on local interfaces.
func firstNonLoopbackIP() string {
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	return ""
}

// getLocalIPForDevice returns BusyScout's IP address on the interface that routes to deviceIP.
func getLocalIPForDevice(deviceIP string) string {
	devIP := net.ParseIP(deviceIP)
	if devIP == nil {
		return "127.0.0.1"
	}

	// Loopback: local test — use host.docker.internal (resolved by fileloader via getaddrinfo)
	if devIP.IsLoopback() {
		return "host.docker.internal"
	}

	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ipNet.Contains(devIP) {
				return ipNet.IP.String()
			}
		}
	}

	return "127.0.0.1"
}
