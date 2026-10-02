package scout

import (
	_ "embed"
	"errors"
	"fmt"
	"github.com/joomcode/errorx"
	"github.com/krabiswabbie/busyscout/internal/telnet"
	"github.com/krabiswabbie/busyscout/internal/xfer"
	"github.com/schollz/progressbar/v3"
	"os"
	"path/filepath"
	"strings"
)

const (
	threads   = 10
	retries   = 5
	chunkSize = 1024
	lineSize  = 128
	tmpDir    = "/tmp"
)

type Scout struct {
	localFile string
	remote    *RemoteFile
	verbose   bool
	mode      Mode
	bar       *progressbar.ProgressBar
	isa       string // cached ISA from light detection
	libc      string // cached libc from light detection
	endian    string // "little" or "big" (only for MIPS)
}

func New(source, target string, mode Mode, verboseFlag bool) (*Scout, error) {
	_, err := os.Stat(source)
	if err != nil {
		return nil, errorx.Decorate(err, "source file does not exist")
	}

	remote, err := ParseRemoteFileName(target)
	if err != nil {
		return nil, errorx.Decorate(err, "failed to parse remote address")
	}

	s := &Scout{
		localFile: source,
		remote:    remote,
		verbose:   verboseFlag,
		mode:      mode,
	}

	// Default to /tmp if no remote path specified
	if remote.Path == "" {
		remote.Path = "/tmp/" + filepath.Base(source)
	}

	// Add the target filename if only target directory is specified
	isDir, errDir := s.checkIsRemoteDirectory(remote.Path)
	if errDir != nil {
		return nil, errorx.Decorate(errDir, "failed to check remote directory")
	}
	if isDir {
		// Preserve the directory spelling: cleaning link/.. can redirect the
		// destination when link refers to a directory elsewhere.
		if !strings.HasSuffix(s.remote.Path, "/") {
			s.remote.Path += "/"
		}
		s.remote.Path += filepath.Base(source)
		isDir, errDir = s.checkIsRemoteDirectory(s.remote.Path)
		if errDir != nil {
			return nil, errorx.Decorate(errDir, "failed to check remote destination")
		}
		if isDir {
			return nil, fmt.Errorf("remote destination %q is not a regular file", s.remote.Path)
		}
	}

	return s, nil
}

func (s *Scout) newClient() (*telnet.TelnetClient, error) {
	tc := &telnet.TelnetClient{
		Address:  s.remote.Host,
		Port:     s.remote.Port,
		Login:    s.remote.Username,
		Password: s.remote.Password,
		Verbose:  s.verbose,
	}

	if errDial := tc.Dial(); errDial != nil {
		return nil, errorx.Decorate(errDial, "failed to open telnet connection")
	}

	return tc, nil
}

// detectISALight runs a quick ISA+libc detection on the device.
func (s *Scout) detectISALight() error {
	if s.isa != "" {
		return nil // already cached
	}

	tc, err := s.newClient()
	if err != nil {
		return err
	}
	defer tc.Close()

	// uname -m → ISA
	stdout, err := tc.Execute("uname", "-m")
	if err != nil {
		return errorx.Decorate(err, "uname failed")
	}
	s.isa = parseUnameMachine(string(stdout))

	// ls libc → libc family
	// Check musl first (Alpine containers) — /lib/ld-musl-* exists only on musl
	stdout, err = tc.Execute("sh -c 'ls /lib/ld-musl-* 2>/dev/null && echo MUSL_DETECTED; ls -l /lib/libc.so* /lib/ld-*.so* 2>/dev/null || true'")
	if err == nil {
		s.libc = parseLibcFamily(string(stdout))
	}

	// MIPS endianness detection
	if s.isa == "mips" {
		stdout, err = tc.Execute("grep", "-i", "mipsel", "/proc/cpuinfo")
		if err == nil && strings.Contains(strings.ToLower(string(stdout)), "mipsel") {
			s.endian = "little"
		} else {
			s.endian = "big" // safe default
		}
	}

	return nil
}

