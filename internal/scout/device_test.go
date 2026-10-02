package scout

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/krabiswabbie/busyscout/internal/telnet"
)

// Opt-in device test. BUSYSCOUT_TEST_TARGET contains user:password@host[:port].
// Only the uniquely named test file is removed from the device.
func TestDeviceRepeatedUploadRoundTrip(t *testing.T) {
	target := os.Getenv("BUSYSCOUT_TEST_TARGET")
	if target == "" {
		t.Skip("set BUSYSCOUT_TEST_TARGET to test a real telnet device")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, fmt.Sprintf("busyscout-roundtrip-%d.bin", time.Now().UnixNano()))
	remotePath := "/tmp/" + filepath.Base(source)
	cleanup, err := NewPull(target+":"+remotePath, ModePrintf, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		tc, err := cleanup.newClient()
		if err != nil {
			t.Errorf("test-file cleanup: %v", err)
			return
		}
		defer tc.Close()
		if _, err := tc.ExecuteChecked("rm -f " + telnet.ShellQuote(remotePath)); err != nil {
			t.Errorf("test-file cleanup: %v", err)
		}
	})
	for _, mode := range []Mode{ModePrintf, ModeAuto} {
		t.Run(string(mode), func(t *testing.T) {
			for attempt := 0; attempt < 2; attempt++ {
				// Different sizes catch append behavior and stale tails on overwrite.
				payload := bytes.Repeat([]byte{0, 255, 13, 10, 27, byte(attempt + 1)}, 683-attempt*100)
				if err := os.WriteFile(source, payload, 0600); err != nil {
					t.Fatal(err)
				}
				push, err := New(source, target, mode, false)
				if err != nil {
					t.Fatalf("upload %d destination check: %v", attempt+1, err)
				}
				if err := push.Push(); err != nil {
					t.Fatalf("upload %d: %v", attempt+1, err)
				}
				pull, err := NewPull(target+":"+remotePath, ModePrintf, false)
				if err != nil {
					t.Fatal(err)
				}
				downloaded := filepath.Join(dir, "downloaded.bin")
				if err := pull.Pull(downloaded); err != nil {
					t.Fatal(err)
				}
				actual, err := os.ReadFile(downloaded)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(actual, payload) {
					t.Fatalf("upload %d roundtrip mismatch: got %d bytes, want %d", attempt+1, len(actual), len(payload))
				}
			}
		})
	}
}
