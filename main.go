package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/krabiswabbie/busyscout/internal/detect"
	"github.com/krabiswabbie/busyscout/internal/scout"
	"k8s.io/klog/v2"
)

var Version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	subcmd := os.Args[1]
	switch subcmd {
	case "push":
		cmdPush(os.Args[2:])
	case "detect":
		cmdDetect(os.Args[2:])
	default:
		// Backward compat: if first arg looks like a file, treat as push
		if len(os.Args) >= 3 && !strings.HasPrefix(os.Args[1], "-") {
			cmdPush(os.Args[1:])
		} else {
			printUsage()
			os.Exit(0)
		}
	}
}

func printUsage() {
	fmt.Printf("busyscout %s\n", Version)
	fmt.Println("Usage:")
	fmt.Println("  busyscout push   <local_file> <remote_target> [--verbose]")
	fmt.Println("  busyscout detect <remote_target> [--verbose]")
	fmt.Println()
	fmt.Println("Remote target format: user:pass@host:port:/path")
	fmt.Println("Examples:")
	fmt.Println("  busyscout push firmware.bin admin:12345@192.168.1.100:/tmp")
	fmt.Println("  busyscout detect admin:12345@192.168.1.100:/tmp --verbose")
}

func cmdPush(args []string) {
	argsCount := len(args)
	if argsCount < 2 || argsCount > 3 || argsCount == 3 && args[2] != "--verbose" {
		fmt.Printf("busyscout %s\n", Version)
		fmt.Println("Usage:   busyscout push <local_file> <remote_target> [--verbose]")
		fmt.Println("Example: busyscout push ipwiz.zip root:12345@192.168.10.18:/tmp")
		os.Exit(0)
	}

	sourceFile := args[0]
	targetFile := args[1]
	verboseFlag := argsCount == 3 && args[2] == "--verbose"

	s, errNew := scout.New(sourceFile, targetFile, verboseFlag)
	if errNew != nil {
		klog.Fatal(errNew)
	}

	if errPush := s.Push(); errPush != nil {
		klog.Fatal(errPush)
	}
}

func cmdDetect(args []string) {
	argsCount := len(args)
	if argsCount < 1 || argsCount > 2 || argsCount == 2 && args[1] != "--verbose" {
		fmt.Printf("busyscout %s\n", Version)
		fmt.Println("Usage:   busyscout detect <remote_target> [--verbose]")
		fmt.Println("Example: busyscout detect admin:12345@192.168.10.18:/tmp")
		os.Exit(0)
	}

	target := args[0]
	verboseFlag := argsCount == 2 && args[1] == "--verbose"

	fp, errDetect := detect.Detect(target, verboseFlag)
	if errDetect != nil {
		klog.Fatal(errDetect)
	}

	fmt.Print(fp.Format())
}
