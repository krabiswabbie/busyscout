package helpers

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/krabiswabbie/busyscout/internal/telnet"
	"github.com/krabiswabbie/busyscout/internal/testutil"
)

func uploadShell(t *testing.T, options testutil.ShellOptions) *telnet.TelnetClient {
	t.Helper()
	host, port, _ := net.SplitHostPort(testutil.StartShell(t, options))
	tc := &telnet.TelnetClient{Address: host, Port: port, Timeout: time.Second}
	if err := tc.Dial(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tc.Close)
	return tc
}

func TestUploadDataRefusedWrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "readonly")
	if err := os.WriteFile(target, []byte("old"), 0400); err != nil {
		t.Fatal(err)
	}
	var commands atomic.Int32
	tc := uploadShell(t, testutil.ShellOptions{OnCommand: func(string) { commands.Add(1) }})
	err := UploadData(tc, bytes.Repeat([]byte("new"), 100), target)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
		t.Fatalf("want write refusal with shell diagnostic, got %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "old" {
		t.Fatalf("refused destination changed: %q, %v", got, err)
	}
	if got := commands.Load(); got != 1 {
		t.Fatalf("continued after refused first chunk: %d commands", got)
	}
}

func TestUploadDataEmptyTruncates(t *testing.T) {
	target := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(target, []byte("old content"), 0600); err != nil {
		t.Fatal(err)
	}
	tc := uploadShell(t, testutil.ShellOptions{})
	if err := UploadData(tc, nil, target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || len(got) != 0 {
		t.Fatalf("want empty destination, got %q, %v", got, err)
	}
}

func TestUploadDataLiteralDestination(t *testing.T) {
	dir := t.TempDir()
	name := "literal ' $HOME ; [*].bin"
	target := filepath.Join(dir, name)
	tc := uploadShell(t, testutil.ShellOptions{Dir: dir})
	data := bytes.Repeat([]byte{0, 255, 13, 10, 27, 1}, 100)
	if err := UploadData(tc, data, target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("literal destination mismatch: got %d bytes, %v", len(got), err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 || files[0].Name() != name {
		t.Fatalf("unexpected shell side effects: %v, %v", files, err)
	}
}

func TestUploadDataRejectsControlPath(t *testing.T) {
	for _, path := range []string{"a\rb", "a\nb", "a\x00b"} {
		t.Run(strings.ReplaceAll(path, "\x00", "NUL"), func(t *testing.T) {
			// Rejection must precede any attempt to use this undialed client.
			if err := UploadData(&telnet.TelnetClient{}, nil, path); err == nil {
				t.Fatal("want unsupported path error")
			}
		})
	}
}
