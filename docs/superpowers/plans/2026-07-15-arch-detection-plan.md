# Architecture Detection — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `busyscout detect` subcommand that fingerprints target device architecture (ISA, endianness, libc, float ABI, CPU sub-arch) via telnet.

**Architecture:** Two-phase detection — Phase 1 gathers CPU/libc info via native BusyBox commands; Phase 2 uploads a tiny static ELF-reader helper via printf when native tools (`od`/`file`) are absent. The existing printf-based upload is extracted into `UploadData` and reused.

**Tech Stack:** Go 1.22, C (statically-linked helper, ~4 KB per arch), telnet transport, `//go:embed` for helper binaries.

## Global Constraints

- Target: Go 1.22.2 (from go.mod)
- Binary helpers must be statically linked and stripped
- All telnet connections inherit `TelnetClient.Timeout` (10s default)
- Errors wrapped via `github.com/joomcode/errorx`
- Output is human-readable only (JSON deferred)
- `--verbose` flag mirrors existing behavior: raw command output to stderr

---

## File Structure

```
internal/
  detect/
    detect.go      — Fingerprint type, Detect() entry point, formatting
    phase1.go      — runPhase1: execute native cmds, parse uname/cpuinfo/libc/SOC/output of file/od/readelf
    phase2.go      — runPhase2: select helper, upload, execute, parse key=value
  helpers/
    helpers.go     — //go:embed of 6 elfreader binaries, HelperForISA(isa) []byte
    elfreader.c    — C source (reference, not compiled at Go build time)
    bin/           — pre-built elfreader-* (committed)
  scout/
    upload.go      — UploadData(tc, data, remotePath): printf-based blob upload
    scout.go       — refactored: sendChunk delegates to UploadData
  telnet/
    telnet.go      — unchanged
main.go            — refactored: subcommand dispatch (push | detect)
Makefile           — added `helpers` target for cross-compilation
```

---

### Task 1: Extract UploadData from sendChunk

**Files:**
- Create: `internal/scout/upload.go`
- Modify: `internal/scout/scout.go` (sendChunk refactor)

**Interfaces:**
- Produces: `func UploadData(tc *telnet.TelnetClient, data []byte, targetFileName string) error`

- [ ] **Step 1: Create upload.go with UploadData**

```go
// internal/scout/upload.go
package scout

import (
	"fmt"
	"github.com/krabiswabbie/busyscout/internal/telnet"
)

// UploadData sends binary data to a remote file via printf over an already-open telnet connection.
// The caller owns the connection lifecycle (Dial/Close).
func UploadData(tc *telnet.TelnetClient, data []byte, targetFileName string) error {
	targetFileName = toUnixPath(targetFileName)
	redirectMode := ">"

	for i := 0; i < len(data); i += lineSize {
		end := i + lineSize
		if end > len(data) {
			end = len(data)
		}

		cmd := "printf '"
		for _, bt := range data[i:end] {
			cmd += fmt.Sprintf("\\x%02x", bt)
		}
		cmd += fmt.Sprintf("' %s %s\n", redirectMode, targetFileName)
		redirectMode = ">>"

		if _, err := tc.Execute(cmd); err != nil {
			return err
		}
	}

	return nil
}
```

- [ ] **Step 2: Refactor sendChunk to use UploadData**

Replace the body of `sendChunk` in `internal/scout/scout.go`:

```go
func (s *Scout) sendChunk(data []byte, targetFileName string) (progress int, err error) {
	tc, errClient := s.newClient()
	if errClient != nil {
		return 0, errClient
	}
	defer tc.Close()

	if errSend := UploadData(tc, data, toUnixPath(targetFileName)); errSend != nil {
		return 0, errSend
	}

	progress = len(data)
	s.bar.Add(progress)
	return progress, nil
}
```
(Note: `fmt` import stays in scout.go — still used by `joinChunks`, `checkFileSize`, `checkIsRemoteDirectory`.)

- [ ] **Step 3: Run existing tests to verify refactor**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && go test ./...
```

Expected: all tests pass. `files_test.go` should not be affected.

- [ ] **Step 4: Commit**

```bash
git add internal/scout/upload.go internal/scout/scout.go
git commit -m "refactor: extract UploadData from sendChunk for reuse"
```

---

### Task 2: CLI refactor — subcommand dispatch

**Files:**
- Modify: `main.go`

**Interfaces:**
- Produces: two subcommands — `push` (existing behavior) and `detect` (new)

- [ ] **Step 1: Rewrite main.go with subcommand dispatch**

```go
// main.go
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/krabiswabbie/busyscout/internal/detect"
	"github.com/krabiswabbie/busyscout/internal/scout"
	"k8s.io/klog/v2"
)

var Version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	subcmd := os.Args[1]
	switch subcmd {
	case "push":
		cmdPush(os.Args[2:])
	case "detect":
		cmdDetect(os.Args[2:])
	default:
		// Backward compat: if first arg looks like a file, treat as push
		if len(os.Args) >= 3 && !strings.HasPrefix(os.Args[1], "-") {
			cmdPush(os.Args[1:])
		} else {
			printUsage()
			os.Exit(0)
		}
	}
}

func printUsage() {
	fmt.Printf("busyscout %s\n", Version)
	fmt.Println("Usage:")
	fmt.Println("  busyscout push   <local_file> <remote_target> [--verbose]")
	fmt.Println("  busyscout detect <remote_target> [--verbose]")
	fmt.Println()
	fmt.Println("Remote target format: user:pass@host:port:/path")
	fmt.Println("Examples:")
	fmt.Println("  busyscout push firmware.bin admin:12345@192.168.1.100:/tmp")
	fmt.Println("  busyscout detect admin:12345@192.168.1.100:/tmp --verbose")
}

