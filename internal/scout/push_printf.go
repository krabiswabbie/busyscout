package scout

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/krabiswabbie/busyscout/internal/helpers"
	"github.com/krabiswabbie/busyscout/internal/telnet"
	"github.com/schollz/progressbar/v3"
)

// Small fixed batches keep checked commands within old interactive shell line
// limits, including ExecuteChecked's extra quoting and completion wrapper.
const joinBatchSize = 4

// pushPrintf writes directly to the requested destination after uploading and
// verifying fragments in a directory exclusively owned by this transfer.
func (s *Scout) pushPrintf() (err error) {
	if err := telnet.ValidateShellPath(s.remote.Path); err != nil {
		return err
	}
	data, err := os.ReadFile(s.localFile)
	if err != nil {
		return fmt.Errorf("read local file: %w", err)
	}
	tc, err := s.newClient()
	if err != nil {
		return err
	}
	defer tc.Close()
	scratch, err := createChunkDirectory(tc)
	if err != nil {
		return err
	}
	// Workers are joined before reaching any return below. Cleanup must preserve
	// both a transfer failure and its own failure, rather than hiding either one.
	defer func() { err = errors.Join(err, s.deleteChunks(tc, scratch)) }()

	s.bar = progressbar.NewOptions(len(data),
		progressbar.OptionSetDescription("Uploading"), progressbar.OptionShowBytes(true), progressbar.OptionShowCount(),
		progressbar.OptionSetTheme(progressbar.Theme{Saucer: "=", SaucerHead: ">", SaucerPadding: " ", BarStart: "[", BarEnd: "]"}))
	defer s.bar.Finish()
	type chunkJob struct {
		name string
		data []byte
	}
	totalChunks := (len(data) + chunkSize - 1) / chunkSize
	jobs := make(chan chunkJob, totalChunks)
	list := make([]string, totalChunks)
	for i := range list {
		start, end := i*chunkSize, (i+1)*chunkSize
		if end > len(data) {
			end = len(data)
		}
		list[i] = fmt.Sprintf("%s/%06d.part", scratch, i)
		jobs <- chunkJob{list[i], data[start:end]}
	}
	close(jobs)

	stopped := make(chan struct{})
	var stopOnce sync.Once
	results := make(chan error, threads)
	var workers sync.WaitGroup
	workerCount := threads
	if totalChunks < workerCount {
		workerCount = totalChunks
	}
	for i := 0; i < workerCount; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				select {
				case <-stopped:
					return
				default:
				}
				var chunkErr error
				for attempt := 0; attempt < retries; attempt++ {
					select {
					case <-stopped:
						return
					default:
					}
					_, chunkErr = s.sendChunk(job.data, job.name)
					if chunkErr == nil {
						chunkErr = s.checkFileSize(len(job.data), job.name)
					}
					if chunkErr == nil {
						s.bar.Add(len(job.data))
						break
					}
				}
				if chunkErr != nil {
					results <- fmt.Errorf("upload fragment %q after %d attempts: %w", job.name, retries, chunkErr)
					stopOnce.Do(func() { close(stopped) })
					return
				}
			}
		}()
	}
	workers.Wait()
	close(results)
	for workerErr := range results {
		err = errors.Join(err, workerErr)
	}
	if err != nil {
		return err
	}
	if err = s.joinChunks(tc, list); err != nil {
		return err
	}
	return checkFileSize(tc, len(data), s.remote.Path)
}

func createChunkDirectory(tc *telnet.TelnetClient) (string, error) {
	var lastErr error
	for attempt := 0; attempt < retries; attempt++ {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return "", fmt.Errorf("generate fragment directory: %w", err)
		}
		dir := tmpDir + "/bs-upload-" + hex.EncodeToString(nonce[:])
		if _, err := tc.ExecuteChecked("mkdir -m 700 " + telnet.ShellQuote(dir)); err == nil {
			return dir, nil
		} else {
			lastErr = err
		}
	}
	return "", fmt.Errorf("create private fragment directory: %w", lastErr)
}

func (s *Scout) sendChunk(data []byte, targetFileName string) (int, error) {
	tc, err := s.newClient()
	if err != nil {
		return 0, err
	}
	defer tc.Close()
	if err := helpers.UploadData(tc, data, targetFileName); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (s *Scout) joinChunks(tc *telnet.TelnetClient, list []string) error {
	if err := telnet.ValidateShellPath(s.remote.Path); err != nil {
		return err
	}
	target := telnet.ShellQuote(s.remote.Path)
	// Explicit truncation handles empty files without cat reading stdin, and
	// checks the destination write even when old bytes have the expected size.
	if _, err := tc.ExecuteChecked(": > " + target); err != nil {
		return fmt.Errorf("truncate remote destination: %w", err)
	}
	for start := 0; start < len(list); start += joinBatchSize {
		end := start + joinBatchSize
		if end > len(list) {
			end = len(list)
		}
		var command strings.Builder
		command.WriteString("cat")
		for _, name := range list[start:end] {
			if err := telnet.ValidateShellPath(name); err != nil {
				return err
			}
			command.WriteByte(' ')
			command.WriteString(telnet.ShellQuote(name))
		}
		command.WriteString(" >> ")
		command.WriteString(target)
		if _, err := tc.ExecuteChecked(command.String()); err != nil {
			return fmt.Errorf("join file chunks: %w", err)
		}
	}
	return nil
}

func (s *Scout) deleteChunks(tc *telnet.TelnetClient, scratch string) error {
	if _, err := tc.ExecuteChecked("rm -r " + telnet.ShellQuote(scratch)); err != nil {
		return fmt.Errorf("remove fragment directory %q: %w", scratch, err)
	}
	return nil
}

func (s *Scout) checkFileSize(expected int, name string) error {
	if err := telnet.ValidateShellPath(name); err != nil {
		return err
	}
	tc, err := s.newClient()
	if err != nil {
		return err
	}
	defer tc.Close()
	return checkFileSize(tc, expected, name)
}

func checkFileSize(tc *telnet.TelnetClient, expected int, name string) error {
	if err := telnet.ValidateShellPath(name); err != nil {
		return err
	}
	output, err := tc.ExecuteChecked("wc -c < " + telnet.ShellQuote(name))
	if err != nil {
		return fmt.Errorf("check remote file size %q: %w", name, err)
	}
	text := strings.TrimSpace(string(output))
	if text == "" {
		return fmt.Errorf("invalid remote file size for %q: %q", name, output)
	}
	for _, digit := range text {
		if digit < '0' || digit > '9' {
			return fmt.Errorf("invalid remote file size for %q: %q", name, output)
		}
	}
	actual, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid remote file size for %q: %q: %w", name, output, err)
	}
	if actual != uint64(expected) {
		return fmt.Errorf("remote file size mismatch for %q: expected %d, actual %d", name, expected, actual)
	}
	return nil
}
