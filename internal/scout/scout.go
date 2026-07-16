package scout

import (
	_ "embed"
	"errors"
	"fmt"
	"github.com/joomcode/errorx"
	"github.com/krabiswabbie/busyscout/internal/helpers"
	"github.com/krabiswabbie/busyscout/internal/telnet"
	"github.com/krabiswabbie/busyscout/internal/xfer"
	"github.com/schollz/progressbar/v3"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	bar       *progressbar.ProgressBar
	isa       string // cached ISA from light detection
	libc      string // cached libc from light detection
	endian    string // "little" or "big" (only for MIPS)
}

func New(source, target string, verboseFlag bool) (*Scout, error) {
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
	}

	// Add the target filename if only target directory is specified
	isDir, errDir := s.checkIsRemoteDirectory(remote.Path)
	if errDir != nil {
		return nil, errorx.Decorate(err, "failed to check remote directory")
	}
	if isDir {
		s.remote.Path = toUnixPath(filepath.Join(s.remote.Path, filepath.Base(source)))
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
	// Detect same subnet
	if xfer.IsSameSubnet(s.remote.Host) {
		if err := s.detectISALight(); err != nil {
			// Fall through to printf mode on detection failure
		} else {
			tc, err := s.newClient()
			if err != nil {
				return err
			}
			defer tc.Close()
			return xfer.Push(tc, s.localFile, s.remote.Path, s.fileloaderISA(), s.libc, s.remote.Host)
		}
	}

	// printf fallback
	type jobDefinition struct {
		fname string
		data  []byte
	}

	data, errRead := os.ReadFile(s.localFile)
	if errRead != nil {
		return errorx.Decorate(errRead, "failed to read local file")
	}

	totalChunks := (len(data) + chunkSize - 1) / chunkSize
	jobCh := make(chan jobDefinition, totalChunks)
	resultCh := make(chan error, totalChunks)

	s.bar = progressbar.NewOptions(len(data),
		progressbar.OptionSetDescription("Uploading"),
		progressbar.OptionShowBytes(true),
		progressbar.OptionShowCount(),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)
	defer s.bar.Finish()

	// Create worker pool
	var wg sync.WaitGroup
	for i := 0; i < threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobCh {
				var (
					progress int
					errSend  error
				)

				for range retries {
					progress, errSend = s.sendChunk(job.data, job.fname)
					if errSend == nil {
						if errCheck := s.checkFileSize(len(job.data), job.fname); errCheck == nil {
							// Chunk uploaded successfully
							break
						}
					}
					s.bar.Add(-1 * progress)
				}
				if errSend != nil {
					resultCh <- errSend
					return
				}
			}
		}()
	}

	// Send chunks to workers
	chunkList := make([]string, totalChunks)
	for i := 0; i < totalChunks; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(data) {
			end = len(data)
		}
		fn := fmt.Sprintf(filepath.Join(tmpDir, "bs.%06d.part"), i)
		chunkList[i] = fn
		jobCh <- jobDefinition{
			fname: fn,
			data:  data[start:end],
		}
	}
	close(jobCh)

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Wait for all workers to finish
	for result := range resultCh {
		if result != nil {
			return result
		}
	}

	// Join chunks, delete temp files, and check target size
	if errJoin := s.joinChunks(chunkList); errJoin != nil {
		return errJoin
	}
	if errDelete := s.deleteChunks(); errDelete != nil {
		return errDelete
	}
	if errCheck := s.checkFileSize(len(data), s.remote.Path); errCheck != nil {
		return errCheck
	}

	return nil
}

func (s *Scout) sendChunk(data []byte, targetFileName string) (progress int, err error) {
	tc, errClient := s.newClient()
	if errClient != nil {
		return 0, errClient
	}
	defer tc.Close()

	if errSend := helpers.UploadData(tc, data, toUnixPath(targetFileName)); errSend != nil {
		return 0, errSend
	}

	progress = len(data)
	s.bar.Add(progress)
	return progress, nil
}

