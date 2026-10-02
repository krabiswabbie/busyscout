package scout

import (
	"errors"
	"testing"

	"github.com/krabiswabbie/busyscout/internal/xfer"
)

func TestParseMode(t *testing.T) {
	for _, name := range []string{"auto", "fast", "printf"} {
		mode, err := ParseMode(name)
		if err != nil {
			t.Errorf("ParseMode(%q): %v", name, err)
		}
		if string(mode) != name {
			t.Errorf("ParseMode(%q) = %q", name, mode)
		}
	}

	for _, name := range []string{"", "turbo", "PRINTF "} {
		if _, err := ParseMode(name); err == nil {
			t.Errorf("ParseMode(%q): expected an error", name)
		}
	}
}

func TestRunTransfer(t *testing.T) {
	errSetup := &xfer.SetupError{Err: errors.New("fileloader did not start")}
	errBroken := errors.New("connection reset halfway")
	errPrintf := errors.New("printf failed")

	tests := []struct {
		name       string
		mode       Mode
		sameSubnet bool
		fastErr    error
		printfErr  error

		wantFast   bool
		wantPrintf bool
		wantNotice bool
		wantErr    error
	}{
		{
			name:       "printf mode never touches the fast path",
			mode:       ModePrintf,
			sameSubnet: true,
			wantPrintf: true,
		},
		{
			name:       "fast mode",
			mode:       ModeFast,
			sameSubnet: true,
			wantFast:   true,
		},
		{
			name:       "fast mode does not fall back",
			mode:       ModeFast,
			sameSubnet: true,
			fastErr:    errSetup,
			wantFast:   true,
			wantErr:    errSetup,
		},
		{
			name:       "auto on another subnet goes straight to printf",
			mode:       ModeAuto,
			wantPrintf: true,
		},
		{
			name:       "auto on the same subnet",
			mode:       ModeAuto,
			sameSubnet: true,
			wantFast:   true,
		},
		{
			name:       "auto falls back when the fast path could not start",
			mode:       ModeAuto,
			sameSubnet: true,
			fastErr:    errSetup,
			wantFast:   true,
			wantPrintf: true,
			wantNotice: true,
		},
		{
			name:       "auto reports the printf error after a fallback",
			mode:       ModeAuto,
			sameSubnet: true,
			fastErr:    errSetup,
			printfErr:  errPrintf,
			wantFast:   true,
			wantPrintf: true,
			wantNotice: true,
			wantErr:    errPrintf,
		},
		{
			name:       "auto does not retry a transfer that broke halfway",
			mode:       ModeAuto,
			sameSubnet: true,
			fastErr:    errBroken,
			wantFast:   true,
			wantErr:    errBroken,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fastRan, printfRan, noticed bool

			err := runTransfer(tt.mode, tt.sameSubnet,
				func() error { fastRan = true; return tt.fastErr },
				func() error { printfRan = true; return tt.printfErr },
				func(cause error) { noticed = true },
			)

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("error: got %v, want %v", err, tt.wantErr)
			}
			if fastRan != tt.wantFast {
				t.Errorf("fast path ran: %v, want %v", fastRan, tt.wantFast)
			}
			if printfRan != tt.wantPrintf {
				t.Errorf("printf path ran: %v, want %v", printfRan, tt.wantPrintf)
			}
			if noticed != tt.wantNotice {
				t.Errorf("fallback notice: %v, want %v", noticed, tt.wantNotice)
			}
		})
	}
}

// The fast path needs the device to connect back, which is not expected to
// work across subnets: say so instead of timing out.
func TestRunTransfer_FastModeOnAnotherSubnet(t *testing.T) {
	var ran bool
	run := func() error { ran = true; return nil }

	err := runTransfer(ModeFast, false, run, run, func(error) {})
	if err == nil {
		t.Fatal("expected an error")
	}
	if ran {
		t.Fatal("no transfer must be attempted")
	}
}
