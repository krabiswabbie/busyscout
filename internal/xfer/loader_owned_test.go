package xfer

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/krabiswabbie/busyscout/internal/telnet"
	"github.com/krabiswabbie/busyscout/internal/testutil"
)

func TestLoaderCheckedOwnedScratchAndLiteralInvocation(t *testing.T) {
	// Keep cleanup away from the legacy shared location. Do not redefine eval:
	// dash rejects functions named after special builtins before emitting a prompt.
	for _, tt := range []struct{ name, init, cause string }{
		{"success", "", ""},
		{"chmod refused", `chmod() { printf 'chmod refused\n' >&2; return 23; };`, "chmod refused"},
		{"invocation refused", `chmod() { command chmod -x "$2"; };`, "Permission denied"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			argsFile := filepath.Join(dir, "args")
			sideEffect := filepath.Join(dir, "injected")
			var mu sync.Mutex
			var dirs []string
			re := regexp.MustCompile(`/tmp/bs-loader-[0-9a-f]+`)
			init := `rm() { for arg do case "$arg" in /tmp/bs-loader) return 0;; esac; done; command rm "$@"; }; ` + tt.init
			host := testutil.StartShell(t, testutil.ShellOptions{Dir: dir, Init: init, OnCommand: func(cmd string) {
				mu.Lock()
				defer mu.Unlock()
				for _, d := range re.FindAllString(cmd, -1) {
					dirs = append(dirs, d)
				}
			}})
			h, p, _ := net.SplitHostPort(host)
			tc := &telnet.TelnetClient{Address: h, Port: p}
			if err := tc.Dial(); err != nil {
				t.Fatal(err)
			}
			defer tc.Close()
			remote := "literal ' $name; touch " + sideEffect + "; [*] \\ name"
			script := []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > " + telnet.ShellQuote(argsFile) + "\n")
			ln, err := runLoader(tc, script, "push", remote, "not-an-ip")
			if tt.cause == "" {
				if err != nil {
					t.Fatal(err)
				}
				ln.Close()
			} else {
				if err == nil {
					ln.Close()
					t.Fatal("setup operation failure ignored")
				}
				if !strings.Contains(err.Error(), tt.cause) {
					t.Fatalf("lost cause %q: %v", tt.cause, err)
				}
			}
			mu.Lock()
			observed := append([]string(nil), dirs...)
			mu.Unlock()
			if len(observed) == 0 {
				t.Fatal("no transfer-owned loader directory")
			}
			for _, d := range observed {
				if _, err := os.Lstat(d); !os.IsNotExist(err) {
					t.Errorf("loader resource remains %s: %v", d, err)
				}
			}
			if _, err := os.Stat(sideEffect); !os.IsNotExist(err) {
				t.Fatal("remote operand executed as shell source")
			}
			if tt.cause == "" {
				args, err := os.ReadFile(argsFile)
				if err != nil {
					t.Fatal(err)
				}
				lines := strings.Split(strings.TrimSuffix(string(args), "\n"), "\n")
				if len(lines) != 4 || lines[0] != "push" || lines[3] != remote {
					t.Fatalf("loader args not literal: %q", args)
				}
			}
		})
	}
}
func TestLoaderRejectsControlOperandsBeforeUpload(t *testing.T) {
	for _, path := range []string{"bad\nname", "bad\rname", "bad\x00name"} {
		t.Run(fmt.Sprintf("%q", path), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("invalid path reached transport: %v", r)
				}
			}()
			ln, err := runLoader(nil, []byte("x"), "push", path, "not-an-ip")
			if ln != nil {
				ln.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "unsupported shell path") {
				t.Fatalf("want rejected path, got %v", err)
			}
		})
	}
}

func TestLoaderDirectoriesArePrivateAndIndependent(t *testing.T) {
	// Reusing a process-wide location or dropping umask allows overlap to share
	// or expose the executable. Observe actual directories, not shell text.
	dir := t.TempDir()
	records := make(chan string, 32)
	re := regexp.MustCompile(`/tmp/bs-loader-[0-9a-f]+`)
	host := testutil.StartShell(t, testutil.ShellOptions{Dir: dir, OnCommand: func(cmd string) {
		if strings.Contains(cmd, "mkdir ") {
			for _, d := range re.FindAllString(cmd, -1) {
				records <- d
			}
		}
	}})
	h, p, _ := net.SplitHostPort(host)
	clients := make([]*telnet.TelnetClient, 2)
	listeners := make([]net.Listener, 2)
	paths := make([]string, 2)
	for i := range clients {
		clients[i] = &telnet.TelnetClient{Address: h, Port: p}
		if err := clients[i].Dial(); err != nil {
			t.Fatal(err)
		}
		defer clients[i].Close()
		var err error
		listeners[i], err = runLoader(clients[i], []byte("#!/bin/sh\nexit 0\n"), "push", "literal name", "not-an-ip")
		if err != nil {
			t.Fatal(err)
		}
		defer listeners[i].Close()
		paths[i] = <-records
		info, err := os.Stat(paths[i])
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("directory not private: %s %v %v", paths[i], info, err)
		}
	}
	if paths[0] == paths[1] {
		t.Fatal("loader invocations share a directory")
	}
	listeners[0].Close()
	if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
		t.Fatalf("first loader survived close: %v", err)
	}
	if _, err := os.Stat(paths[1] + "/fileloader"); err != nil {
		t.Fatalf("first cleanup removed second loader: %v", err)
	}
	listeners[1].Close()
	if _, err := os.Stat(paths[1]); !os.IsNotExist(err) {
		t.Fatalf("second loader survived close: %v", err)
	}
}

func TestLoaderSetupFailureCleanupIsScoped(t *testing.T) {
	for _, tt := range []struct {
		name, init, cause string
		owned             bool
	}{
		{"mkdir refused", `mkdir() { printf 'mkdir refused\n' >&2; return 27; };`, "mkdir refused", false},
		{"upload refused", `printf() { case "$1" in '\043\041'*) command printf 'upload refused\n' >&2; return 29;; esac; command printf "$@"; };`, "upload refused", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			re := regexp.MustCompile(`/tmp/bs-loader-[0-9a-f]+`)
			var mu sync.Mutex
			var observed []string
			var cleanup bool
			host := testutil.StartShell(t, testutil.ShellOptions{Dir: t.TempDir(), Init: tt.init, OnCommand: func(cmd string) {
				mu.Lock()
				defer mu.Unlock()
				observed = append(observed, re.FindAllString(cmd, -1)...)
				if strings.Contains(cmd, "rm -rf") {
					cleanup = true
				}
			}})
			h, p, _ := net.SplitHostPort(host)
			tc := &telnet.TelnetClient{Address: h, Port: p}
			if err := tc.Dial(); err != nil {
				t.Fatal(err)
			}
			defer tc.Close()
			ln, err := runLoader(tc, []byte("#!/bin/sh\nexit 0\n"), "push", "file", "not-an-ip")
			if ln != nil {
				ln.Close()
			}
			if err == nil || !strings.Contains(err.Error(), tt.cause) {
				t.Fatalf("lost setup failure %q: %v", tt.cause, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if cleanup != tt.owned {
				t.Fatalf("cleanup ownership: got %v, owned %v", cleanup, tt.owned)
			}
			for _, d := range observed {
				if _, err := os.Lstat(d); !os.IsNotExist(err) {
					t.Fatalf("scratch remains: %s: %v", d, err)
				}
			}
		})
	}
}
