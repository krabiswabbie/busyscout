package xfer

import (
	"fmt"
	"net"

	"github.com/joomcode/errorx"
	"github.com/krabiswabbie/busyscout/internal/helpers"
	"github.com/krabiswabbie/busyscout/internal/telnet"
)

const loaderPath = "/tmp/bs-loader"

// Push uploads a local file to the remote device via fast TCP mode.
// tc is an open telnet connection to the device.
// isa and libc are detected architecture info for selecting the correct fileloader.
// hostIP is the device IP, used to determine which local interface to bind the listener on.
func Push(tc *telnet.TelnetClient, localPath, remotePath, isa, libc, hostIP string) error {
	// 1. Select fileloader
	loader, err := helpers.FileloaderForISA(isa, libc)
	if err != nil {
		return errorx.Decorate(err, "unsupported architecture")
	}

	// 2. Upload fileloader via printf
	if err := helpers.UploadData(tc, loader, loaderPath); err != nil {
		return errorx.Decorate(err, "failed to upload fileloader")
	}

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
	cmd := fmt.Sprintf("%s push %s %d %s &", loaderPath, busyIP, port, remotePath)
	if _, err := tc.Execute("sh", "-c", cmd); err != nil {
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

// getLocalIPForDevice returns BusyScout's IP address on the interface that routes to deviceIP.
func getLocalIPForDevice(deviceIP string) string {
	devIP := net.ParseIP(deviceIP)
	if devIP == nil {
		return "127.0.0.1"
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