func cmdPush(args []string) {
	argsCount := len(args)
	if argsCount < 2 || argsCount > 3 || argsCount == 3 && args[2] != "--verbose" {
		fmt.Printf("busyscout %s\n", Version)
		fmt.Println("Usage:   busyscout push <local_file> <remote_target> [--verbose]")
		fmt.Println("Example: busyscout push ipwiz.zip root:12345@192.168.10.18:/tmp")
		os.Exit(0)
	}

	sourceFile := args[0]
	targetFile := args[1]
	verboseFlag := argsCount == 3 && args[2] == "--verbose"

	s, errNew := scout.New(sourceFile, targetFile, verboseFlag)
	if errNew != nil {
		klog.Fatal(errNew)
	}

	if errPush := s.Push(); errPush != nil {
		klog.Fatal(errPush)
	}
}

func cmdDetect(args []string) {
	argsCount := len(args)
	if argsCount < 1 || argsCount > 2 || argsCount == 2 && args[1] != "--verbose" {
		fmt.Printf("busyscout %s\n", Version)
		fmt.Println("Usage:   busyscout detect <remote_target> [--verbose]")
		fmt.Println("Example: busyscout detect admin:12345@192.168.10.18:/tmp")
		os.Exit(0)
	}

	target := args[0]
	verboseFlag := argsCount == 2 && args[1] == "--verbose"

	fp, errDetect := detect.Detect(target, verboseFlag)
	if errDetect != nil {
		klog.Fatal(errDetect)
	}

	fmt.Print(fp.Format())
}
```

- [ ] **Step 2: Build and verify CLI parsing**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && go build -o /dev/null .
```

Expected: build fails with "package .../detect not found" — expected, Task 3 creates it.

- [ ] **Step 3: Commit**

```bash
git add main.go
git commit -m "refactor: subcommand dispatch (push / detect) with backward compat"
```

---

### Task 3: Fingerprint type + Detect entry point

**Files:**
- Create: `internal/detect/detect.go`

**Interfaces:**
- Produces: `type Fingerprint struct{...}`, `func Detect(remote string, verbose bool) (*Fingerprint, error)`, `func (f *Fingerprint) Format() string`

- [ ] **Step 1: Create detect.go with Fingerprint type and Detect skeleton**

```go
// internal/detect/detect.go
package detect

import (
	"fmt"
	"strings"

	"github.com/joomcode/errorx"
	"github.com/krabiswabbie/busyscout/internal/scout"
	"github.com/krabiswabbie/busyscout/internal/telnet"
)

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
}

// Detect connects to the target device and fingerprints its architecture.
func Detect(remote string, verbose bool) (*Fingerprint, error) {
	rm, err := scout.ParseRemoteFileName(remote)
	if err != nil {
		return nil, errorx.Decorate(err, "failed to parse remote address")
	}

	fp := &Fingerprint{}

	// Phase 1: Native commands
	if err := runPhase1(fp, rm, verbose); err != nil {
		return nil, errorx.Decorate(err, "phase 1 failed")
	}

	// Phase 2: Helper binary (only if needed)
	if err := runPhase2(fp, rm, verbose); err != nil {
		return nil, errorx.Decorate(err, "phase 2 failed")
	}

	// Derive toolchain hint
	fp.deriveToolchainHint()

	return fp, nil
}

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

	if f.Libc != "" {
		b.WriteString(fmt.Sprintf("libc:             %s\n", f.Libc))
	} else {
		b.WriteString("libc:             [uncertain]\n")
	}

	if f.SoCHint != "" {
		b.WriteString(fmt.Sprintf("SoC hint:         %s\n", f.SoCHint))
	}

	if f.ToolchainHint != "" {
		b.WriteString(fmt.Sprintf("Toolchain hint:   %s\n", f.ToolchainHint))
	}

	return b.String()
}

func (f *Fingerprint) archLabel() string {
	if f.ISA == "arm" && f.ARMSubArch != "" {
		return strings.ToUpper("arm" + f.ARMSubArch)
	}
	switch f.ISA {
	case "aarch64":
		return "AArch64"
	case "x86_64":
		return "x86_64"
	case "x86":
		return "x86"
	case "mips":
		return "MIPS"
	}
	return f.ISA
}

func (f *Fingerprint) deriveToolchainHint() {
	if f.ISA == "" {
		return
	}

	var arch, libc, abi string

	switch f.ISA {
	case "arm":
		arch = "arm"
		if f.ARMSubArch != "" {
			arch = "arm" + f.ARMSubArch
		}
	case "aarch64":
		arch = "aarch64"
	case "mips":
		if f.Endianness == "little" {
			arch = "mipsel"
		} else {
			arch = "mips"
		}
	case "x86":
		arch = "i386"
	case "x86_64":
		arch = "x86_64"
	}

	switch {
	case strings.Contains(f.Libc, "uClibc"):
		libc = "uclibc"
	case strings.Contains(f.Libc, "musl"):
		libc = "musl"
	default:
		libc = "gnu"
	}

	if f.FloatABI == "hard" {
		abi = "eabihf"
	} else if f.ISA == "arm" {
		abi = "eabi"
	}

	f.ToolchainHint = strings.TrimRight(arch+"-linux-"+libc+abi, "-")
}

// newTelnetClient creates a connected telnet client for the given remote.
func newTelnetClient(rm *scout.RemoteFile, verbose bool) (*telnet.TelnetClient, error) {
	tc := &telnet.TelnetClient{
		Address:  rm.Host,
		Port:     rm.Port,
		Login:    rm.Username,
		Password: rm.Password,
		Verbose:  verbose,
	}

	if err := tc.Dial(); err != nil {
		return nil, errorx.Decorate(err, "failed to open telnet connection")
	}

	return tc, nil
}
```

