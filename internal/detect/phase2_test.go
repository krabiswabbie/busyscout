package detect

import (
	"testing"
)

func TestParseHelperOutput(t *testing.T) {
	output := `class=32
endian=little
machine=40
float_abi=hard
cpu_arch=v7
`

	fp := &Fingerprint{}
	parseHelperOutput(output, fp)

	if fp.WordSize != 32 {
		t.Errorf("WordSize = %d, want 32", fp.WordSize)
	}
	if fp.Endianness != "little" {
		t.Errorf("Endianness = %q, want little", fp.Endianness)
	}
	if fp.ISA != "arm" {
		t.Errorf("ISA = %q, want arm", fp.ISA)
	}
	if fp.FloatABI != "hard" {
		t.Errorf("FloatABI = %q, want hard", fp.FloatABI)
	}
	if fp.ARMSubArch != "v7" {
		t.Errorf("ARMSubArch = %q, want v7", fp.ARMSubArch)
	}
}

func TestMapMachineToISA(t *testing.T) {
	tests := map[string]string{
		"40":  "arm",
		"183": "aarch64",
		"8":   "mips",
		"3":   "x86",
		"62":  "x86_64",
		"99":  "",
	}

	for in, want := range tests {
		got := mapMachineToISA(in)
		if got != want {
			t.Errorf("mapMachineToISA(%q) = %q, want %q", in, got, want)
		}
	}
}
