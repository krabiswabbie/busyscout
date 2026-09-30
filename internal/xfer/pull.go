package xfer

import (
	"github.com/joomcode/errorx"
	"github.com/krabiswabbie/busyscout/internal/telnet"
)

// Pull downloads a remote file from the device via fast TCP mode.
// tc is an open telnet connection to the device.
// remotePath is the file path on the device to download.
// localPath is where to save the downloaded file on the BusyScout host.
// isa and libc are detected architecture info for selecting the correct fileloader.
// hostIP is the device IP, used to determine which local interface to bind the listener on.
func Pull(tc *telnet.TelnetClient, remotePath, localPath, isa, libc, hostIP string) error {
	ln, err := startLoader(tc, "pull", remotePath, isa, libc, hostIP)
	if err != nil {
		return err
	}
	defer ln.Close()
	defer removeLoader(tc)

	// Accept connection and receive file (loader sends TYPE_PULL + TYPE_DATA)
	if err := AcceptAndPull(ln, localPath); err != nil {
		return errorx.Decorate(err, "fast pull failed")
	}

	return nil
}