- [ ] **Step 3: Verify build fails with expected missing symbols**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && go build ./... 2>&1
```

Expected: `undefined: runPhase1`, `undefined: runPhase2` — confirms correct skeleton, Task 4+5 fill in.

- [ ] **Step 4: Commit**

```bash
git add internal/detect/detect.go
git commit -m "feat(detect): Fingerprint type, Detect skeleton, formatting"
```

---

### Task 4: Phase 1 — native command execution and parsing

**Files:**
- Create: `internal/detect/phase1.go`

**Interfaces:**
- Consumes: `*Fingerprint`, `*scout.RemoteFile`, `bool verbose`
- Produces: `func runPhase1(fp *Fingerprint, rm *scout.RemoteFile, verbose bool) error`

- [ ] **Step 1: Create phase1.go**

```go
// internal/detect/phase1.go
package detect

import (
	"regexp"
	"strings"

	"github.com/joomcode/errorx"
	"github.com/krabiswabbie/busyscout/internal/scout"
)

// knownMappings maps uname -m values to fingerprint fields.
type unameInfo struct {
	ISA      string
	WordSize int
}

var unameMap = map[string]unameInfo{
	"armv5l":   {"arm", 32},
	"armv5tel": {"arm", 32},
	"armv6l":   {"arm", 32},
	"armv7l":   {"arm", 32},
	"armv8l":   {"arm", 32},
	"aarch64":  {"aarch64", 64},
	"mips":     {"mips", 32},
	"mips64":   {"mips", 64},
	"i386":     {"x86", 32},
	"i486":     {"x86", 32},
	"i586":     {"x86", 32},
	"i686":     {"x86", 32},
	"x86_64":   {"x86_64", 64},
}

func runPhase1(fp *Fingerprint, rm *scout.RemoteFile, verbose bool) error {
	tc, err := newTelnetClient(rm, verbose)
	if err != nil {
		return err
	}
	defer tc.Close()

	// Collect raw outputs
	unameOut, _ := tc.Execute("uname", "-a")
	cpuinfoOut, _ := tc.Execute("cat", "/proc/cpuinfo")
	versionOut, _ := tc.Execute("cat", "/proc/version")
	libcOut, _ := tc.Execute("ls -l /lib/libc.so* /lib/ld-*.so* /lib/ld-uClibc* /lib/ld-musl* 2>/dev/null")

	// SoC hints (best-effort)
	hisiOut, _ := tc.Execute("cat", "/proc/hisi_chipid")
	umapOut, _ := tc.Execute("ls", "/proc/umap")
	dmesgOut, _ := tc.Execute("sh -c 'dmesg 2>/dev/null | grep -iE \"hisi|chip|soc\" | head -5'")

	// Optional tools
	fileOut, _ := tc.Execute("file", "/bin/busybox")
	odOut, _ := tc.Execute("sh -c 'od -An -t x1 -N20 /bin/busybox 2>/dev/null | head -1'")
	readelfOut, _ := tc.Execute("sh -c 'readelf -A /bin/busybox 2>/dev/null | grep -E \"Tag_CPU_arch|Tag_ABI_VFP|Tag_FP_arch\"'")

	// --- Parse ---

	// 1. uname
	parseUname(string(unameOut), fp)

	// 2. /proc/cpuinfo
	parseCPUInfo(string(cpuinfoOut), fp)

	// 3. libc
	parseLibc(string(libcOut), fp)

	// 4. SoC hints
	parseSoC(string(hisiOut), string(umapOut), string(dmesgOut), fp)

	// 5. File command (optional, may override)
	if fileOut != nil && len(fileOut) > 0 {
		parseFileOutput(string(fileOut), fp)
	}

	// 6. od — raw ELF bytes (optional)
	if odOut != nil && len(odOut) > 0 {
		parseODOutput(string(odOut), fp)
	}

	// 7. readelf -A — ARM float ABI (optional)
	if readelfOut != nil && len(readelfOut) > 0 {
		parseReadelfOutput(string(readelfOut), fp)
	}

	// 8. /proc/version as fallback for ISA hints
	if fp.ISA == "" {
		parseProcVersion(string(versionOut), fp)
	}

	// Endianness default (ARM + AArch64 + x86 are LE; MIPS depends on cpuinfo)
	if fp.Endianness == "" {
		switch fp.ISA {
		case "mips":
			fp.Endianness = "big" // default for MIPS, overridden by cpuinfo if "mipsel"
		case "":
			// can't determine
		default:
			fp.Endianness = "little"
		}
	}

	// Validation
	if fp.ISA == "" {
		return errorx.IllegalState.New("could not determine ISA from uname or /proc/cpuinfo")
	}

	return nil
}

