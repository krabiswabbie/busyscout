package xfer

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/joomcode/errorx"
	"github.com/krabiswabbie/busyscout/internal/helpers"
	"github.com/krabiswabbie/busyscout/internal/telnet"
)

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
		return nil, &SetupError{err}
	}

	return ln, nil
}

// ownedLoaderListener ties scratch cleanup to this invocation's listener. It
// is closed only after AcceptAndPush/Pull has finished all network workers.
type ownedLoaderListener struct {
	net.Listener
	cleanup func()
	once    sync.Once
}

func (l *ownedLoaderListener) Close() error {
	err := l.Listener.Close()
	l.once.Do(l.cleanup)
	return err
}
func (l *ownedLoaderListener) SetDeadline(t time.Time) error {
	return l.Listener.(*net.TCPListener).SetDeadline(t)
}

func runLoader(tc *telnet.TelnetClient, loader []byte, mode, remotePath, hostIP string) (net.Listener, error) {
	if err := telnet.ValidateShellPath(remotePath); err != nil {
		return nil, err
	}
	if mode != "push" && mode != "pull" {
		return nil, fmt.Errorf("unsupported loader mode %q", mode)
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("generate loader directory: %w", err)
	}
	dir := "/tmp/bs-loader-" + hex.EncodeToString(nonce[:])
	// No mkdir -p: an existing object must never become this invocation's resource.
	if _, err := tc.ExecuteChecked("umask 077; mkdir " + telnet.ShellQuote(dir)); err != nil {
		return nil, errorx.Decorate(err, "create loader directory")
	}
	cleanup := func() { tc.ExecuteChecked("rm -rf " + telnet.ShellQuote(dir)) }
	success := false
	defer func() {
		if !success {
			cleanup()
		}
	}()
	loaderPath := dir + "/fileloader"
	if err := helpers.UploadData(tc, loader, loaderPath); err != nil {
		return nil, errorx.Decorate(err, "failed to upload fileloader")
	}
	if _, err := tc.ExecuteChecked("chmod +x " + telnet.ShellQuote(loaderPath)); err != nil {
		return nil, errorx.Decorate(err, "failed to chmod loader")
	}
	port, ln, err := StartListener()
	if err != nil {
		return nil, errorx.Decorate(err, "failed to start listener")
	}
	busyIP := getLocalIPForDevice(hostIP)
	command := telnet.ShellQuote(loaderPath) + " " + telnet.ShellQuote(mode) + " " + telnet.ShellQuote(busyIP) + " " + telnet.ShellQuote(fmt.Sprintf("%d", port)) + " " + telnet.ShellQuote(remotePath)
	if _, err := tc.ExecuteChecked(command); err != nil {
		ln.Close()
		return nil, errorx.Decorate(err, "failed to start fileloader on device")
	}
	success = true
	return &ownedLoaderListener{Listener: ln, cleanup: cleanup}, nil
}
