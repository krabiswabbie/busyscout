package xfer

import (
	"errors"
	"testing"
)

func isSetupError(err error) bool {
	var setupErr *SetupError
	return errors.As(err, &setupErr)
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