// parseUname extracts ISA and word size from "uname -a" output.
// Typical: "Linux (none) 3.0.8 #7 PREEMPT Thu Jan 1 00:00:00 CST 1970 armv7l GNU/Linux"
func parseUname(output string, fp *Fingerprint) {
	for pattern, info := range unameMap {
		if strings.Contains(output, pattern) {
			fp.ISA = info.ISA
			fp.WordSize = info.WordSize
			return
		}
	}
}

// parseCPUInfo extracts ARM sub-arch, features, and MIPS byteorder from /proc/cpuinfo.
func parseCPUInfo(output string, fp *Fingerprint) {
	// ARM: CPU architecture, Features (vfp, neon, half, thumb)
	if strings.Contains(output, "CPU architecture") {
		re := regexp.MustCompile(`CPU architecture:\s*(\d+)`)
		if m := re.FindStringSubmatch(output); m != nil {
			fp.ARMSubArch = "v" + m[1]
		}
	}

	// ARM features
	if strings.Contains(output, "Features") {
		re := regexp.MustCompile(`Features\s*:\s*(.+)`)
		if m := re.FindStringSubmatch(output); m != nil {
			features := m[1]
			// Hard-float hint: vfp + neon usually means hard-float capable
			// (definitive answer requires readelf or helper)
			if fp.FloatABI == "" && strings.Contains(features, "vfp") {
				// Don't set FloatABI here — vfp present doesn't guarantee hard-float ABI
				// This is just a confidence hint; readelf or helper confirms
			}
		}
	}

	// ARM model name
	if strings.Contains(output, "model name") {
		re := regexp.MustCompile(`model name\s*:\s*(.+)`)
		if m := re.FindStringSubmatch(output); m != nil {
			model := strings.TrimSpace(m[1])
			if strings.Contains(strings.ToLower(model), "armv6") && fp.ARMSubArch == "" {
				fp.ARMSubArch = "v6"
			}
		}
	}

	// MIPS byteorder
	if strings.Contains(output, "byteorder") {
		re := regexp.MustCompile(`byteorder\s*:\s*(\S+)`)
		if m := re.FindStringSubmatch(output); m != nil {
			switch strings.ToLower(m[1]) {
			case "little endian":
				fp.Endianness = "little"
			case "big endian":
				fp.Endianness = "big"
			}
		}
	}

	// MIPS system type
	if strings.Contains(output, "system type") {
		re := regexp.MustCompile(`system type\s*:\s*(.+)`)
		if m := re.FindStringSubmatch(output); m != nil {
			st := strings.TrimSpace(m[1])
			if strings.Contains(st, "MIPS") && fp.ISA == "" {
				fp.ISA = "mips"
				fp.WordSize = 32
			}
		}
	}

	// MIPS cpu model
	if strings.Contains(output, "cpu model") {
		re := regexp.MustCompile(`cpu model\s*:\s*(.+)`)
		if m := re.FindStringSubmatch(output); m != nil {
			model := strings.TrimSpace(m[1])
			if strings.Contains(strings.ToLower(model), "mips") && fp.ISA == "" {
				fp.ISA = "mips"
				fp.WordSize = 32
			}
		}
	}
}

// parseLibc determines libc family from library file listing.
func parseLibc(output string, fp *Fingerprint) {
	switch {
	case strings.Contains(output, "ld-musl"):
		fp.Libc = "musl"
	case strings.Contains(output, "ld-uClibc"):
		fp.Libc = "uClibc"
		fp.Libc = extractUclibcVersion(output)
	case strings.Contains(output, "libc.so.6") || strings.Contains(output, "ld-linux"):
		fp.Libc = "glibc"
	}
}

func extractUclibcVersion(output string) string {
	re := regexp.MustCompile(`libuClibc-([\d.]+)\.so`)
	if m := re.FindStringSubmatch(output); m != nil {
		return "uClibc " + m[1]
	}
	return "uClibc"
}

// parseSoC extracts SoC hints from multiple sources.
func parseSoC(hisiOut, umapOut, dmesgOut string, fp *Fingerprint) {
	// /proc/hisi_chipid — direct chip ID
	if hisiOut != "" {
		chipID := strings.TrimSpace(hisiOut)
		if chipID != "" {
			soc := mapHiChipID(chipID)
			if soc != "" {
				fp.SoCHint = soc
				return
			}
		}
	}

	// /proc/umap — HiSilicon media platform
	if umapOut != "" {
		re := regexp.MustCompile(`(hi\d+\w*)`)
		if m := re.FindStringSubmatch(strings.ToLower(umapOut)); m != nil {
			fp.SoCHint = "HiSilicon " + m[1]
			return
		}
	}

	// dmesg
	if dmesgOut != "" {
		re := regexp.MustCompile(`(?i)(hi\d+\w*)`)
		if m := re.FindStringSubmatch(dmesgOut); m != nil {
			fp.SoCHint = "HiSilicon " + strings.ToLower(m[1])
			return
		}
	}
}

func mapHiChipID(id string) string {
	id = strings.TrimPrefix(strings.ToLower(id), "0x")
	m := map[string]string{
		"35180100": "HiSilicon hi3518",
		"35160100": "HiSilicon hi3516",
		"3516a100": "HiSilicon hi3516a",
		"3516c100": "HiSilicon hi3516c",
		"3516d100": "HiSilicon hi3516d",
		"3516ev100": "HiSilicon hi3516ev100",
		"3519v101": "HiSilicon hi3519v101",
		"35360100": "HiSilicon hi3536",
	}
	if v, ok := m[id]; ok {
		return v
	}
	return ""
}

