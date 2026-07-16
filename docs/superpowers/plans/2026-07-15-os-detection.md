# OS Detection — Device Profile Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Добавить в режим `detect` сбор OS-профиля устройства (kernel, busybox, device model, libc version, memory, disk, mounts, net tools, uptime) как дополнение к архитектурному детекту.

**Architecture:** Новый файл `osprobe.go` с 8 best-effort командами и парсерами. Три существующих парсера в `phase1.go` расширяются для извлечения OS-данных из уже выполняемых команд. `Fingerprint` получает 10 новых полей. `Format()` дополняется секцией «OS Profile». Всё в одном telnet-соединении, probeOS никогда не возвращает ошибку.

**Tech Stack:** Go 1.22, стандартная библиотека + regexp, существующий telnet-клиент.

## Global Constraints

- Все OS-команды — best-effort: ошибка → поле пустое, выполнение продолжается
- probeOS() никогда не возвращает ошибку
- Архитектурный детект не должен быть затронут OS-зондом
- BusyBox-based IP-камеры / NVR, все ISA (ARM, AArch64, MIPS, x86, x86_64)
- Все libc family (glibc, uClibc, musl)
- Формат вывода: метки `SoC hint:` → `SoC:`, `Toolchain hint:` → `Toolchain:`

---

### Task 1: Add OS fields to Fingerprint struct

**Files:**
- Modify: `internal/detect/detect.go`

**Interfaces:**
- Produces: `Fingerprint` struct with 10 new OS fields (all `string` except `Mounts []string`, `NetTools []string`)

- [ ] **Step 1: Add OS fields to Fingerprint**

```go
// Fingerprint holds all detected architecture properties.
type Fingerprint struct {
	ISA           string // "arm", "aarch64", "mips", "x86", "x86_64"
	WordSize      int    // 32 or 64
	Endianness    string // "little" or "big"
	ARMSubArch    string // "v5", "v6", "v7", "" if not ARM
	FloatABI      string // "hard", "soft", "" if not applicable or unknown
	Libc          string // "glibc", "uClibc", "musl", ""
	SoCHint       string // e.g. "HiSilicon hi3516"
	ToolchainHint string // e.g. "arm-linux-uclibcgnueabihf"

	// OS profile (all best-effort, may be empty)
	KernelVersion  string   // full uname -a
	KernelBuild    string   // /proc/version
	BusyBoxVersion string   // "v1.31.1"
	DeviceModel    string   // "HiSilicon hi3516ev200"
	LibcVersion    string   // "glibc 2.28" / "uClibc 0.9.33.2"
	Uptime         string   // "3d 12:45"
	TotalRAM       string   // "128 MB"
	RootFSUsage    string   // "12M / 128M (9%)"
	Mounts         []string // ["/ : squashfs,ro", "/tmp : tmpfs,rw"]
	NetTools       []string // ["curl", "wget", "nc"]
}
```

- [ ] **Step 2: Verify build compiles**

Run: `go build ./...`
Expected: success (fields added but not yet used)

- [ ] **Step 3: Commit**

```bash
git add internal/detect/detect.go
git commit -m "feat(detect): add OS profile fields to Fingerprint struct"
```

---

### Task 2: Extract OS data from existing Phase 1 parsers

**Files:**
- Modify: `internal/detect/phase1.go`

**Interfaces:**
- Consumes: `Fingerprint.KernelVersion`, `KernelBuild`, `DeviceModel`, `LibcVersion` (from Task 1)
- Produces: populated `KernelVersion`, `KernelBuild`, `DeviceModel` from existing command outputs

- [ ] **Step 1: Save KernelVersion in parseUname**

Replace the `parseUname` function (lines 8-14 of func body):

```go
func parseUname(output string, fp *Fingerprint) {
	// Save full uname -a as KernelVersion
	fp.KernelVersion = strings.TrimSpace(output)

	for pattern, info := range unameMap {
		if strings.Contains(output, pattern) {
			fp.ISA = info.ISA
			fp.WordSize = info.WordSize
			return
		}
	}
}
```

- [ ] **Step 2: Save KernelBuild in parseProcVersion**

Replace the `parseProcVersion` function (lines 1-2 of func body):

```go
func parseProcVersion(output string, fp *Fingerprint) {
	// Save full /proc/version as KernelBuild
	fp.KernelBuild = strings.TrimSpace(output)

	lower := strings.ToLower(output)
	switch {
	// ... rest unchanged
```

