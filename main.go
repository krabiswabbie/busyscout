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
  busyscout push <local> user:pass@host[:port]/path [--verbose]
  busyscout pull user:pass@host[:port]/path <local> [--verbose]
  busyscout detect user:pass@host[:port]/path [--verbose]

Mode selection is automatic:
  Same subnet → fast TCP (~6-8 KB loader + line-speed transfer)
  Different subnet → printf over telnet (slower but NAT-safe)`)
}

func cmdPush() {
	args := flag.NewFlagSet("push", flag.ExitOnError)
	verbose := args.Bool("verbose", false, "verbose output")
	args.Parse(os.Args[2:])

	if args.NArg() < 2 {
		fmt.Println("Usage: busyscout push <local> user:pass@host[:port]/path [--verbose]")
		os.Exit(1)
	}

	s, err := scout.New(args.Arg(0), args.Arg(1), *verbose)
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
	args := flag.NewFlagSet("pull", flag.ExitOnError)
	verbose := args.Bool("verbose", false, "verbose output")
	args.Parse(os.Args[2:])

	if args.NArg() < 2 {
		fmt.Println("Usage: busyscout pull user:pass@host[:port]/path <local> [--verbose]")
		os.Exit(1)
	}

	s, err := scout.NewPull(args.Arg(0), *verbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := s.Pull(args.Arg(1)); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func cmdDetect() {
	args := flag.NewFlagSet("detect", flag.ExitOnError)
	verbose := args.Bool("verbose", false, "verbose output")
	args.Parse(os.Args[2:])

	if args.NArg() < 1 {
		fmt.Println("Usage: busyscout detect user:pass@host[:port]/path [--verbose]")
		os.Exit(1)
	}

	target := args.Arg(0)

	fp, errDetect := detect.Detect(target, *verbose)
	if errDetect != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", errDetect)
		os.Exit(1)
	}

	fmt.Print(fp.Format())
}

func cmdLegacyPush() {
	// Legacy: busyscout <file> <remote> [--verbose]
	s, err := scout.New(os.Args[1], os.Args[2], len(os.Args) > 3 && os.Args[3] == "--verbose")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if err := s.Push(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
