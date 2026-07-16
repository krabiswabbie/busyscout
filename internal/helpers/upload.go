package helpers

import (
	"fmt"
	"strings"

	"github.com/krabiswabbie/busyscout/internal/telnet"
)

// UploadData sends binary data to a remote file via printf over an already-open telnet connection.
// The caller owns the connection lifecycle (Dial/Close).
func UploadData(tc *telnet.TelnetClient, data []byte, targetFileName string, lineSize int) error {
	if lineSize <= 0 {
		return fmt.Errorf("lineSize must be positive, got %d", lineSize)
	}
	targetFileName = toUnixPath(targetFileName)
	redirectMode := ">"

	for i := 0; i < len(data); i += lineSize {
		end := i + lineSize
		if end > len(data) {
			end = len(data)
		}

		cmd := "printf '"
		for _, bt := range data[i:end] {
			cmd += fmt.Sprintf("\\%03o", bt)
		}
		cmd += fmt.Sprintf("' %s %s\n", redirectMode, targetFileName)
		redirectMode = ">>"

		if _, err := tc.Execute(cmd); err != nil {
			return err
		}
	}

	return nil
}

// DefaultLineSize is the safe printf chunk size for unknown devices.
const DefaultLineSize = 128

// toUnixPath converts a path to use forward slashes, regardless of platform
func toUnixPath(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}
