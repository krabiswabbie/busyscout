// internal/helpers/helpers.go
package helpers

import (
	_ "embed"
	"errors"
	"strings"
)

//go:embed bin/elfreader-arm-uclibc
var elfreaderARMUclibc []byte

//go:embed bin/elfreader-arm-glibc
var elfreaderARMGlibc []byte

//go:embed bin/elfreader-arm-musl
var elfreaderARMMusl []byte

//go:embed bin/elfreader-aarch64-glibc
var elfreaderAARCH64Glibc []byte

//go:embed bin/elfreader-mipsel-uclibc
var elfreaderMIPSELUclibc []byte

//go:embed bin/elfreader-mips-uclibc
var elfreaderMIPSUclibc []byte

//go:embed bin/elfreader-x86-glibc
var elfreaderX86Glibc []byte

//go:embed bin/elfreader-x86_64-glibc
var elfreaderX8664Glibc []byte

// HelperForISA returns the embedded helper binary for the given ISA and libc family.
// isa is the ISA string from Fingerprint (e.g. "arm", "mips").
// libc is the detected libc family ("glibc", "uClibc", "musl") — may be empty.
func HelperForISA(isa, libc string) ([]byte, error) {
	// Normalize libc
	libcNorm := normalizeLibc(libc)

	switch isa {
	case "arm":
		switch libcNorm {
		case "uclibc":
			return elfreaderARMUclibc, nil
		case "musl":
			return elfreaderARMMusl, nil
		default:
			return elfreaderARMGlibc, nil
		}
	case "aarch64":
		return elfreaderAARCH64Glibc, nil
	case "mips":
		return elfreaderMIPSUclibc, nil
	case "x86":
		return elfreaderX86Glibc, nil
	case "x86_64":
		return elfreaderX8664Glibc, nil
	default:
		return nil, errors.New("unsupported ISA: " + isa)
	}
}

// HelperForISALE returns the little-endian MIPS helper for the given libc.
func HelperForISALE(isa, libc string) ([]byte, error) {
	if isa == "mips" {
		return elfreaderMIPSELUclibc, nil
	}
	return HelperForISA(isa, libc)
}

func normalizeLibc(libc string) string {
	lower := strings.ToLower(libc)
	switch {
	case strings.Contains(lower, "uclibc"):
		return "uclibc"
	case strings.Contains(lower, "musl"):
		return "musl"
	case strings.Contains(lower, "glibc"):
		return "glibc"
	default:
		return ""
	}
}
