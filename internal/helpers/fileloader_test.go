package helpers

import (
	"testing"
)

func TestFileloaderForISA_KnownISA(t *testing.T) {
	tests := []struct{ isa, libc string }{
		{"arm", "uclibc"},
		{"arm", "glibc"},
		{"arm", "musl"},
		{"aarch64", "glibc"},
		{"mips", "uclibc"},
		{"x86", "glibc"},
		{"x86_64", "glibc"},
	}

	for _, tt := range tests {
		t.Run(tt.isa+"-"+tt.libc, func(t *testing.T) {
			b, err := FileloaderForISA(tt.isa, tt.libc)
			if err != nil {
				t.Fatalf("FileloaderForISA(%q, %q) error: %v", tt.isa, tt.libc, err)
			}
			if len(b) == 0 {
				t.Fatalf("FileloaderForISA(%q, %q) returned empty", tt.isa, tt.libc)
			}
		})
	}
}

func TestFileloaderForISA_UnknownISA(t *testing.T) {
	_, err := FileloaderForISA("sparc", "glibc")
	if err == nil {
		t.Fatal("expected error for unknown ISA")
	}
}
