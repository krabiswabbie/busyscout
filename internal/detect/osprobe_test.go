package detect

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseBusyBoxVersion(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "typical busybox",
			input:  "BusyBox v1.31.1 (2020-01-15 10:00:00 CST) multi-call binary.\n\nUsage: busybox ...",
			expect: "v1.31.1",
		},
		{
			name:   "single line",
			input:  "BusyBox v1.26.2 () multi-call binary.",
			expect: "v1.26.2",
		},
		{
			name:   "no match",
			input:  "some other output",
			expect: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseBusyBoxVersion(tt.input)
			if got != tt.expect {
				t.Errorf("got %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestParseGlibcVersion(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "glibc 2.28",
			input:  "GNU C Library (GNU libc) stable release version 2.28.\nCopyright (C) 2018 Free Software Foundation, Inc.",
			expect: "2.28.",
		},
		{
			name:   "no version",
			input:  "no version here",
			expect: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGlibcVersion(tt.input)
			if got != tt.expect {
				t.Errorf("got %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestParseMeminfoTotal(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "128 MB",
			input:  "MemTotal:         128456 kB\nMemFree:           50000 kB\n",
			expect: "125 MB",
		},
		{
			name:   "64 MB",
			input:  "MemTotal:          61024 kB\n",
			expect: "60 MB",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseMeminfoTotal(tt.input)
			if got != tt.expect {
				t.Errorf("got %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestParseDFRoot(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name: "df /",
			input: `Filesystem           1K-blocks      Used Available Use% Mounted on
/dev/root                128000     12000    116000   9% /
`,
			expect: "11M / 113M (9%)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDFRoot(tt.input)
			if got != tt.expect {
				t.Errorf("got %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestParseMountsFiltered(t *testing.T) {
	input := `rootfs / rootfs rw 0 0
/dev/root / squashfs ro,relatime 0 0
tmpfs /tmp tmpfs rw,nosuid,nodev 0 0
none /proc proc rw,relatime 0 0
/dev/mtdblock3 /mnt jffs2 rw,relatime 0 0
`
	got := parseMountsFiltered(input)
	expect := []string{
		"/ : rootfs,rw",
		"/ : squashfs,ro,relatime",
		"/tmp : tmpfs,rw,nosuid,nodev",
		"/mnt : jffs2,rw,relatime",
	}
	if !reflect.DeepEqual(got, expect) {
		t.Errorf("got %v, want %v", got, expect)
	}
}

func TestParseUptimeSeconds(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "3 days",
			input:  "300000.00 150000.00",
			expect: "3d 11:20",
		},
		{
			name:   "under 1 day",
			input:  "4500.00 2000.00",
			expect: "1:15",
		},
		{
			name:   "empty",
			input:  "",
			expect: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseUptimeSeconds(tt.input)
			if got != tt.expect {
				t.Errorf("got %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestParseNetTools(t *testing.T) {
	input := `/usr/bin/curl
/bin/nc
/usr/bin/wget
`
	got := parseNetTools(input)
	expect := []string{"curl", "nc", "wget"}
	if !reflect.DeepEqual(got, expect) {
		t.Errorf("got %v, want %v", got, expect)
	}
}

func TestFormatWithOS(t *testing.T) {
	fp := &Fingerprint{
		ISA:            "arm",
		WordSize:       32,
		Endianness:     "little",
		ARMSubArch:     "v7",
		FloatABI:       "hard",
		Libc:           "uClibc 0.9.33",
		LibcVersion:    "uClibc 0.9.33.2",
		SoCHint:        "HiSilicon hi3516",
		ToolchainHint:  "armv7-linux-uclibceabihf",
		KernelVersion:  "Linux (none) 3.10.0-hi3516 #1 Tue Sep 15 10:00:00 CST 2020 armv7l GNU/Linux",
		BusyBoxVersion: "v1.26.2",
		DeviceModel:    "HiSilicon hi3516ev200",
		Uptime:         "3d 12:45",
		TotalRAM:       "128 MB",
		RootFSUsage:    "12M / 128M (9%)",
		Mounts:         []string{"/ : squashfs,ro", "/tmp : tmpfs,rw"},
		NetTools:       []string{"curl", "wget", "nc"},
	}
	output := fp.Format()

	if !strings.Contains(output, "OS Profile:") {
		t.Error("expected OS Profile section in output")
	}
	if !strings.Contains(output, "Kernel:") {
		t.Error("expected Kernel line")
	}
	if !strings.Contains(output, "BusyBox:   v1.26.2") {
		t.Error("expected BusyBox version")
	}
	if !strings.Contains(output, "Tools:     curl, wget, nc") {
		t.Error("expected Tools line")
	}
}

func TestFormatWithoutOS(t *testing.T) {
	fp := &Fingerprint{
		ISA:           "arm",
		WordSize:      32,
		Endianness:    "little",
		Libc:          "glibc",
		LibcVersion:   "glibc 2.28",
		ToolchainHint: "arm-linux-gnueabi",
	}
	output := fp.Format()

	if strings.Contains(output, "OS Profile:") {
		t.Error("expected no OS Profile section when all OS fields are empty")
	}
}
