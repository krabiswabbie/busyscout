package scout

import (
	_ "embed"
	"errors"
	"fmt"
	"github.com/joomcode/errorx"
	"github.com/krabiswabbie/busyscout/internal/telnet"
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
		Login:    s.remote.Username,
		Password: s.remote.Password,
		Verbose:  s.verbose,
	}

	if errDial := tc.Dial(); errDial != nil {
		return nil, errorx.Decorate(errDial, "failed to open telnet connection")
	}

	return tc, nil
}

func (s *Scout) Push() error {
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

	// Ensure target filename uses forward slashes
	targetFileName = toUnixPath(targetFileName)
	redirectMode := ">"

	// Iterate over the full chunk in 128 byte steps
	for i := 0; i < len(data); i += lineSize {
		end := i + lineSize
		if end > len(data) {
			end = len(data)
		}

		// Construct the command for the current sub-chunk
		cmd := "printf '"
		for _, bt := range data[i:end] {
			cmd += fmt.Sprintf("\\x%02x", bt)
		}
		cmd += fmt.Sprintf("' %s %s\n", redirectMode, targetFileName) // Append to the file
		redirectMode = ">>"

		// Send the command
		_, errExecute := tc.Execute(cmd)
		if errExecute != nil {
			return progress, errExecute
		}

		progress += end - i
		s.bar.Add(end - i)
	}

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
