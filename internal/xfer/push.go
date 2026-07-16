package xfer

import (
	"fmt"
	"net"
	"time"

	"github.com/joomcode/errorx"
	"github.com/krabiswabbie/busyscout/internal/helpers"
	"github.com/krabiswabbie/busyscout/internal/telnet"
)

const loaderPath = "/tmp/bs-loader"

// Push uploads a local file to the remote device via fast TCP mode.
// dial creates a new telnet connection — called once per fallback attempt
// for the fileloader upload, plus retained for chmod and execute.
func Push(dial func() (*telnet.TelnetClient, error), localPath, remotePath, isa, libc, hostIP string) error {
	// 1. Select fileloader
	loader, err := helpers.FileloaderForISA(isa, libc)
	if err != nil {
		return errorx.Decorate(err, "unsupported architecture")
	}

	// 2. Upload fileloader via printf with auto-fallback chunk size.
	// Each attempt opens a fresh connection in case the previous one
	// was killed by a too-long command.
	var (
		tc        *telnet.TelnetClient
		uploadErr error
	)
	for _, lineSize := range []int{256, 128} {
		tc, err = dial()
		if err != nil {
			return errorx.Decorate(err, "failed to connect")
		}
		tc.Timeout = 3 * time.Second
		uploadErr = helpers.UploadData(tc, loader, loaderPath, lineSize)
		if uploadErr == nil {
			break
		}
		tc.Close()
	}
	if uploadErr != nil {
		return errorx.Decorate(uploadErr, "failed to upload fileloader")
	}
	defer tc.Close()

	// 3. chmod +x
	if _, err := tc.Execute("chmod", "+x", loaderPath); err != nil {
		return errorx.Decorate(err, "failed to chmod loader")
	}

	// 4. Start TCP listener
	port, ln, err := StartListener()
	if err != nil {
		return errorx.Decorate(err, "failed to start listener")
	}
	defer ln.Close()

	// 5. Execute fileloader on device (in background via & so telnet returns)
	// Determine BusyScout's IP reachable from device — use the same interface as device
	busyIP := getLocalIPForDevice(hostIP)
	if _, err := tc.Execute(loaderPath, "push", busyIP, fmt.Sprintf("%d", port), remotePath); err != nil {
		return errorx.Decorate(err, "failed to start fileloader on device")
	}

	// 6. Accept connection and push file
	if err := AcceptAndPush(ln, localPath); err != nil {
		return errorx.Decorate(err, "fast push failed")
	}

	// 7. Cleanup (best-effort)
	tc.Execute("rm", "-f", loaderPath)

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