// parseUnameMachine extracts ISA from uname -m output.
func parseUnameMachine(output string) string {
	o := strings.TrimSpace(strings.ToLower(output))
	switch {
	case strings.HasPrefix(o, "armv"):
		return "arm"
	case strings.HasPrefix(o, "aarch64"):
		return "aarch64"
	case strings.HasPrefix(o, "mips"):
		return "mips"
	case o == "i386" || o == "i486" || o == "i586" || o == "i686":
		return "x86"
	case o == "x86_64":
		return "x86_64"
	default:
		return o
	}
}

// parseLibcFamily detects libc family from ls output.
func parseLibcFamily(output string) string {
	o := strings.ToLower(output)
	switch {
	case strings.Contains(o, "musl_detected"):
		return "musl"
	case strings.Contains(o, "uclibc"):
		return "uclibc"
	case strings.Contains(o, "musl") || strings.Contains(o, "ld-musl"):
		return "musl"
	case strings.Contains(o, "glibc") || strings.Contains(o, "libc.so"):
		return "glibc"
	default:
		return ""
	}
}

// fileloaderISA returns the correct ISA for fileloader selection,
// accounting for MIPS endianness (mipsel vs mips).
func (s *Scout) fileloaderISA() string {
	if s.isa == "mips" && s.endian == "little" {
		return "mipsel"
	}
	return s.isa
}

func (s *Scout) Push() error {
	return s.transfer(s.pushFast, s.pushPrintf)
}

// transfer runs one of the two transfer methods according to the selected mode
func (s *Scout) transfer(fast, printf func() error) error {
	return runTransfer(s.mode, xfer.IsSameSubnet(s.remote.Host), fast, printf, func(cause error) {
		fmt.Fprintf(os.Stderr, "warning: fast transfer is unavailable: %v\n", cause)
		fmt.Fprintln(os.Stderr, "warning: falling back to printf mode, it is much slower")
	})
}

// fastClient detects the device architecture and opens the connection for the
// fileloader. A failed detection leaves the device untouched.
func (s *Scout) fastClient() (*telnet.TelnetClient, error) {
	if err := s.detectISALight(); err != nil {
		return nil, &xfer.SetupError{Err: err}
	}

	return s.newClient()
}

// pushFast uploads the file with the fileloader over reverse TCP
func (s *Scout) pushFast() error {
	tc, err := s.fastClient()
	if err != nil {
		return err
	}
	defer tc.Close()

	return xfer.Push(tc, s.localFile, s.remote.Path, s.fileloaderISA(), s.libc, s.remote.Host)
}

// Pull downloads a file from the remote device.
func (s *Scout) Pull(localPath string) error {
	return s.transfer(
		func() error { return s.pullFast(localPath) },
		func() error { return s.pullPrintf(localPath) },
	)
}

// pullFast downloads the file with the fileloader over reverse TCP
func (s *Scout) pullFast(localPath string) error {
	tc, err := s.fastClient()
	if err != nil {
		return err
	}
	defer tc.Close()

	return xfer.Pull(tc, s.remote.Path, localPath, s.fileloaderISA(), s.libc, s.remote.Host)
}

// pullPrintf downloads the file through an encoder on the device over telnet
func (s *Scout) pullPrintf(localPath string) error {
	tc, err := s.newClient()
	if err != nil {
		return err
	}
	defer tc.Close()

	return PullViaPrintf(tc, s.remote.Path, localPath)
}

// NewPull creates a Scout configured for downloading a file from a remote device.
func NewPull(target string, mode Mode, verboseFlag bool) (*Scout, error) {
	remote, err := ParseRemoteFileName(target)
	if err != nil {
		return nil, errorx.Decorate(err, "failed to parse remote address")
	}

	if remote.Path == "" {
		return nil, errors.New("pull requires a remote file path")
	}

	return &Scout{
		remote:  remote,
		verbose: verboseFlag,
		mode:    mode,
	}, nil
}
