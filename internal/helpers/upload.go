package helpers

import (
	"fmt"

	"github.com/krabiswabbie/busyscout/internal/telnet"
)

// UploadData sends binary data to a remote file via printf over an already-open telnet connection.
// The caller owns the connection lifecycle (Dial/Close).
func UploadData(tc *telnet.TelnetClient, data []byte, targetFileName string) error {
	if err := telnet.ValidateShellPath(targetFileName); err != nil {
		return err
	}
	target := telnet.ShellQuote(targetFileName)
	if len(data) == 0 {
		_, err := tc.ExecuteChecked(": > " + target)
		return err
	}
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
		cmd += fmt.Sprintf("' %s %s", redirectMode, target)
		redirectMode = ">>"

		if _, err := tc.ExecuteChecked(cmd); err != nil {
			return err
		}
	}

	return nil
}

const (
	lineSize = 128
)