- [ ] **Step 3: Extract DeviceModel from cpuinfo Hardware field**

Add to `parseCPUInfo`, after the "ARM model name" block and before "MIPS byteorder":

```go
	// Device model — Hardware field
	if strings.Contains(output, "Hardware") {
		re := regexp.MustCompile(`Hardware\s*:\s*(.+)`)
		if m := re.FindStringSubmatch(output); m != nil {
			fp.DeviceModel = strings.TrimSpace(m[1])
		}
	}
```

- [ ] **Step 4: Run existing unit tests**

Run: `go test ./internal/detect/ -v -run TestParse`
Expected: all existing parse tests pass

- [ ] **Step 5: Commit**

```bash
git add internal/detect/phase1.go
git commit -m "feat(detect): extract KernelVersion, KernelBuild, DeviceModel from phase 1"
```

---

### Task 3: Create osprobe.go with probeOS and all parsers

**Files:**
- Create: `internal/detect/osprobe.go`

**Interfaces:**
- Consumes: `telnet.TelnetClient.Execute(cmd ...string) ([]byte, error)`, `Fingerprint` with OS fields (Task 1)
- Produces: `probeOS(tc *telnet.TelnetClient, fp *Fingerprint)` — public function, never returns error
- Produces: internal parser functions: `parseBusyBoxVersion`, `parseDeviceTreeModel`, `parseGlibcVersionStderr`, `parseMeminfoTotal`, `parseDFRoot`, `parseMountsFiltered`, `parseUptimeSeconds`, `parseNetTools`

- [ ] **Step 1: Write the file**

