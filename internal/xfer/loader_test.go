package xfer

import (
	"errors"
	"strings"
	"testing"
)

func isSetupError(err error) bool {
	var setupErr *SetupError
	return errors.As(err, &setupErr)
}

func TestLoaderStartError(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		wantErr bool
		// wantMsg is what the shell said, it has to reach the user
		wantMsg string
		// notWant must stay out of the error
		notWant string
	}{
		{
			name:   "loader started",
			output: "bs-rc=0",
		},
		{
			name:    "no matching dynamic loader on the device",
			output:  "-sh: /tmp/bs-loader: not found\r\nbs-rc=127",
			wantErr: true,
			wantMsg: "/tmp/bs-loader: not found",
		},
		{
			name:    "noexec mount",
			output:  "/bin/sh: /tmp/bs-loader: Permission denied\r\nbs-rc=126",
			wantErr: true,
			wantMsg: "Permission denied",
		},
		{
			name:    "tail of the command echo precedes the output",
			output:  "41234 /tmp/file; echo bs-rc=$?\r\n-sh: /tmp/bs-loader: not found\r\nbs-rc=127",
			wantErr: true,
			wantMsg: "/tmp/bs-loader: not found",
		},
		{
			name:    "failure without a message",
			output:  "bs-rc=1",
			wantErr: true,
		},
		{
			name:    "command echo is not taken for the shell message",
			output:  "41234 /tmp/file; echo bs-rc=$?\r\nbs-rc=1",
			wantErr: true,
			notWant: "41234",
		},
		{
			// A shell that does not report the status: behave as before
			name:   "no status in the output",
			output: "",
		},
		{
			name:   "only the echoed command",
			output: "41234 /tmp/file; echo bs-rc=$?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := loaderStartError([]byte(tt.output))
			if (err != nil) != tt.wantErr {
				t.Fatalf("got error %v, want error: %v", err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error %q does not carry the shell message %q", err, tt.wantMsg)
			}
			if err != nil && tt.notWant != "" && strings.Contains(err.Error(), tt.notWant) {
				t.Errorf("error %q must not contain %q", err, tt.notWant)
			}
		})
	}
}

// No fileloader for the device: nothing has been transferred, so the caller
// may retry in another mode.
func TestPush_UnsupportedISAIsSetupError(t *testing.T) {
	err := Push(nil, "local.bin", "/tmp/remote.bin", "sparc", "glibc", "192.168.1.10")
	if !isSetupError(err) {
		t.Fatalf("got %v, want a SetupError", err)
	}
}

func TestPull_UnsupportedISAIsSetupError(t *testing.T) {
	err := Pull(nil, "/tmp/remote.bin", "local.bin", "sparc", "glibc", "192.168.1.10")
	if !isSetupError(err) {
		t.Fatalf("got %v, want a SetupError", err)
	}
}

// The fileloader never connected back
func TestAcceptAndPush_AcceptFailureIsSetupError(t *testing.T) {
	_, ln, err := StartListener()
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()

	if err := AcceptAndPush(ln, "local.bin"); !isSetupError(err) {
		t.Fatalf("got %v, want a SetupError", err)
	}
}

func TestAcceptAndPull_AcceptFailureIsSetupError(t *testing.T) {
	_, ln, err := StartListener()
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()

	if err := AcceptAndPull(ln, "local.bin"); !isSetupError(err) {
		t.Fatalf("got %v, want a SetupError", err)
	}
}
