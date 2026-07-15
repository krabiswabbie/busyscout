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
	odOut, _ := tc.Execute("sh -c 'od -An -t x1 -N20 /bin/busybox 2>/dev/null'")
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
			_ = features // used for future feature extraction
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
	case strings.Contains(lower, "aarch64"):
		fp.ISA = "aarch64"
	case strings.Contains(lower, "arm"):
		fp.ISA = "arm"
		// Try to extract version
		re := regexp.MustCompile(`(?i)ARMv(\d+)`)
		if m := re.FindStringSubmatch(output); m != nil {
			fp.ARMSubArch = "v" + m[1]
		}
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
	if len(fields) < 20 {
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

	// Bytes 18-19: e_machine (fields[18] = byte 18, fields[19] = byte 19)
	// e_machine is a 16-bit field at ELF offset 18 (0-indexed)
	if fp.Endianness == "little" {
		em := fields[18] + fields[19] // LE: low byte first (fields[18]=low, fields[19]=high)
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
	} else if fp.Endianness == "big" {
		em := fields[18] + fields[19] // BE: high byte first
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
