package xfer

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func compileFileloader(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("host C-helper tests require Linux daemon/socket semantics")
	}
	cc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("host C-helper tests require gcc")
	}
	path := filepath.Join(t.TempDir(), "fileloader")
	out, err := exec.Command(cc, "-std=c99", "-Wall", "-Wextra", "-Werror", "-O2", "-o", path, "../helpers/src/fileloader.c").CombinedOutput()
	if err != nil {
		t.Fatalf("compile helper: %v\n%s", err, out)
	}
	return path
}
func helperConnect(t *testing.T, loader, mode, dest string) net.Conn {
	t.Helper()
	port, ln, err := StartListener()
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ln.(*net.TCPListener).SetDeadline(time.Now().Add(3 * time.Second))
	if err := exec.Command(loader, mode, "127.0.0.1", itoa(port), dest).Run(); err != nil {
		t.Fatalf("launch: %v", err)
	}
	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	t.Cleanup(func() { conn.Close() })
	return conn
}
func helperPushFrame(t *testing.T, conn net.Conn, size uint64, data []byte) {
	t.Helper()
	var frame bytes.Buffer
	frame.WriteByte(1)
	binary.Write(&frame, binary.BigEndian, uint32(1))
	frame.WriteByte('x')
	binary.Write(&frame, binary.BigEndian, size)
	frame.Write(data)
	if _, err := conn.Write(frame.Bytes()); err != nil {
		t.Fatal(err)
	}
}
func readHelperReply(conn net.Conn) (byte, string, error) {
	var typ [1]byte
	if _, err := io.ReadFull(conn, typ[:]); err != nil {
		return 0, "", err
	}
	if typ[0] != 4 {
		return typ[0], "", nil
	}
	var n uint32
	if err := binary.Read(conn, binary.BigEndian, &n); err != nil {
		return typ[0], "", err
	}
	if n > 4096 {
		return typ[0], "", io.ErrShortBuffer
	}
	msg := make([]byte, n)
	_, err := io.ReadFull(conn, msg)
	return typ[0], string(msg), err
}
func TestFileloaderHostCompletion(t *testing.T) {
	loader := compileFileloader(t)
	t.Run("successful empty repeated overwrite", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "target")
		for _, data := range [][]byte{[]byte("first larger value"), []byte("second"), nil, []byte("again")} {
			conn := helperConnect(t, loader, "push", dst)
			helperPushFrame(t, conn, uint64(len(data)), data)
			typ, msg, err := readHelperReply(conn)
			conn.Close()
			if err != nil || typ != 5 {
				t.Fatalf("want explicit OK, got type=%d msg=%q err=%v", typ, msg, err)
			}
			got, err := os.ReadFile(dst)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("contents %q, want %q: %v", got, data, err)
			}
		}
	})
	for _, tt := range []struct {
		name, dest, diagnostic string
		size                   uint64
		data                   []byte
		truncated              bool
	}{
		{"forbidden destination", t.TempDir(), "open output", 4, []byte("data"), false},
		{"write failure", "/dev/full", "write output", 4, []byte("data"), false},
		{"truncated input", filepath.Join(t.TempDir(), "partial"), "read data", 4, []byte("ab"), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			conn := helperConnect(t, loader, "push", tt.dest)
			helperPushFrame(t, conn, tt.size, tt.data)
			if tt.truncated {
				conn.(*net.TCPConn).CloseWrite()
			}
			typ, msg, err := readHelperReply(conn)
			if err != nil || typ != 4 || !strings.Contains(msg, tt.diagnostic) {
				t.Fatalf("want diagnostic %q, got type=%d msg=%q err=%v", tt.diagnostic, typ, msg, err)
			}
		})
	}
	t.Run("pull compatibility", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "source")
		want := []byte("pull stays compatible")
		os.WriteFile(dst, want, 0600)
		conn := helperConnect(t, loader, "pull", dst)
		var typ [1]byte
		io.ReadFull(conn, typ[:])
		if typ[0] != 2 {
			t.Fatalf("want PULL, got %d", typ[0])
		}
		var n uint32
		binary.Read(conn, binary.BigEndian, &n)
		io.CopyN(io.Discard, conn, int64(n))
		io.ReadFull(conn, typ[:])
		if typ[0] != 3 {
			t.Fatalf("want DATA, got %d", typ[0])
		}
		var size uint64
		binary.Read(conn, binary.BigEndian, &size)
		got := make([]byte, size)
		_, err := io.ReadFull(conn, got)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("pull %q: %v", got, err)
		}
	})
}

func TestFileloaderHostLargePayloadFailure(t *testing.T) {
	loader := compileFileloader(t)
	for _, tt := range []struct{ name, dest, cause string }{
		{"open refused", t.TempDir(), "open output"},
		{"write refused", "/dev/full", "No space left on device"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			port, ln, err := StartListener()
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			src := filepath.Join(t.TempDir(), "large")
			os.WriteFile(src, make([]byte, 16<<20), 0600)
			if err := exec.Command(loader, "push", "127.0.0.1", itoa(port), tt.dest).Run(); err != nil {
				t.Fatal(err)
			}
			err = AcceptAndPush(ln, src)
			if err == nil || !strings.Contains(err.Error(), tt.cause) || isSetupError(err) {
				t.Fatalf("lost real C diagnostic %q: %v", tt.cause, err)
			}
		})
	}
}

func TestFileloaderHostCloseFailure(t *testing.T) {
	// Inject the close syscall failure at the actual C-helper boundary. Removing
	// its close-result check must change the reply from ERROR to an incorrect OK.
	loader := compileFileloader(t)
	dir := t.TempDir()
	dest := filepath.Join(dir, "target")
	source := filepath.Join(dir, "close_failure.c")
	shim := filepath.Join(dir, "close_failure.so")
	code := `#define _GNU_SOURCE
#include <dlfcn.h>
#include <unistd.h>
#include <stdlib.h>
#include <stdio.h>
#include <string.h>
#include <errno.h>
int close(int fd) {
 int (*real_close)(int)=dlsym(RTLD_NEXT,"close");
 char link[64],path[4096]; snprintf(link,sizeof(link),"/proc/self/fd/%d",fd);
 ssize_t n=readlink(link,path,sizeof(path)-1); if(n>=0)path[n]=0;
 const char *target=getenv("BS_CLOSE_FAIL"); int fail=n>=0 && target && strcmp(path,target)==0;
 int rc=real_close(fd); if(fail){errno=ENOSPC;return -1;} return rc;
}`
	if err := os.WriteFile(source, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("gcc", "-shared", "-fPIC", "-o", shim, source, "-ldl").CombinedOutput(); err != nil {
		t.Fatalf("compile close shim: %v %s", err, out)
	}
	t.Setenv("LD_PRELOAD", shim)
	t.Setenv("BS_CLOSE_FAIL", dest)
	conn := helperConnect(t, loader, "push", dest)
	helperPushFrame(t, conn, 4, []byte("data"))
	typ, msg, err := readHelperReply(conn)
	if err != nil || typ != 4 || !strings.Contains(msg, "close output: No space left on device") {
		t.Fatalf("lost close cause or false success: type=%d msg=%q err=%v", typ, msg, err)
	}
}