func (s *Scout) joinChunks(list []string) error {
	// Ensure all paths use forward slashes
	for i := range list {
		list[i] = toUnixPath(list[i])
	}

	target := toUnixPath(filepath.Join(tmpDir, "bs.*.part"))
	cmd := fmt.Sprintf("cat %s > %s", target, toUnixPath(s.remote.Path))

	tc, errClient := s.newClient()
	if errClient != nil {
		return errClient
	}
	defer tc.Close()

	_, err := tc.Execute(cmd)
	if err != nil {
		return errorx.Decorate(err, "failed to join file chunks")
	}
	return nil
}

func (s *Scout) deleteChunks() error {
	target := toUnixPath(filepath.Join(tmpDir, "bs.*.part"))
	cmd := "rm " + target

	tc, errClient := s.newClient()
	if errClient != nil {
		return errClient
	}
	defer tc.Close()

	_, err := tc.Execute(cmd)
	if err != nil {
		return errorx.Decorate(err, "failed to join client chunks")
	}
	return nil
}

func (s *Scout) checkFileSize(sz int, fname string) error {
	cmd := fmt.Sprintf("ls -l %s", toUnixPath(fname))

	tc, errClient := s.newClient()
	if errClient != nil {
		return errClient
	}
	defer tc.Close()

	stdout, err := tc.Execute(cmd)
	if err != nil {
		return errorx.Decorate(err, "failed to send command")
	}

	// stdout should return the following string
	// -rw-r--r--    1 root     root         14472 May  4 06:08 filename

	// Split output by whitespace and try to parse each field as integer
	fields := strings.Fields(string(stdout))
	for _, field := range fields {
		if size, err := strconv.Atoi(field); err == nil && size == sz {
			return nil
		}
	}

	return errors.New("unable to parse target file size from stdout")
}

func (s *Scout) checkIsRemoteDirectory(path string) (bool, error) {
	cmd := fmt.Sprintf("ls -ld %s", toUnixPath(path))

	tc, errClient := s.newClient()
	if errClient != nil {
		return false, errClient
	}
	defer tc.Close()

	stdout, err := tc.Execute(cmd)
	if err != nil {
		return false, errorx.Decorate(err, "failed to send command")
	}

	// stdout should return the following string
	// drwxrwxrwx    9 root     root           460 May  4 08:44 /tmp

	stdoutStr := string(stdout)
	if strings.Contains(stdoutStr, "No such file or directory") {
		return false, nil
	}

	// Split stdout by whitespace
	fields := strings.Fields(string(stdout))

	if len(fields) >= 2 {
		permissionsStr := fields[0] // Assuming permissions is the first
		if permissionsStr[0] == 'd' {
			// It is directory
			return true, nil
		}
	}

	return false, errors.New("unable to parse target file size from stdout")
}

// toUnixPath converts a path to use forward slashes, regardless of platform
func toUnixPath(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}

// Pull downloads a file from the remote device.
func (s *Scout) Pull(localPath string) error {
	// Detect same subnet
	if xfer.IsSameSubnet(s.remote.Host) {
		if err := s.detectISALight(); err != nil {
			// Fall through to printf mode
		} else {
			tc, err := s.newClient()
			if err != nil {
				return err
			}
			defer tc.Close()
			return xfer.Pull(tc, s.remote.Path, localPath, s.fileloaderISA(), s.libc, s.remote.Host)
		}
	}

	// printf fallback
	tc, err := s.newClient()
	if err != nil {
		return err
	}
	defer tc.Close()
	return PullViaPrintf(tc, s.remote.Path, localPath)
}

// NewPull creates a Scout configured for downloading a file from a remote device.
func NewPull(target string, verboseFlag bool) (*Scout, error) {
	remote, err := ParseRemoteFileName(target)
	if err != nil {
		return nil, errorx.Decorate(err, "failed to parse remote address")
	}

	return &Scout{
		remote:  remote,
		verbose: verboseFlag,
	}, nil
}
