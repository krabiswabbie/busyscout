package main

import (
	"flag"
	"io"
	"reflect"
	"testing"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantPos     []string
		wantVerbose bool
		wantMode    string
	}{
		{
			name:     "no flags",
			args:     []string{"file", "host"},
			wantPos:  []string{"file", "host"},
			wantMode: "auto",
		},
		{
			name:        "flags after positionals",
			args:        []string{"file", "host", "--verbose", "--mode=printf"},
			wantPos:     []string{"file", "host"},
			wantVerbose: true,
			wantMode:    "printf",
		},
		{
			name:     "flags before positionals",
			args:     []string{"--mode", "fast", "file", "host"},
			wantPos:  []string{"file", "host"},
			wantMode: "fast",
		},
		{
			name:        "flag between positionals",
			args:        []string{"file", "--verbose", "host"},
			wantPos:     []string{"file", "host"},
			wantVerbose: true,
			wantMode:    "auto",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("push", flag.ContinueOnError)
			verbose := fs.Bool("verbose", false, "")
			mode := fs.String("mode", "auto", "")

			pos, err := parseArgs(fs, tt.args)
			if err != nil {
				t.Fatalf("parseArgs: %v", err)
			}
			if !reflect.DeepEqual(pos, tt.wantPos) {
				t.Errorf("positional: got %q, want %q", pos, tt.wantPos)
			}
			if *verbose != tt.wantVerbose {
				t.Errorf("verbose: got %v, want %v", *verbose, tt.wantVerbose)
			}
			if *mode != tt.wantMode {
				t.Errorf("mode: got %q, want %q", *mode, tt.wantMode)
			}
		})
	}
}

func TestParseArgs_UnknownFlag(t *testing.T) {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	if _, err := parseArgs(fs, []string{"file", "host", "--bogus"}); err == nil {
		t.Fatal("expected an error for an unknown flag after positionals")
	}
}
