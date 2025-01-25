package main

import (
	"fmt"
	"github.com/krabiswabbie/busyscout/internal/scout"
	"k8s.io/klog/v2"
	"os"
)

func main() {
	argsCount := len(os.Args)
	if argsCount < 3 || argsCount > 4 || argsCount == 4 && os.Args[3] != "--verbose" {
		fmt.Println("Usage:   ./busyscout local_file remote_path [--verbose]")
		fmt.Println("Example: ./busyscout ipwiz.zip root:12345@192.168.10.18:/tmp")
		os.Exit(0)
	}

	// Extract the source and target file paths from command line arguments
	sourceFile := os.Args[1]
	targetFile := os.Args[2]
	verboseFlag := argsCount == 4 && os.Args[3] == "--verbose"

	s, errNew := scout.New(sourceFile, targetFile, verboseFlag)
	if errNew != nil {
		klog.Fatal(errNew)
	}

	if errPush := s.Push(); errPush != nil {
		klog.Fatal(errPush)
	}
}
