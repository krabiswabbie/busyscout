// internal/scout/pull_printf_test.go
package scout

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"github.com/krabiswabbie/busyscout/internal/telnet"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeBase64Output(t *testing.T) {
	want := []byte("hello from printf pull")
	encoded := base64.StdEncoding.EncodeToString(want)
	output := encoded + "\n#"

	got, err := decodeBase64Output([]byte(output))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestDecodeHexOutput(t *testing.T) {
	want := []byte("hello hex test")
	hexStr := ""
	for _, b := range want {
		hexStr += fmt.Sprintf("%02x", b)
	}
	// xxd -p output: lines of hex, then prompt
	output := hexStr + "\n#"

	got, err := decodeHexOutput([]byte(output))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestPrintfLiteralUploadDownloadRoundtrip(t *testing.T) {
	// Splitting an unquoted operand breaks newly uploaded filenames and allows shell expansion.
	dir, host, _ := printfFixture(t, "")
	target := filepath.Join(dir, "space ' $d; [*] \\ name")
	want := []byte("literal roundtrip\x00\xff\n")
	s := printfScout(t, dir, host, target, want)
	if err := s.Push(); err != nil {
		t.Fatal(err)
	}
	h, p, _ := net.SplitHostPort(host)
	tc := &telnet.TelnetClient{Address: h, Port: p}
	if err := tc.Dial(); err != nil {
		t.Fatal(err)
	}
	defer tc.Close()
	local := filepath.Join(t.TempDir(), "download")
	if err := PullViaPrintf(tc, target, local); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(local)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("roundtrip %q: %v", got, err)
	}
}
func TestPrintfPullRejectsControlPaths(t *testing.T) {
	for _, path := range []string{"bad\nname", "bad\rname", "bad\x00name"} {
		_, err := pullWithEncoder(nil, path, "base64", decodeBase64Output)
		if err == nil || !strings.Contains(err.Error(), "unsupported shell path") {
			t.Fatalf("want unsupported path, got %v", err)
		}
	}
}