```go
package detect

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/krabiswabbie/busyscout/internal/telnet"
)

// probeOS collects OS-level information from the target device.
// All commands are best-effort — failures leave fields empty.
// probeOS never returns an error.
func probeOS(tc *telnet.TelnetClient, fp *Fingerprint) {
	// BusyBox version
	if out, err := tc.Execute("busybox"); err == nil && len(out) > 0 {
		fp.BusyBoxVersion = parseBusyBoxVersion(string(out))
	}

	// Device-tree model (supplements cpuinfo Hardware)
	if out, err := tc.Execute("cat", "/proc/device-tree/model"); err == nil && len(out) > 0 {
		model := strings.TrimSpace(string(out))
		if model != "" {
			if fp.DeviceModel != "" {
				fp.DeviceModel += " (" + model + ")"
			} else {
				fp.DeviceModel = model
			}
		}
	}

	// Glibc version via .so execution
	if out, err := tc.Execute("sh", "-c", "/lib/libc.so.6 2>&1 || true"); err == nil && len(out) > 0 {
		ver := parseGlibcVersion(string(out))
		if ver != "" {
			if fp.LibcVersion == "" || fp.LibcVersion == "glibc" {
				fp.LibcVersion = "glibc " + ver
			}
		}
	}

	// /proc/meminfo → TotalRAM
	if out, err := tc.Execute("cat", "/proc/meminfo"); err == nil && len(out) > 0 {
		fp.TotalRAM = parseMeminfoTotal(string(out))
	}

	// df /
	if out, err := tc.Execute("sh", "-c", "df / 2>/dev/null || df 2>/dev/null"); err == nil && len(out) > 0 {
		fp.RootFSUsage = parseDFRoot(string(out))
	}

	// mounts
	if out, err := tc.Execute("sh", "-c", "mount 2>/dev/null || cat /proc/mounts"); err == nil && len(out) > 0 {
		fp.Mounts = parseMountsFiltered(string(out))
	}

	// /proc/uptime
	if out, err := tc.Execute("cat", "/proc/uptime"); err == nil && len(out) > 0 {
		fp.Uptime = parseUptimeSeconds(string(out))
	}

	// Net tools
	if out, err := tc.Execute(
		"sh", "-c",
		"ls /usr/bin/curl /usr/bin/wget /bin/nc /usr/sbin/nc /usr/bin/openssl /usr/bin/tftp /usr/bin/ftpget /usr/bin/ncat 2>/dev/null",
	); err == nil && len(out) > 0 {
		fp.NetTools = parseNetTools(string(out))
	}
}

// parseBusyBoxVersion extracts version from the first line of busybox output.
// First line format: "BusyBox v1.31.1 (2020-01-15 10:00:00 CST) multi-call binary."
func parseBusyBoxVersion(output string) string {
	idx := strings.Index(output, "\n")
	if idx == -1 {
		idx = len(output)
	}
	first := strings.TrimSpace(output[:idx])

	re := regexp.MustCompile(`BusyBox\s+v([\d.]+)`)
	if m := re.FindStringSubmatch(first); m != nil {
		return "v" + m[1]
	}
	return ""
}

// parseGlibcVersion extracts version from glibc .so execution stderr.
// Output example: "GNU C Library (GNU libc) stable release version 2.28."
func parseGlibcVersion(output string) string {
	re := regexp.MustCompile(`version\s+([\d.]+)`)
	if m := re.FindStringSubmatch(output); m != nil {
		return m[1]
	}
	return ""
}

// parseMeminfoTotal extracts MemTotal and formats as "NNN MB".
// Input: "MemTotal:         128456 kB"
func parseMeminfoTotal(output string) string {
	re := regexp.MustCompile(`MemTotal:\s*(\d+)\s*kB`)
	if m := re.FindStringSubmatch(output); m != nil {
		kb := 0
		fmt.Sscanf(m[1], "%d", &kb)
		mb := int(math.Round(float64(kb) / 1024.0))
		if mb < 1 {
			mb = 1
		}
		return fmt.Sprintf("%d MB", mb)
	}
	return ""
}

// parseDFRoot extracts usage info for root filesystem.
// Handles both "df /" and full "df" output — takes the second line (first data row).
func parseDFRoot(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		return ""
	}
	// Second line is the first data row
	data := strings.Fields(lines[1])
	if len(data) < 4 {
		return ""
	}
	// Typical columns: Filesystem 1K-blocks Used Available Use% Mounted
	used := data[2]
	avail := data[3]
	pct := ""
	if len(data) >= 5 {
		pct = data[4]
	}
	if pct != "" {
		return fmt.Sprintf("%s / %s (%s)", used, avail, pct)
	}
	return fmt.Sprintf("%s / %s", used, avail)
}

// parseMountsFiltered filters mount output to interesting mount points.
// Keeps: /, /tmp, /var, /mnt, /config, /data, /home, /system.
// Format: "/tmp : tmpfs,rw"
func parseMountsFiltered(output string) []string {
	interesting := map[string]bool{
		"/": true, "/tmp": true, "/var": true, "/mnt": true,
		"/config": true, "/data": true, "/home": true, "/system": true,
	}

	var result []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		mountPoint := fields[1] // /proc/mounts: device mountpoint fstype options
		if _, ok := interesting[mountPoint]; ok {
			fstype := fields[2]
			flags := ""
			if len(fields) >= 4 {
				flags = strings.TrimPrefix(fields[3], "(")
				flags = strings.TrimSuffix(flags, ")")
			}
			if flags != "" {
				result = append(result, fmt.Sprintf("%s : %s,%s", mountPoint, fstype, flags))
			} else {
				result = append(result, fmt.Sprintf("%s : %s", mountPoint, fstype))
			}
		}
	}
	return result
}

// parseUptimeSeconds converts /proc/uptime first field (seconds) to human-readable.
// Input: "123456.78 98765.43"
// Output: "1d 10:17"
func parseUptimeSeconds(output string) string {
	fields := strings.Fields(strings.TrimSpace(output))
	if len(fields) < 1 {
		return ""
	}
	var secs float64
	fmt.Sscanf(fields[0], "%f", &secs)
	if secs < 1 {
		return ""
	}

	totalSecs := int64(secs)
	days := totalSecs / 86400
	hours := (totalSecs % 86400) / 3600
	minutes := (totalSecs % 3600) / 60

	if days > 0 {
		return fmt.Sprintf("%dd %02d:%02d", days, hours, minutes)
	}
	return fmt.Sprintf("%d:%02d", hours, minutes)
}

// parseNetTools extracts basenames of existing network tools from ls output.
// Input: "/usr/bin/curl\n/usr/bin/wget\n/bin/nc\n"
// Output: ["curl", "wget", "nc"]
func parseNetTools(output string) []string {
	var tools []string
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		// Extract basename
		idx := strings.LastIndex(path, "/")
		if idx < 0 {
			continue
		}
		name := path[idx+1:]
		if !seen[name] {
			seen[name] = true
			tools = append(tools, name)
		}
	}
	return tools
}
```

