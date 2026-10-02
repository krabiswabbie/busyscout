package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/krabiswabbie/busyscout/internal/detect"
	"github.com/krabiswabbie/busyscout/internal/scout"
)

var Version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]

	// Handle --help and --version
	switch cmd {
	case "--help", "-h":
		printUsage()
		return
	case "--version", "-v":
		fmt.Println("busyscout version", Version)
		return
	}

	// Parse command-specific flags
	switch cmd {
	case "push":
		cmdPush()
	case "pull":
		cmdPull()
	case "detect":
		cmdDetect()
	default:
		// Legacy format: busyscout <file> <remote> [--verbose]
		if len(os.Args) < 3 {
			printUsage()
			os.Exit(1)
		}
		cmdLegacyPush()
	}
}

func printUsage() {
	fmt.Println(`BusyScout — push/pull files to embedded devices (IP cameras, NVR) via telnet.

Usage:
  busyscout push <local> user:pass@host[:port][:/path] [--mode=<mode>] [--verbose]
  busyscout pull user:pass@host[:port]:/path <local> [--mode=<mode>] [--verbose]
  busyscout detect user:pass@host[:port] [--verbose]

Transfer modes:
  auto    Same subnet → fast TCP (~6-8 KB loader + line-speed transfer),
          with a fallback to printf if the loader cannot be used.
          Different subnet → printf over telnet (slower but NAT-safe).
          This is the default.
  fast    Fast TCP only, no fallback
  printf  printf over telnet only, no loader is uploaded`)
}

// transferArgs parses the arguments shared by push and pull
func transferArgs(name, usage string) (positional []string, mode scout.Mode, verbose bool) {
	args := flag.NewFlagSet(name, flag.ExitOnError)
	verboseFlag := args.Bool("verbose", false, "verbose output")
	modeFlag := args.String("mode", string(scout.ModeAuto), "transfer mode: auto, fast or printf")
	positional, _ = parseArgs(args, os.Args[2:])

	if len(positional) < 2 {
		fmt.Println(usage)
		os.Exit(1)
	}

	mode, err := scout.ParseMode(*modeFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	return positional, mode, *verboseFlag
}

func cmdPush() {
	args, mode, verbose := transferArgs("push",
		"Usage: busyscout push <local> user:pass@host[:port][:/path] [--mode=auto|fast|printf] [--verbose]")

	s, err := scout.New(args[0], args[1], mode, verbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := s.Push(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func cmdPull() {
	args, mode, verbose := transferArgs("pull",
		"Usage: busyscout pull user:pass@host[:port]:/path <local> [--mode=auto|fast|printf] [--verbose]")

	s, err := scout.NewPull(args[0], mode, verbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := s.Pull(args[1]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func cmdDetect() {
	args := flag.NewFlagSet("detect", flag.ExitOnError)
	verbose := args.Bool("verbose", false, "verbose output")
	positional, _ := parseArgs(args, os.Args[2:])

	if len(positional) < 1 {
		fmt.Println("Usage: busyscout detect user:pass@host[:port] [--verbose]")
		os.Exit(1)
	}

	target := positional[0]

	fp, errDetect := detect.Detect(target, *verbose)
	if errDetect != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", errDetect)
		os.Exit(1)
	}

	fmt.Print(fp.Format())
}

func cmdLegacyPush() {
	// Legacy: busyscout <file> <remote> [--verbose]
	s, err := scout.New(os.Args[1], os.Args[2], scout.ModeAuto, len(os.Args) > 3 && os.Args[3] == "--verbose")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if err := s.Push(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// parseArgs parses flags placed anywhere among the positional arguments and
// returns the positional ones. The flag package alone stops at the first
// positional argument, so flags given after it would be ignored.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string

	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}