// parseFileOutput extracts ELF info from `file /bin/busybox` output.
// Example: "/bin/busybox: ELF 32-bit LSB executable, ARM, version 1 (SYSV), ..."
func parseFileOutput(output string, fp *Fingerprint) {
	lower := strings.ToLower(output)

	if strings.Contains(lower, "32-bit") {
		fp.WordSize = 32
	} else if strings.Contains(lower, "64-bit") {
		fp.WordSize = 64
	}

	if strings.Contains(lower, "lsb") {
		fp.Endianness = "little"
	} else if strings.Contains(lower, "msb") {
		fp.Endianness = "big"
	}

	switch {
	case strings.Contains(lower, "arm"):
		fp.ISA = "arm"
		// Try to extract version
		re := regexp.MustCompile(`(?i)ARMv(\d+)`)
		if m := re.FindStringSubmatch(output); m != nil {
			fp.ARMSubArch = "v" + m[1]
		}
	case strings.Contains(lower, "aarch64"):
		fp.ISA = "aarch64"
	case strings.Contains(lower, "mips"):
		fp.ISA = "mips"
	case strings.Contains(lower, "80386") || strings.Contains(lower, "i386"):
		fp.ISA = "x86"
	case strings.Contains(lower, "x86-64") || strings.Contains(lower, "x86_64"):
		fp.ISA = "x86_64"
	}
}

// parseODOutput parses the hex dump of the first 20 bytes of an ELF file.
// Example: " 7f 45 4c 46 01 01 01 00 00 00 00 00 00 00 00 00 02 00 28 00"
func parseODOutput(output string, fp *Fingerprint) {
	fields := strings.Fields(strings.TrimSpace(output))
	if len(fields) < 19 {
		return
	}

	// Byte 4: ELF class
	if fields[4] == "01" {
		fp.WordSize = 32
	} else if fields[4] == "02" {
		fp.WordSize = 64
	}

	// Byte 5: endianness
	if fields[5] == "01" {
		fp.Endianness = "little"
	} else if fields[5] == "02" {
		fp.Endianness = "big"
	}

	// Bytes 18-19: e_machine (little-endian interpretation)
	if fp.Endianness == "little" && len(fields) >= 19 {
		em := fields[17] + fields[18] // LE: low byte first
		switch em {
		case "2800":
			fp.ISA = "arm"
		case "0800":
			fp.ISA = "mips"
		case "b700":
			fp.ISA = "aarch64"
		case "0300":
			fp.ISA = "x86"
		case "3e00":
			fp.ISA = "x86_64"
		}
	} else if fp.Endianness == "big" && len(fields) >= 19 {
		em := fields[17] + fields[18] // BE: high byte first
		switch em {
		case "0028":
			fp.ISA = "arm"
		case "0008":
			fp.ISA = "mips"
		case "00b7":
			fp.ISA = "aarch64"
		}
	}
}

// parseReadelfOutput parses `readelf -A` for ARM attributes.
func parseReadelfOutput(output string, fp *Fingerprint) {
	// Tag_CPU_arch: v5TE / v7 / v8 etc.
	reArch := regexp.MustCompile(`Tag_CPU_arch:\s*v?(\d+\w*)`)
	if m := reArch.FindStringSubmatch(output); m != nil {
		fp.ARMSubArch = "v" + m[1]
	}

	// Tag_ABI_VFP_args: VFP registers → hard-float
	if strings.Contains(output, "Tag_ABI_VFP_args: VFP registers") {
		fp.FloatABI = "hard"
	} else if strings.Contains(output, "Tag_ABI_VFP_args") {
		fp.FloatABI = "soft"
	}
}

// parseProcVersion extracts architecture from /proc/version when uname fails.
func parseProcVersion(output string, fp *Fingerprint) {
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "arm"):
		fp.ISA = "arm"
		fp.WordSize = 32
	case strings.Contains(lower, "aarch64"):
		fp.ISA = "aarch64"
		fp.WordSize = 64
	case strings.Contains(lower, "mips"):
		fp.ISA = "mips"
		fp.WordSize = 32
	case strings.Contains(lower, "x86_64"):
		fp.ISA = "x86_64"
		fp.WordSize = 64
	case strings.Contains(lower, "i386") || strings.Contains(lower, "i686"):
		fp.ISA = "x86"
		fp.WordSize = 32
	}
}

// needsPhase2 returns true if ELF data is incomplete and a helper would help.
func (fp *Fingerprint) needsPhase2() bool {
	// Need: ISA confirmed, endianness, word size
	// Helper adds: definitive e_machine, ARM float ABI, CPU arch
	if fp.ISA == "" {
		return false // can't proceed to phase 2 anyway
	}
	// If ARM and float ABI unknown, helper helps
	if fp.ISA == "arm" && fp.FloatABI == "" {
		return true
	}
	// If ISA uncertain (parsed only from uname, not confirmed by ELF), helper confirms
	// Always run if we got here without od/file providing ELF confirmation
	return fp.WordSize == 0
}
```

- [ ] **Step 2: Verify build compiles**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && go build ./...
```

Expected: build succeeds (phase2 is still a stub but Task 3 has a call to it; we need Task 5 to compile). Actually, `runPhase2` is called in detect.go but defined in Task 5. The build will fail. Add a temporary stub.

- [ ] **Step 3: Add temporary runPhase2 stub to detect.go for compilation**

Append to `internal/detect/detect.go`:

```go
// runPhase2 stub — implemented in phase2.go
func runPhase2(fp *Fingerprint, rm *scout.RemoteFile, verbose bool) error {
	return nil
}
```

