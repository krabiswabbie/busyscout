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
