package detect

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/joomcode/errorx"
	"github.com/krabiswabbie/busyscout/internal/scout"
	"github.com/krabiswabbie/busyscout/internal/telnet"
	"k8s.io/klog/v2"
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

	MuslVersion string // "1.2.3" — only when libc is musl and the loader reported it
	TimeT       string // "32-bit" / "64-bit" — only when derivable with certainty

	// OS profile (all best-effort, may be empty)
	KernelVersion  string   // full uname -a
	KernelBuild    string   // /proc/version
	BusyBoxVersion string   // "v1.31.1"
	DeviceModel    string   // "HiSilicon hi3516ev200"
	LibcVersion    string   // "glibc 2.28" / "uClibc 0.9.33.2" / "musl 1.2.3"
	Uptime         string   // "3d 12:45"
	TotalRAM       string   // "128 MB"
	RootFSUsage    string   // "12M / 128M (9%)"
	Mounts         []string // ["/ : squashfs,ro", "/tmp : tmpfs,rw"]
	NetTools       []string // ["curl", "wget", "nc"]
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

	// Phase 2: Helper binary (only if needed, best-effort)
	if fp.needsPhase2() {
		if err := runPhase2(fp, rm, verbose); err != nil {
			// Best-effort: timeout or upload failure is not fatal.
			// Phase 1 + OS probe already collected what they could.
			klog.Warningf("phase 2 skipped: %v", err)
		}
	}

	// Derive toolchain hint
	fp.deriveToolchainHint()
	fp.deriveTimeT()

	return fp, nil
}

// deriveTimeT determines the width of time_t on the target.
//
// This is only stated when it can be established with certainty, because a
// wrong answer is worse than none: a binary built with the opposite time_t
// layout links and starts, then corrupts every struct carrying a timestamp.
//
// musl switched all 32-bit architectures to a 64-bit time_t in 1.2.0, so the
// musl version is the deciding fact there. glibc and uClibc keep a 32-bit
// time_t on 32-bit targets unless the application opts in via _TIME_BITS=64,
// which cannot be observed from the outside — those are left unset.
func (f *Fingerprint) deriveTimeT() {
	if f.WordSize == 64 {
		f.TimeT = "64-bit"
		return
	}
	if f.WordSize != 32 || f.MuslVersion == "" {
		return
	}
	if muslHasTime64(f.MuslVersion) {
		f.TimeT = "64-bit"
	} else {
		f.TimeT = "32-bit"
	}
}

// muslHasTime64 reports whether the given musl version uses a 64-bit time_t
// on 32-bit architectures. The switch landed in musl 1.2.0.
func muslHasTime64(version string) bool {
	return compareVersions(version, "1.2.0") >= 0
}

// compareVersions compares dot-separated numeric versions.
// Returns -1 if a < b, 0 if equal, 1 if a > b. Non-numeric components count as 0.
func compareVersions(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(as) {
			av, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bv, _ = strconv.Atoi(bs[i])
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
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

	libcLabel := f.LibcVersion
	if libcLabel == "" {
		libcLabel = f.Libc
	}
	if libcLabel != "" {
		b.WriteString(fmt.Sprintf("libc:             %s\n", libcLabel))
	} else {
		b.WriteString("libc:             [uncertain]\n")
	}

	if f.TimeT != "" {
		b.WriteString(fmt.Sprintf("time_t:           %s%s\n", f.TimeT, f.timeTBasis()))
	} else if f.WordSize == 32 && f.Libc == "musl" {
		b.WriteString("time_t:           [uncertain -- musl version unknown]\n")
	}

	if f.SoCHint != "" {
		b.WriteString(fmt.Sprintf("SoC:              %s\n", f.SoCHint))
	}

	if f.ToolchainHint != "" {
		b.WriteString(fmt.Sprintf("Toolchain:        %s%s\n", f.ToolchainHint, f.toolchainNote()))
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

// timeTBasis explains where the time_t width came from, when it came from
// something less obvious than the word size.
func (f *Fingerprint) timeTBasis() string {
	if f.WordSize != 32 || f.MuslVersion == "" {
		return ""
	}
	if muslHasTime64(f.MuslVersion) {
		return fmt.Sprintf(" (musl %s >= 1.2.0)", f.MuslVersion)
	}
	return fmt.Sprintf(" (musl %s < 1.2.0)", f.MuslVersion)
}

// toolchainNote warns about the musl time64 ABI split next to the toolchain
// triplet. The triplet alone (e.g. "armv7-linux-musleabihf") does not say which
// side of musl 1.2.0 the toolchain must sit on, and picking the wrong side
// produces a binary that links and runs but passes mangled timestamp structs.
func (f *Fingerprint) toolchainNote() string {
	if f.WordSize != 32 || f.Libc != "musl" {
		return ""
	}
	if f.MuslVersion == "" {
		return "  [musl version unknown -- verify it against the toolchain, time64 ABI split at 1.2.0]"
	}
	if muslHasTime64(f.MuslVersion) {
		return fmt.Sprintf("  [needs a time64 toolchain: musl >= 1.2.0, device has %s]", f.MuslVersion)
	}
	return fmt.Sprintf("  [needs a pre-time64 toolchain: musl < 1.2.0, device has %s]", f.MuslVersion)
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

