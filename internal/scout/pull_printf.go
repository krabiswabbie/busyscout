// internal/scout/pull_printf.go
package scout

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/joomcode/errorx"
)

// PullViaPrintf downloads a remote file via printf channel (slow, for NAT scenarios).
// Tries base64 first, then xxd -p, then od -An -t x1.
func PullViaPrintf(tc telnetExecutor, remotePath, localPath string) error {
	var data []byte
	var err error

	// Try base64
	data, err = pullWithEncoder(tc, remotePath, "base64", decodeBase64Output)
	if err == nil {
		return os.WriteFile(localPath, data, 0644)
	}

	// Try xxd -p
	data, err = pullWithEncoder(tc, remotePath, "xxd -p", decodeHexOutput)
	if err == nil {
		return os.WriteFile(localPath, data, 0644)
	}

	// Try od -An -t x1
	data, err = pullWithEncoder(tc, remotePath, "od -An -t x1", decodeHexOutput)
	if err == nil {
		return os.WriteFile(localPath, data, 0644)
	}

	return errorx.Decorate(err, "no suitable encoder found on device (tried base64, xxd, od)")
}

// telnetExecutor is the minimal telnet client interface.
type telnetExecutor interface {
	Execute(name string, args ...string) ([]byte, error)
}

func pullWithEncoder(tc telnetExecutor, remotePath, encoder string, decoder func([]byte) ([]byte, error)) ([]byte, error) {
	cmd := fmt.Sprintf("%s %s", encoder, remotePath)
	// Split encoder into command and args
	parts := strings.Fields(cmd)
	name := parts[0]
	args := parts[1:]

	stdout, err := tc.Execute(name, args...)
	if err != nil {
		return nil, errorx.Decorate(err, "encoder command failed")
	}

	return decoder(stdout)
}

func decodeBase64Output(output []byte) ([]byte, error) {
	// Strip trailing prompt (last line containing # or $)
	s := stripPrompt(string(output))
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return base64.StdEncoding.DecodeString(s)
}

func decodeHexOutput(output []byte) ([]byte, error) {
	s := stripPrompt(string(output))
	// Remove all whitespace
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, " ", "")
	return hex.DecodeString(s)
}

func stripPrompt(s string) string {
	// Remove trailing prompt line (# or $ at end of last line)
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "#" || trimmed == "$" {
			lines = lines[:i]
			break
		}
		// Also handle "something #" or "something $"
		if len(trimmed) > 0 && (trimmed[len(trimmed)-1] == '#' || trimmed[len(trimmed)-1] == '$') {
			lines = lines[:i]
			break
		}
	}
	return strings.Join(lines, "")
}