Note: this stub will be removed in Task 5 when phase2.go is created.

- [ ] **Step 4: Verify build compiles**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && go build ./...
```

Expected: build succeeds.

- [ ] **Step 5: Commit**

```bash
git add internal/detect/phase1.go internal/detect/detect.go
git commit -m "feat(detect): Phase 1 — native command execution and parsing"
```

---

### Task 5: Phase 2 — helper upload and execution

**Files:**
- Create: `internal/detect/phase2.go`
- Modify: `internal/detect/detect.go` (replace stub with real call, add needsPhase2 gate)

**Interfaces:**
- Consumes: `*Fingerprint`, `*scout.RemoteFile`, `bool verbose`, `helpers.HelperForISA(isa string) ([]byte, error)`
- Produces: `func runPhase2(fp *Fingerprint, rm *scout.RemoteFile, verbose bool) error`

- [ ] **Step 1: Create phase2.go**

```go
// internal/detect/phase2.go
package detect

import (
	"strings"

	"github.com/joomcode/errorx"
	"github.com/krabiswabbie/busyscout/internal/helpers"
	"github.com/krabiswabbie/busyscout/internal/scout"
)

const helperRemotePath = "/tmp/bs-helper"

func runPhase2(fp *Fingerprint, rm *scout.RemoteFile, verbose bool) error {
	if !fp.needsPhase2() {
		return nil
	}

	helperData, err := helpers.HelperForISA(fp.ISA)
	if err != nil {
		return errorx.Decorate(err, "no helper available for detected ISA")
	}

	// Open a fresh connection
	tc, err := newTelnetClient(rm, verbose)
	if err != nil {
		return err
	}
	defer tc.Close()

	// Upload helper binary
	if err := scout.UploadData(tc, helperData, helperRemotePath); err != nil {
		return errorx.Decorate(err, "failed to upload helper binary")
	}

	// Make executable and run
	if _, err := tc.Execute("chmod", "+x", helperRemotePath); err != nil {
		return errorx.Decorate(err, "failed to chmod helper")
	}

	stdout, err := tc.Execute(helperRemotePath, "/bin/busybox")
	if err != nil {
		return errorx.Decorate(err, "failed to execute helper")
	}

	// Cleanup (best-effort)
	tc.Execute("rm", "-f", helperRemotePath)

	// Parse helper output
	parseHelperOutput(string(stdout), fp)

	return nil
}

// parseHelperOutput parses key=value lines from the helper binary.
func parseHelperOutput(output string, fp *Fingerprint) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])

		switch key {
		case "class":
			switch value {
			case "32":
				fp.WordSize = 32
			case "64":
				fp.WordSize = 64
			}
		case "endian":
			fp.Endianness = value // "little" or "big"
		case "machine":
			// Map e_machine number to ISA
			fp.ISA = mapMachineToISA(value)
		case "cpu_arch":
			fp.ARMSubArch = value // "v5", "v6", "v7", etc.
		case "float_abi":
			fp.FloatABI = value // "hard" or "soft"
		}
	}
}

func mapMachineToISA(machine string) string {
	m := map[string]string{
		"40":  "arm",     // EM_ARM
		"183": "aarch64", // EM_AARCH64
		"8":   "mips",    // EM_MIPS
		"3":   "x86",     // EM_386
		"62":  "x86_64",  // EM_X86_64
	}
	if v, ok := m[machine]; ok {
		return v
	}
	return ""
}
```

- [ ] **Step 2: Update detect.go — wire needsPhase2 gate and remove stub**

In `internal/detect/detect.go`, replace the stub `runPhase2` with the real import-dependent call. The `Detect` function already calls `runPhase2` — update the function body to add the gate:

Replace the `runPhase2` stub at the bottom of detect.go:

```go
// Remove the temporary stub:
//   func runPhase2(fp *Fingerprint, rm *scout.RemoteFile, verbose bool) error { return nil }
```

The real `runPhase2` is now in phase2.go (created in Step 1). No action needed in detect.go — just delete the stub.

- [ ] **Step 3: Build — will fail due to missing helpers package**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && go build ./...
```

Expected: `package github.com/krabiswabbie/busyscout/internal/helpers is not in std` — expected, Task 6 creates it.

- [ ] **Step 4: Commit**

```bash
git add internal/detect/phase2.go internal/detect/detect.go
git commit -m "feat(detect): Phase 2 — helper upload and execution"
```

---

### Task 6: Helper C program + Go embed

**Files:**
- Create: `internal/helpers/elfreader.c`
- Create: `internal/helpers/helpers.go`
- Create: `internal/helpers/bin/` (directory, initially empty until helpers are cross-compiled)

**Interfaces:**
- Produces: `func HelperForISA(isa string) ([]byte, error)` — returns embedded binary for ISA family

- [ ] **Step 1: Create the C helper source**

