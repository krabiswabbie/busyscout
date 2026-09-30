package xfer

import (
	"bytes"
	"fmt"
	"net"
	"regexp"

	"github.com/joomcode/errorx"
	"github.com/krabiswabbie/busyscout/internal/helpers"
	"github.com/krabiswabbie/busyscout/internal/telnet"
)

const loaderPath = "/tmp/bs-loader"

// loaderStatusMarker precedes the fileloader's exit status in the shell output
const loaderStatusMarker = "bs-rc="

var loaderStatusRe = regexp.MustCompile(loaderStatusMarker + `(\d+)`)

// SetupError is a fast path failure that happened before any file data was
// transferred, so the transfer can be safely retried in another mode.
type SetupError struct {
	Err error
}

func (e *SetupError) Error() string {
	return e.Err.Error()
}

func (e *SetupError) Unwrap() error {
	return e.Err
}

// startLoader uploads the fileloader to the device and starts it in the given
// mode ("push" or "pull"). It returns the listener the fileloader connects
// back to. Every failure is a SetupError.
func startLoader(tc *telnet.TelnetClient, mode, remotePath, isa, libc, hostIP string) (net.Listener, error) {
	// 1. Select fileloader
	loader, err := helpers.FileloaderForISA(isa, libc)
	if err != nil {
		return nil, &SetupError{errorx.Decorate(err, "unsupported architecture")}
	}

	ln, err := runLoader(tc, loader, mode, remotePath, hostIP)
	if err != nil {
		removeLoader(tc)
		return nil, &SetupError{err}
	}

	return ln, nil
}

func runLoader(tc *telnet.TelnetClient, loader []byte, mode, remotePath, hostIP string) (net.Listener, error) {
	// 2. Upload fileloader via printf
	if err := helpers.UploadData(tc, loader, loaderPath); err != nil {
		return nil, errorx.Decorate(err, "failed to upload fileloader")
	}

	// 3. chmod +x
	if _, err := tc.Execute("chmod", "+x", loaderPath); err != nil {
		return nil, errorx.Decorate(err, "failed to chmod loader")
	}

	// 4. Start TCP listener
	port, ln, err := StartListener()
	if err != nil {
		return nil, errorx.Decorate(err, "failed to start listener")
	}

	// 5. Execute fileloader on device. It daemonizes, so the command returns
	// at once, and its exit status tells whether the loader could start at all.
	// Determine BusyScout's IP reachable from device — use the same interface as device
	busyIP := getLocalIPForDevice(hostIP)
	out, err := tc.Execute(loaderPath, mode, busyIP, fmt.Sprintf("%d", port), remotePath+";", "echo", loaderStatusMarker+"$?")
	if err != nil {
		ln.Close()
		return nil, errorx.Decorate(err, "failed to start fileloader on device")
	}
	if err := loaderStartError(out); err != nil {
		ln.Close()
		return nil, err
	}

	return ln, nil
}

// loaderStartError reports a fileloader that could not start on the device,
// judging by the shell output of the start command. It returns nil if the
// loader started, and also if the shell did not report the exit status.
func loaderStartError(output []byte) error {
	matches := loaderStatusRe.FindAllSubmatchIndex(output, -1)
	if len(matches) == 0 {
		return nil
	}

	last := matches[len(matches)-1]
	status := string(output[last[2]:last[3]])
	if status == "0" {
		return nil
	}

	// The line before the status is what the shell said about the failure,
	// unless it is the tail of the command echo
	before := bytes.TrimSpace(output[:last[0]])
	if idx := bytes.LastIndexByte(before, '\n'); idx >= 0 {
		before = bytes.TrimSpace(before[idx+1:])
	}
	if len(before) == 0 || bytes.Contains(before, []byte(loaderStatusMarker)) {
		return fmt.Errorf("fileloader did not start on the device (exit status %s)", status)
	}

	return fmt.Errorf("fileloader did not start on the device (exit status %s): %s", status, before)
}

// removeLoader deletes the fileloader from the device (best-effort)
func removeLoader(tc *telnet.TelnetClient) {
	tc.Execute("rm", "-f", loaderPath)
}
