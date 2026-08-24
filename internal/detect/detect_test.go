package detect

import (
	"strings"
	"testing"
)

func TestDeriveTimeT(t *testing.T) {
	tests := []struct {
		name   string
		fp     Fingerprint
		expect string
	}{
		{
			name:   "musl 1.2.3 on 32-bit is time64",
			fp:     Fingerprint{WordSize: 32, Libc: "musl", MuslVersion: "1.2.3"},
			expect: "64-bit",
		},
		{
			name:   "musl 1.2.0 is the first time64 release",
			fp:     Fingerprint{WordSize: 32, Libc: "musl", MuslVersion: "1.2.0"},
			expect: "64-bit",
		},
		{
			name:   "musl 1.1.24 predates time64",
			fp:     Fingerprint{WordSize: 32, Libc: "musl", MuslVersion: "1.1.24"},
			expect: "32-bit",
		},
		{
			name:   "musl with unknown version stays unset",
			fp:     Fingerprint{WordSize: 32, Libc: "musl"},
			expect: "",
		},
		{
			name:   "32-bit glibc stays unset (may opt into _TIME_BITS=64)",
			fp:     Fingerprint{WordSize: 32, Libc: "glibc"},
			expect: "",
		},
		{
			name:   "64-bit target is always time64",
			fp:     Fingerprint{WordSize: 64, Libc: "musl"},
			expect: "64-bit",
		},
		{
			name:   "unknown word size stays unset",
			fp:     Fingerprint{Libc: "musl", MuslVersion: "1.2.3"},
			expect: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := tt.fp
			fp.deriveTimeT()
			if fp.TimeT != tt.expect {
				t.Errorf("TimeT = %q, want %q", fp.TimeT, tt.expect)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b   string
		expect int
	}{
		{"1.2.3", "1.2.0", 1},
		{"1.2.0", "1.2.0", 0},
		{"1.1.24", "1.2.0", -1},
		{"1.2", "1.2.0", 0},
		{"1.10.0", "1.9.0", 1},
		{"2.0", "1.2.0", 1},
		{"", "1.2.0", -1},
	}

	for _, tt := range tests {
		t.Run(tt.a+" vs "+tt.b, func(t *testing.T) {
			if got := compareVersions(tt.a, tt.b); got != tt.expect {
				t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.expect)
			}
		})
	}
}

func TestFormatMuslTime64(t *testing.T) {
	fp := &Fingerprint{
		ISA:         "arm",
		WordSize:    32,
		Endianness:  "little",
		ARMSubArch:  "v7",
		FloatABI:    "hard",
		Libc:        "musl",
		LibcVersion: "musl 1.2.3",
		MuslVersion: "1.2.3",
	}
	fp.deriveToolchainHint()
	fp.deriveTimeT()
	output := fp.Format()

	if !strings.Contains(output, "libc:             musl 1.2.3") {
		t.Errorf("expected musl version on the libc line, got:\n%s", output)
	}
	if !strings.Contains(output, "time_t:           64-bit (musl 1.2.3 >= 1.2.0)") {
		t.Errorf("expected time64 line, got:\n%s", output)
	}
	if !strings.Contains(output, "armv7-linux-musleabihf") {
		t.Errorf("expected musl triplet, got:\n%s", output)
	}
	if !strings.Contains(output, "needs a time64 toolchain") {
		t.Errorf("expected toolchain time64 warning, got:\n%s", output)
	}
}

func TestFormatMuslPreTime64(t *testing.T) {
	fp := &Fingerprint{
		ISA:         "arm",
		WordSize:    32,
		Endianness:  "little",
		Libc:        "musl",
		LibcVersion: "musl 1.1.24",
		MuslVersion: "1.1.24",
	}
	fp.deriveToolchainHint()
	fp.deriveTimeT()
	output := fp.Format()

	if !strings.Contains(output, "time_t:           32-bit (musl 1.1.24 < 1.2.0)") {
		t.Errorf("expected 32-bit time_t line, got:\n%s", output)
	}
	if !strings.Contains(output, "needs a pre-time64 toolchain") {
		t.Errorf("expected pre-time64 toolchain warning, got:\n%s", output)
	}
}

func TestFormatMuslUnknownVersion(t *testing.T) {
	fp := &Fingerprint{
		ISA:         "arm",
		WordSize:    32,
		Endianness:  "little",
		Libc:        "musl",
		LibcVersion: "musl",
	}
	fp.deriveToolchainHint()
	fp.deriveTimeT()
	output := fp.Format()

	if !strings.Contains(output, "time_t:           [uncertain") {
		t.Errorf("expected uncertain time_t line, got:\n%s", output)
	}
	if !strings.Contains(output, "musl version unknown") {
		t.Errorf("expected unknown-version toolchain warning, got:\n%s", output)
	}
}

func TestFormatNonMuslHasNoTimeTNoise(t *testing.T) {
	fp := &Fingerprint{
		ISA:         "arm",
		WordSize:    32,
		Endianness:  "little",
		Libc:        "uClibc 0.9.33",
		LibcVersion: "uClibc 0.9.33.2",
	}
	fp.deriveToolchainHint()
	fp.deriveTimeT()
	output := fp.Format()

	if strings.Contains(output, "time_t:") {
		t.Errorf("expected no time_t line for uClibc, got:\n%s", output)
	}
	if strings.Contains(output, "time64") {
		t.Errorf("expected no time64 note for uClibc, got:\n%s", output)
	}
}
