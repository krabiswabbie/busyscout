package telnet

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ShellQuote quotes one literal operand for a POSIX shell.
func ShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// ValidateShellPath rejects path bytes that cannot safely pass through the
// line-oriented Telnet shell protocol. Other shell metacharacters are literal
// operands when passed through ShellQuote.
func ValidateShellPath(path string) error {
	if strings.ContainsAny(path, "\r\n\x00") {
		return fmt.Errorf("unsupported shell path: contains CR, LF, or NUL")
	}
	return nil
}

// ExecuteChecked runs a command in a subshell and requires an explicit remote
// exit status. Completion framing uses fresh random text between RS and US;
// the echoed request only contains octal escapes, never the framing bytes.
// The shell's combined output is returned without the completion record.
// Command source must be one physical line without CR, LF, or NUL.
func (tc *TelnetClient) ExecuteChecked(command string) ([]byte, error) {
	if strings.ContainsAny(command, "\r\n\x00") {
		return nil, fmt.Errorf("unsupported shell command: contains CR, LF, or NUL")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("generate shell completion marker: %w", err)
	}
	marker := "__busyscout_" + hex.EncodeToString(nonce[:]) + "__"
	// Quoted eval keeps trailing comments inside the supplied source and avoids
	// physical newlines (interactive shells echo continuation prompts). Status
	// is captured outside the subshell, so `exit` and `set -e` cannot skip it.
	wrapped := "bs_m=" + ShellQuote(marker) + "; printf '\\036%s:B\\037' \"$bs_m\"; ( eval " + ShellQuote(command) + " ); bs_status=$?; printf '\\036%s:%s\\037\\n' \"$bs_m\" \"$bs_status\""
	if err := tc.discardBuffered(); err != nil {
		return nil, fmt.Errorf("prepare checked shell command: %w", err)
	}
	if err := tc.conn.SetReadDeadline(time.Now().Add(tc.Timeout)); err != nil {
		return nil, err
	}
	request := []byte(wrapped + "\r\n")
	tc.log("Send checked command: %s", wrapped)
	if _, err := tc.Write(request); err != nil {
		return nil, fmt.Errorf("write checked shell command: %w", err)
	}
	// Interactive terminals may wrap/redraw echo or disable it entirely. Only
	// the emitted unpredictable BEGIN frame identifies where command output starts.
	begin := []byte("\x1e" + marker + ":B\x1f")
	var pending []byte
	for !bytes.HasSuffix(pending, begin) {
		b, err := tc.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("missing shell completion begin: %w", err)
		}
		pending = append(pending, b)
		if len(pending) > len(begin) {
			pending = pending[1:]
		}
	}

	prefix := []byte("\x1e" + marker + ":")
	var output []byte
	for {
		b, err := tc.ReadByte()
		if err != nil {
			return output, fmt.Errorf("missing shell completion: %w; output: %s", err, output)
		}
		output = append(output, b)
		if b != '\x1f' {
			continue
		}
		start := bytes.LastIndex(output, prefix)
		if start < 0 {
			continue
		}
		statusText := string(output[start+len(prefix) : len(output)-1])
		status, err := strconv.Atoi(statusText)
		if err != nil || status < 0 || status > 255 || strconv.Itoa(status) != statusText {
			return output[:start], fmt.Errorf("malformed shell completion status %q; output: %s", statusText, output[:start])
		}
		output = output[:start]
		b, err = tc.ReadByte()
		if err == nil && b == '\r' {
			b, err = tc.ReadByte()
		}
		if err != nil || b != '\n' {
			return output, fmt.Errorf("malformed shell completion terminator; output: %s", output)
		}
		if _, err := tc.ReadUntilBanner(); err != nil {
			return output, fmt.Errorf("read prompt after shell completion: %w; output: %s", err, output)
		}
		tc.log("Received checked output with size = %d, exit code = %d", len(output), status)
		if status != 0 {
			return output, fmt.Errorf("remote shell exit code %d; output: %s", status, output)
		}
		return output, nil
	}
}
