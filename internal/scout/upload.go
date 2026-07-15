package scout

import (
	"fmt"

	"github.com/krabiswabbie/busyscout/internal/telnet"
)

// UploadData sends binary data to a remote file via printf over an already-open telnet connection.
// The caller owns the connection lifecycle (Dial/Close).
func UploadData(tc *telnet.TelnetClient, data []byte, targetFileName string) error {
	targetFileName = toUnixPath(targetFileName)
	redirectMode := ">"

	for i := 0; i < len(data); i += lineSize {
		end := i + lineSize
		if end > len(data) {
			end = len(data)
		}

		cmd := "printf '"
		for _, bt := range data[i:end] {
			cmd += fmt.Sprintf("\\x%02x", bt)
		}
		cmd += fmt.Sprintf("' %s %s\n", redirectMode, targetFileName)
		redirectMode = ">>"

		if _, err := tc.Execute(cmd); err != nil {
			return err
		}
	}

	return nil
}