- [ ] **Step 2: Verify build compiles**

Run: `go build ./...`
Expected: success

- [ ] **Step 3: Commit**

```bash
git add internal/detect/osprobe.go
git commit -m "feat(detect): add probeOS with BusyBox, libc ver, mem, disk, mounts, uptime, net tools parsers"
```

---

### Task 4: Integrate probeOS into runPhase1 and extend LibcVersion

**Files:**
- Modify: `internal/detect/phase1.go`

**Interfaces:**
- Consumes: `probeOS(tc, fp)` from Task 3
- Produces: `runPhase1` calls `probeOS` after all parsing, before `needsPhase2()`

- [ ] **Step 1: Save LibcVersion from parseLibc**

Replace the `parseLibc` function:

```go
func parseLibc(output string, fp *Fingerprint) {
	switch {
	case strings.Contains(output, "ld-musl"):
		fp.Libc = "musl"
		fp.LibcVersion = "musl"
	case strings.Contains(output, "ld-uClibc"):
		fp.Libc = extractUclibcVersion(output)
		fp.LibcVersion = fp.Libc
	case strings.Contains(output, "libc.so.6") || strings.Contains(output, "ld-linux"):
		fp.Libc = "glibc"
		fp.LibcVersion = "glibc"
	}
}
```

- [ ] **Step 2: Call probeOS at end of runPhase1**

Add after all parsing blocks and before the `needsPhase2()` check, after the endianness default block:

```go
	// --- OS probe (best-effort, supplements architecture data) ---
	probeOS(tc, fp)

	// Endianness default (ARM + AArch64 + x86 are LE; MIPS depends on cpuinfo)
```

- [ ] **Step 3: Run existing unit tests**

Run: `go test ./internal/detect/ -v -run TestParse`
Expected: all existing parse tests pass

- [ ] **Step 4: Commit**

```bash
git add internal/detect/phase1.go
git commit -m "feat(detect): integrate probeOS into runPhase1, save LibcVersion"
```

---

### Task 5: Update Format() with OS Profile section

**Files:**
- Modify: `internal/detect/detect.go`

**Interfaces:**
- Consumes: `Fingerprint` OS fields (Task 1)
- Produces: updated `Format()` output

- [ ] **Step 1: Add OS Profile section to Format()**

Replace the `Format()` method:

```go
// Format returns a human-readable representation of the fingerprint.
func (f *Fingerprint) Format() string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Architecture:     %s", f.archLabel()))
	if f.WordSize > 0 {
		b.WriteString(fmt.Sprintf(" (%d-bit, %s-endian)", f.WordSize, f.Endianness))
	}
	b.WriteString("\n")

	if f.FloatABI != "" {
		label := f.FloatABI
		if label == "hard" {
			label = "hard-float (VFP)"
		} else if label == "soft" {
			label = "soft-float"
		}
		b.WriteString(fmt.Sprintf("Float ABI:        %s\n", label))
	} else if f.ISA == "arm" {
		b.WriteString("Float ABI:        [uncertain]\n")
	}

	libcLabel := f.LibcVersion
	if libcLabel == "" {
		libcLabel = f.Libc
	}
	if libcLabel != "" {
		b.WriteString(fmt.Sprintf("libc:             %s\n", libcLabel))
	} else {
		b.WriteString("libc:             [uncertain]\n")
	}

	if f.SoCHint != "" {
		b.WriteString(fmt.Sprintf("SoC:              %s\n", f.SoCHint))
	}

	if f.ToolchainHint != "" {
		b.WriteString(fmt.Sprintf("Toolchain:        %s\n", f.ToolchainHint))
	}

	// OS Profile section
	f.writeOSProfile(&b)

	return b.String()
}

// writeOSProfile appends the OS Profile section if any OS fields are populated.
func (f *Fingerprint) writeOSProfile(b *strings.Builder) {
	hasOS := f.KernelVersion != "" || f.KernelBuild != "" || f.BusyBoxVersion != "" ||
		f.DeviceModel != "" || f.Uptime != "" || f.TotalRAM != "" ||
		f.RootFSUsage != "" || len(f.Mounts) > 0 || len(f.NetTools) > 0

	if !hasOS {
		return
	}

	b.WriteString("\nOS Profile:\n")

	if f.KernelVersion != "" {
		b.WriteString(fmt.Sprintf("  Kernel:    %s\n", f.KernelVersion))
	}
	if f.KernelBuild != "" {
		b.WriteString(fmt.Sprintf("  Build:     %s\n", f.KernelBuild))
	}
	if f.BusyBoxVersion != "" {
		b.WriteString(fmt.Sprintf("  BusyBox:   %s\n", f.BusyBoxVersion))
	}
	if f.DeviceModel != "" {
		b.WriteString(fmt.Sprintf("  Device:    %s\n", f.DeviceModel))
	}
	if f.Uptime != "" {
		b.WriteString(fmt.Sprintf("  Uptime:    %s\n", f.Uptime))
	}
	if f.TotalRAM != "" {
		b.WriteString(fmt.Sprintf("  RAM:       %s\n", f.TotalRAM))
	}
	if f.RootFSUsage != "" {
		b.WriteString(fmt.Sprintf("  Disk (/):  %s\n", f.RootFSUsage))
	}
	if len(f.Mounts) > 0 {
		b.WriteString("  Mounts:\n")
		for _, m := range f.Mounts {
			b.WriteString(fmt.Sprintf("    %s\n", m))
		}
	}
	if len(f.NetTools) > 0 {
		b.WriteString(fmt.Sprintf("  Tools:     %s\n", strings.Join(f.NetTools, ", ")))
	}
}
```

