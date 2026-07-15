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
	case "x86":
		return fileloaderX86Glibc, nil
	case "x86_64":
		return fileloaderX8664Glibc, nil
	default:
		return nil, errors.New("unsupported ISA: " + isa)
	}
}
