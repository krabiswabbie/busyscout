package detect

import (
	"testing"
)

func TestParseUname(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		wantISA string
		wantWS  int
	}{
		{"armv7l", "Linux (none) 3.0.8 #7 armv7l GNU/Linux", "arm", 32},
		{"aarch64", "Linux localhost 4.9.0 aarch64", "aarch64", 64},
		{"mips", "Linux (none) 3.10.14 mips", "mips", 32},
		{"x86_64", "Linux server 5.10.0 x86_64", "x86_64", 64},
		{"i686", "Linux (none) 2.6.32 i686", "x86", 32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := &Fingerprint{}
			parseUname(tt.output, fp)
			if fp.ISA != tt.wantISA {
				t.Errorf("ISA = %q, want %q", fp.ISA, tt.wantISA)
			}
			if fp.WordSize != tt.wantWS {
				t.Errorf("WordSize = %d, want %d", fp.WordSize, tt.wantWS)
			}
		})
	}
}

func TestParseCPUInfo_ARM(t *testing.T) {
	output := `Processor       : ARMv7 Processor rev 1 (v7l)
CPU architecture: 7
Features        : swp half thumb fastmult vfp edsp neon vfpv3 tls
CPU implementer : 0x41
CPU variant     : 0x2
CPU part        : 0xc09
CPU revision    : 1
Hardware        : Hisilicon HI35xx`

	fp := &Fingerprint{}
	parseCPUInfo(output, fp)

	if fp.ARMSubArch != "v7" {
		t.Errorf("ARMSubArch = %q, want v7", fp.ARMSubArch)
	}
}

func TestParseCPUInfo_MIPS(t *testing.T) {
	output := `system type             : MIPS Malta
cpu model               : MIPS 24Kc V5.5
byteorder               : little endian`

	fp := &Fingerprint{}
	parseCPUInfo(output, fp)

	if fp.ISA != "mips" {
		t.Errorf("ISA = %q, want mips", fp.ISA)
	}
	if fp.WordSize != 32 {
		t.Errorf("WordSize = %d, want 32", fp.WordSize)
	}
	if fp.Endianness != "little" {
		t.Errorf("Endianness = %q, want little", fp.Endianness)
	}
}

func TestParseLibc(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		wantLibc string
	}{
		{
			"glibc",
			`lrwxrwxrwx 1 root root 12 Jan  1  2000 /lib/libc.so.6 -> libc-2.9.so
lrwxrwxrwx 1 root root 14 Jan  1  2000 /lib/ld-linux.so.3 -> ld-2.9.so`,
			"glibc",
		},
		{
			"uClibc",
			`lrwxrwxrwx 1 root root 17 Jan  1  2000 /lib/libc.so.0 -> libuClibc-0.9.33.so
lrwxrwxrwx 1 root root 17 Jan  1  2000 /lib/ld-uClibc.so.0 -> ld-uClibc-0.9.33.so`,
			"uClibc 0.9.33",
		},
		{
			"musl",
			`lrwxrwxrwx 1 root root 12 Jan  1  2000 /lib/libc.musl-x86_64.so.1 -> libc.so
lrwxrwxrwx 1 root root 12 Jan  1  2000 /lib/ld-musl-x86_64.so.1 -> /lib/libc.so`,
			"musl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := &Fingerprint{}
			parseLibc(tt.output, fp)
			if fp.Libc != tt.wantLibc {
				t.Errorf("Libc = %q, want %q", fp.Libc, tt.wantLibc)
			}
		})
	}
}

func TestParseFileOutput(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		wantISA string
		wantWS  int
		wantEnd string
	}{
		{
			"ARM 32-bit LE",
			"/bin/busybox: ELF 32-bit LSB executable, ARM, version 1 (SYSV), dynamically linked",
			"arm", 32, "little",
		},
		{
			"AArch64",
			"/bin/busybox: ELF 64-bit LSB executable, ARM aarch64, version 1 (SYSV)",
			"aarch64", 64, "little",
		},
		{
			"MIPS BE",
			"/bin/busybox: ELF 32-bit MSB executable, MIPS, version 1 (SYSV)",
			"mips", 32, "big",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := &Fingerprint{}
			parseFileOutput(tt.output, fp)
			if fp.ISA != tt.wantISA {
				t.Errorf("ISA = %q, want %q", fp.ISA, tt.wantISA)
			}
			if fp.WordSize != tt.wantWS {
				t.Errorf("WordSize = %d, want %d", fp.WordSize, tt.wantWS)
			}
			if fp.Endianness != tt.wantEnd {
				t.Errorf("Endianness = %q, want %q", fp.Endianness, tt.wantEnd)
			}
		})
	}
}

func TestParseODOutput(t *testing.T) {
	fp := &Fingerprint{Endianness: "little"}
	parseODOutput("7f 45 4c 46 01 01 01 00 00 00 00 00 00 00 00 00 02 00 28 00", fp)

	if fp.WordSize != 32 {
		t.Errorf("WordSize = %d, want 32", fp.WordSize)
	}
	if fp.ISA != "arm" {
		t.Errorf("ISA = %q, want arm", fp.ISA)
	}
}

func TestParseReadelfOutput(t *testing.T) {
	output := `Tag_CPU_arch: v7
Tag_CPU_arch_profile: Application
Tag_ARM_ISA_use: Yes
Tag_THUMB_ISA_use: Thumb-2
Tag_FP_arch: VFPv3
Tag_ABI_HardFP_use: SP and DP
Tag_ABI_VFP_args: VFP registers`

	fp := &Fingerprint{}
	parseReadelfOutput(output, fp)

	if fp.ARMSubArch != "v7" {
		t.Errorf("ARMSubArch = %q, want v7", fp.ARMSubArch)
	}
	if fp.FloatABI != "hard" {
		t.Errorf("FloatABI = %q, want hard", fp.FloatABI)
	}
}

func TestNeedsPhase2(t *testing.T) {
	// ARM without float ABI
	fp := &Fingerprint{ISA: "arm", WordSize: 32}
	if !fp.needsPhase2() {
		t.Error("expected needsPhase2 true for ARM without float ABI")
	}

	// ARM with float ABI
	fp2 := &Fingerprint{ISA: "arm", FloatABI: "hard", WordSize: 32}
	if fp2.needsPhase2() {
		t.Error("expected needsPhase2 false for ARM with float ABI")
	}

	// x86
	fp3 := &Fingerprint{ISA: "x86_64", WordSize: 64}
	if fp3.needsPhase2() {
		t.Error("expected needsPhase2 false for x86_64")
	}
}