```c
// internal/helpers/elfreader.c
// Tiny static ELF reader — reads /bin/busybox header and prints arch info.
// Build: CC=arm-linux-gnueabi-gcc -static -s -Os -o elfreader-arm elfreader.c
//        strip elfreader-arm

#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <fcntl.h>
#include <unistd.h>
#include <elf.h>

static void print_field(const char *key, const char *value) {
    printf("%s=%s\n", key, value);
}

int main(int argc, char **argv) {
    const char *path = "/bin/busybox";
    if (argc > 1) path = argv[1];

    int fd = open(path, O_RDONLY);
    if (fd < 0) { perror("open"); return 1; }

    // Read first 64 bytes (covers both 32-bit and 64-bit ELF headers)
    unsigned char buf[64];
    ssize_t n = read(fd, buf, sizeof(buf));
    close(fd);
    if (n < 20) { fprintf(stderr, "short read\n"); return 1; }

    // Check ELF magic
    if (buf[0] != 0x7f || buf[1] != 'E' || buf[2] != 'L' || buf[3] != 'F') {
        fprintf(stderr, "not an ELF file\n");
        return 1;
    }

    // EI_CLASS
    if (buf[4] == 1) print_field("class", "32");
    else if (buf[4] == 2) print_field("class", "64");

    // EI_DATA
    if (buf[5] == 1) print_field("endian", "little");
    else if (buf[5] == 2) print_field("endian", "big");

    // e_machine (bytes 18-19)
    unsigned short machine = buf[18] | (buf[19] << 8);
    char machine_str[16];
    snprintf(machine_str, sizeof(machine_str), "%u", machine);
    print_field("machine", machine_str);

    // ARM attributes section: parse section headers for .ARM.attributes
    // We need e_shoff to find section header table
    unsigned long shoff;
    unsigned short shentsize, shnum, shstrndx;

    if (buf[4] == 1) { // 32-bit
        shoff     = *(unsigned int *)(buf + 32);
        shentsize = *(unsigned short *)(buf + 46);
        shnum     = *(unsigned short *)(buf + 48);
        shstrndx  = *(unsigned short *)(buf + 50);
    } else { // 64-bit
        shoff     = *(unsigned long *)(buf + 40);
        shentsize = *(unsigned short *)(buf + 58);
        shnum     = *(unsigned short *)(buf + 60);
        shstrndx  = *(unsigned short *)(buf + 62);
    }

    // Read section name string table header
    if (shnum == 0 || shentsize == 0) return 0; // stripped, no sections

    lseek(fd, shoff + (unsigned long)shstrndx * shentsize, SEEK_SET);
    unsigned char shstrtab_hdr[64];
    read(fd, shstrtab_hdr, sizeof(shstrtab_hdr));
    unsigned long shstrtab_off;
    if (buf[4] == 1)
        shstrtab_off = *(unsigned int *)(shstrtab_hdr + 16);
    else
        shstrtab_off = *(unsigned long *)(shstrtab_hdr + 24);

    // Scan section headers for .ARM.attributes
    for (unsigned short i = 0; i < shnum; i++) {
        lseek(fd, shoff + (unsigned long)i * shentsize, SEEK_SET);
        unsigned char shdr[64];
        read(fd, shdr, sizeof(shdr));

        unsigned int sh_name = *(unsigned int *)shdr;

        // Read section name
        char namebuf[32];
        lseek(fd, shstrtab_off + sh_name, SEEK_SET);
        read(fd, namebuf, sizeof(namebuf) - 1);
        namebuf[sizeof(namebuf) - 1] = '\0';

        if (strcmp(namebuf, ".ARM.attributes") != 0) continue;

        // Found it — get offset and size
        unsigned long attr_off, attr_size;
        if (buf[4] == 1) {
            attr_off  = *(unsigned int *)(shdr + 16);
            attr_size = *(unsigned int *)(shdr + 20);
        } else {
            attr_off  = *(unsigned long *)(shdr + 24);
            attr_size = *(unsigned long *)(shdr + 32);
        }

        // Read the section
        unsigned char *attr = malloc(attr_size);
        if (!attr) break;
        lseek(fd, attr_off, SEEK_SET);
        read(fd, attr, attr_size);

        // Parse ARM build attributes (simplified: scan for known tags)
        // Section format: 'A' version length "aeabi" subsections...
        // We scan for Tag_CPU_arch (6) and Tag_ABI_VFP_args (28) = 0x1c
        char *p = (char *)attr;
        char *end = p + attr_size;
        while (p + 2 < end) {
            int tag = (unsigned char)p[0];
            int len = 0;
            // ULEB128 length
            if (p[1] < 0x80) len = p[1];
            else if (p[1] < 0xc0) len = ((p[1] & 0x7f) << 7) | (p[2] & 0x7f);
            else break;

            if (p + 2 + len > end) break;

            switch (tag) {
            case 6: { // Tag_CPU_arch
                int arch = (unsigned char)p[2]; // uleb128, typically small
                char buf[8];
                snprintf(buf, sizeof(buf), "v%d", arch);
                print_field("cpu_arch", buf);
                break;
            }
            case 28: { // Tag_ABI_VFP_args
                int vfp = (unsigned char)p[2];
                print_field("float_abi", vfp == 1 ? "hard" : "soft");
                break;
            }
            }

            p += 2 + len;
        }

        free(attr);
        break;
    }

    return 0;
}
```

- [ ] **Step 2: Create helpers.go with embed**

