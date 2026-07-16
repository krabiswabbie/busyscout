// internal/helpers/fileloader.go
package helpers

import (
	_ "embed"
	"errors"
)

//go:embed bin/fileloader-arm-uclibc
var fileloaderARMUclibc []byte

//go:embed bin/fileloader-arm-glibc
var fileloaderARMGlibc []byte

//go:embed bin/fileloader-arm-musl
var fileloaderARMMusl []byte

//go:embed bin/fileloader-aarch64-glibc
var fileloaderAARCH64Glibc []byte

//go:embed bin/fileloader-mipsel-uclibc
var fileloaderMIPSELUclibc []byte

//go:embed bin/fileloader-mips-uclibc
var fileloaderMIPSUclibc []byte

//go:embed bin/fileloader-x86-glibc
var fileloaderX86Glibc []byte

//go:embed bin/fileloader-x86_64-glibc
var fileloaderX8664Glibc []byte

// FileloaderForISALE returns the little-endian MIPS fileloader for the given ISA and libc.
// For non-MIPS ISAs, delegates to FileloaderForISA.
func FileloaderForISALE(isa, libc string) ([]byte, error) {
	if isa == "mips" {
		return fileloaderMIPSELUclibc, nil
	}
	return FileloaderForISA(isa, libc)
}

// FileloaderForISA returns the embedded fileloader binary for the given ISA and libc family.
func FileloaderForISA(isa, libc string) ([]byte, error) {
	libcNorm := normalizeLibc(libc)

	switch isa {
	case "arm":
		switch libcNorm {
		case "uclibc":
			return fileloaderARMUclibc, nil
		case "musl":
			return fileloaderARMMusl, nil
		default:
			return fileloaderARMGlibc, nil
		}
	case "aarch64":
		return fileloaderAARCH64Glibc, nil
	case "mips":
		return fileloaderMIPSUclibc, nil
	case "mipsel":
		return fileloaderMIPSELUclibc, nil
	case "x86":
		return fileloaderX86Glibc, nil
	case "x86_64":
		return fileloaderX8664Glibc, nil
	default:
		return nil, errors.New("unsupported ISA: " + isa)
	}
}
