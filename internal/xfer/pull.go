package xfer

import (
	"fmt"

	"github.com/joomcode/errorx"
	"github.com/krabiswabbie/busyscout/internal/helpers"
	"github.com/krabiswabbie/busyscout/internal/telnet"
)

// Pull downloads a remote file from the device via fast TCP mode.
// tc is an open telnet connection to the device.
// remotePath is the file path on the device to download.
// localPath is where to save the downloaded file on the BusyScout host.
// isa and libc are detected architecture info for selecting the correct fileloader.
// hostIP is the device IP, used to determine which local interface to bind the listener on.
func Pull(tc *telnet.TelnetClient, remotePath, localPath, isa, libc, hostIP string) error {
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
	if _, err := tc.Execute(loaderPath, "pull", busyIP, fmt.Sprintf("%d", port), remotePath); err != nil {
		return errorx.Decorate(err, "failed to start fileloader on device")
	}

	// 6. Accept connection and receive file (loader sends TYPE_PULL + TYPE_DATA)
	if err := AcceptAndPull(ln, localPath); err != nil {
		return errorx.Decorate(err, "fast pull failed")
	}

	// 7. Cleanup (best-effort)
	tc.Execute("rm", "-f", loaderPath)

	return nil
}