```go
// internal/helpers/helpers.go
package helpers

import (
	_ "embed"
	"errors"
)

//go:embed bin/elfreader-arm
var elfreaderARM []byte

//go:embed bin/elfreader-aarch64
var elfreaderAARCH64 []byte

//go:embed bin/elfreader-mipsel
var elfreaderMIPSEL []byte

//go:embed bin/elfreader-mips
var elfreaderMIPS []byte

//go:embed bin/elfreader-x86
var elfreaderX86 []byte

//go:embed bin/elfreader-x86_64
var elfreaderX8664 []byte

// HelperForISA returns the embedded helper binary for the given ISA family.
func HelperForISA(isa string) ([]byte, error) {
	switch isa {
	case "arm":
		return elfreaderARM, nil
	case "aarch64":
		return elfreaderAARCH64, nil
	case "mips":
		// Big-endian MIPS — the user may need to upload mips version
		return elfreaderMIPS, nil
	case "x86":
		return elfreaderX86, nil
	case "x86_64":
		return elfreaderX8664, nil
	default:
		return nil, errors.New("unsupported ISA: " + isa)
	}
}

// HelperForISALE returns the little-endian variant for the given ISA.
func HelperForISALE(isa string) ([]byte, error) {
	switch isa {
	case "mips":
		return elfreaderMIPSEL, nil
	default:
		return HelperForISA(isa)
	}
}
```

- [ ] **Step 3: Create placeholder binaries for compilation**

For now, create empty placeholder files so `go build` works. The real binaries will be cross-compiled in Task 8.

```bash
mkdir -p internal/helpers/bin
touch internal/helpers/bin/elfreader-arm
touch internal/helpers/bin/elfreader-aarch64
touch internal/helpers/bin/elfreader-mipsel
touch internal/helpers/bin/elfreader-mips
touch internal/helpers/bin/elfreader-x86
touch internal/helpers/bin/elfreader-x86_64
```

- [ ] **Step 4: Update phase2.go to handle MIPS endianness**

In `internal/detect/phase2.go`, update `runPhase2` to select the right MIPS helper:

```go
	helperData, err := helpers.HelperForISA(fp.ISA)
	if err != nil {
		return errorx.Decorate(err, "no helper available for detected ISA")
	}
```

Replace with:

```go
	var helperData []byte
	// For MIPS, select BE or LE helper based on detected endianness
	if fp.ISA == "mips" && fp.Endianness == "little" {
		helperData, err = helpers.HelperForISALE(fp.ISA)
	} else {
		helperData, err = helpers.HelperForISA(fp.ISA)
	}
	if err != nil {
		return errorx.Decorate(err, "no helper available for detected ISA")
	}
```

- [ ] **Step 5: Verify build compiles**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && go build ./...
```

Expected: build succeeds (with empty placeholder binaries).

- [ ] **Step 6: Commit**

```bash
git add internal/helpers/
git add internal/detect/phase2.go
git commit -m "feat(helpers): elfreader C source, Go embed, placeholder binaries"
```

---

### Task 7: Tests

**Files:**
- Create: `internal/detect/phase1_test.go`
- Create: `internal/detect/phase2_test.go`
- Modify: `tests/docker-compose.yaml` (integration test setup)
- Create: `tests/Dockerfile.test` (camera simulator image)

- [ ] **Step 1: Create phase1_test.go**

```go
// internal/detect/phase1_test.go
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
		name    string
		output  string
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
```

- [ ] **Step 2: Create phase2_test.go**

```go
// internal/detect/phase2_test.go
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
```

- [ ] **Step 3: Run unit tests**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && go test ./internal/detect/... -v
```

Expected: all unit tests pass.

- [ ] **Step 4: Run full test suite**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && go test ./... -v
```

Expected: all tests pass (files_test.go + new detect tests).

- [ ] **Step 5: Commit**

```bash
git add internal/detect/phase1_test.go internal/detect/phase2_test.go
git commit -m "test(detect): unit tests for phase1/phase2 parsing"
```

---

### Task 8: Makefile — helper cross-compilation

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Add `helpers` and `helpers-clean` targets**

```makefile
# internal/helpers/elfreader.c
HELPER_SRC=internal/helpers/elfreader.c
HELPER_BIN_DIR=internal/helpers/bin

helpers:
	mkdir -p $(HELPER_BIN_DIR)
	arm-linux-gnueabi-gcc -static -s -Os -o $(HELPER_BIN_DIR)/elfreader-arm $(HELPER_SRC)
	aarch64-linux-gnu-gcc -static -s -Os -o $(HELPER_BIN_DIR)/elfreader-aarch64 $(HELPER_SRC)
	mipsel-linux-gnu-gcc -static -s -Os -o $(HELPER_BIN_DIR)/elfreader-mipsel $(HELPER_SRC)
	mips-linux-gnu-gcc -static -s -Os -o $(HELPER_BIN_DIR)/elfreader-mips $(HELPER_SRC)
	gcc -static -s -Os -o $(HELPER_BIN_DIR)/elfreader-x86 $(HELPER_SRC)
	gcc -static -s -Os -o $(HELPER_BIN_DIR)/elfreader-x86_64 $(HELPER_SRC)
	@echo "Helpers built successfully"

helpers-clean:
	rm -f $(HELPER_BIN_DIR)/elfreader-*

.PHONY: helpers helpers-clean
```

- [ ] **Step 2: Add .gitignore for helper binaries**

```bash
echo "!internal/helpers/bin/elfreader-*" >> .gitignore
```

Actually, we want to COMMIT the binaries. So they should NOT be gitignored.

- [ ] **Step 3: Verify Makefile syntax**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && make -n helpers
```

Expected: prints the commands without executing (cross-compilers may not be installed locally).

- [ ] **Step 4: Commit**

```bash
git add Makefile
git commit -m "build: add helpers cross-compilation target to Makefile"
```

---

### Final Verification

After all tasks are complete:

```bash
cd /Users/eafilin/Home/ipeye/busyscout
go build ./...           # must succeed
go test ./... -v         # all tests pass
go vet ./...             # no warnings
```
