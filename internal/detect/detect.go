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

