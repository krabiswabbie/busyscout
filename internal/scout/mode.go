package scout

import (
	"errors"
	"fmt"

	"github.com/krabiswabbie/busyscout/internal/xfer"
)

// Mode selects how the file is transferred
type Mode string

const (
	// ModeAuto tries the fast path on the same subnet and falls back to printf
	ModeAuto Mode = "auto"
	// ModeFast is the fileloader over reverse TCP, without a fallback
	ModeFast Mode = "fast"
	// ModePrintf is printf over telnet, no fileloader is uploaded
	ModePrintf Mode = "printf"
)

func ParseMode(name string) (Mode, error) {
	switch mode := Mode(name); mode {
	case ModeAuto, ModeFast, ModePrintf:
		return mode, nil
	}

	return "", fmt.Errorf("unknown transfer mode %q (expected auto, fast or printf)", name)
}

// runTransfer runs the fast and the printf transfer according to mode.
// notice is called with the cause before falling back to printf.
func runTransfer(mode Mode, sameSubnet bool, fast, printf func() error, notice func(cause error)) error {
	switch mode {
	case ModePrintf:
		return printf()
	case ModeFast:
		if !sameSubnet {
			return errors.New("fast mode requires the device to be on the same subnet")
		}
		return fast()
	}

	if !sameSubnet {
		return printf()
	}

	err := fast()

	// Only a transfer that has not started can be retried: a broken one
	// would fail the same way, or leave the user guessing what happened
	var setupErr *xfer.SetupError
	if !errors.As(err, &setupErr) {
		return err
	}

	notice(err)
	return printf()
}
