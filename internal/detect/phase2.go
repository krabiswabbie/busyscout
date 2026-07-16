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

	// Select helper: MIPS little-endian uses mipsel helper; libc-aware
	var (
		helperData []byte
		err        error
	)
	if fp.ISA == "mips" && fp.Endianness == "little" {
		helperData, err = helpers.HelperForISALE(fp.ISA, fp.Libc)
	} else {
		helperData, err = helpers.HelperForISA(fp.ISA, fp.Libc)
	}
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
	if err := helpers.UploadData(tc, helperData, helperRemotePath, helpers.DefaultLineSize); err != nil {
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