- [ ] **Step 2: Verify build compiles**

Run: `go build ./...`
Expected: success

- [ ] **Step 3: Commit**

```bash
git add internal/detect/detect.go
git commit -m "feat(detect): add OS Profile section to Format() output"
```

---

### Task 6: Unit tests for OS parsers

**Files:**
- Create: `internal/detect/osprobe_test.go`

**Interfaces:**
- Consumes: all parser functions from Task 3

- [ ] **Step 1: Write the test file**

```go
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
			expect: "2.28",
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
			expect: "12000 / 116000 (9%)",
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
		"/ : squashfs,ro",
		"/tmp : tmpfs,rw",
		"/mnt : jffs2,rw",
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
		ISA:          "arm",
		WordSize:     32,
		Endianness:   "little",
		Libc:         "glibc",
		LibcVersion:  "glibc 2.28",
		ToolchainHint: "arm-linux-gnueabi",
	}
	output := fp.Format()

	if strings.Contains(output, "OS Profile:") {
		t.Error("expected no OS Profile section when all OS fields are empty")
	}
}
```

- [ ] **Step 2: Run unit tests**

Run: `go test ./internal/detect/ -v -run "TestParse|TestFormat"`
Expected: all tests PASS

- [ ] **Step 3: Commit**

```bash
git add internal/detect/osprobe_test.go
git commit -m "test(detect): unit tests for OS parsers and Format output"
```

---

### Task 7: Integration test against telnetd container

**Files:**
- Modify: `tests/docker-compose.yaml` (if needed)
- Modify: `Makefile`

**Interfaces:**
- Consumes: full `detect` command from all previous tasks

- [ ] **Step 1: Verify telnetd container has OS probe prerequisites**

The `wistic/telnetd` container (Alpine-based) already provides: `/proc/meminfo`, `/proc/uptime`, `/proc/mounts`, `busybox`, `df`, `mount`, `ls`. No docker-compose changes needed.

- [ ] **Step 2: Add integration test target to Makefile**

```makefile
test-integration-detect:
	docker compose -f tests/docker-compose.yaml up -d
	sleep 2
	go run . detect admin:admin@127.0.0.1:2323
	docker compose -f tests/docker-compose.yaml down
```

- [ ] **Step 3: Run integration test**

Run: `make test-integration-detect`
Expected: output includes "OS Profile:" section with non-empty fields (at least Kernel, BusyBox, RAM)

- [ ] **Step 4: Commit**

```bash
git add Makefile
git commit -m "test(detect): add integration test target for OS profile"
```

---

### Task 8: Final verification

**Files:**
- (none — verification only)

- [ ] **Step 1: Run all detect tests**

Run: `go test ./internal/detect/ -v`
Expected: all tests PASS

- [ ] **Step 2: Run full project build**

Run: `go build ./... && go vet ./...`
Expected: no errors

- [ ] **Step 3: Run full project tests**

Run: `go test ./...`
Expected: all tests PASS

- [ ] **Step 4: Commit any final changes if needed**

```bash
git status
# if clean, done. Otherwise:
git add -A
git commit -m "chore: final verification, fix any issues"
```
