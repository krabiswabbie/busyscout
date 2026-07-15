// internal/scout/pull_printf_test.go
package scout

import (
	"encoding/base64"
	"fmt"
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
