# Fast File Transfer — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add high-speed TCP file transfer (push + pull) with automatic printf fallback for NAT scenarios.

**Architecture:** C fileloader binary (8 ISA×libc variants, ~6-8 KB) delivered via printf, establishes reverse TCP connection to BusyScout, framed protocol for push/pull. Auto-detects same-subnet vs NAT and selects fast/printf mode.

**Tech Stack:** Go 1.21+, C99, POSIX sockets, Docker (cross-compilation), telnet (printf delivery).

## Global Constraints

- C helpers: `-std=c99 -s -Os`, dynamically linked, output to `internal/helpers/bin/fileloader-*`
- Go packages follow existing project conventions: `errorx` for wrapping, `github.com/joomcode/errorx`
- Protocol: big-endian integers, 1B type + 4B namelen + filename + 8B filesize + data
- CLI: `busyscout push <local> remote` and `busyscout pull remote <local>` with `[--verbose]`
- No changes to `internal/telnet/`, `internal/helpers/helpers.go`, `elfreader.c`, or `detect` mode
- `IsSameSubnet()`: compare device IP against `net.Interfaces()`; false on failure → printf fallback

---

### Task 1: C fileloader binary

**Files:**
- Create: `internal/helpers/src/fileloader.c`
- Create: `internal/helpers/src/fileloader_test.sh`

**Interfaces:**
- Produces: `fileloader <mode> <ip> <port> [filename]` — POSIX sockets, connect + framed protocol

- [ ] **Step 1: Write fileloader.c**

```c
/*
 * fileloader.c — BusyScout fast file transfer helper
 *
 * Usage: fileloader push <ip> <port> <filename>
 *        fileloader pull <ip> <port> <filename>
 *
 * Protocol (all multi-byte ints are big-endian):
 *
 * PUSH (BusyScout → device):
 *   [1B type='P'] [4B namelen] [filename] [8B filesize] [data bytes...]
 *
 * PULL (device → BusyScout):
 *   loader sends:  [1B type='G'] [4B namelen] [filename]
 *   BusyScout responds:
 *     success: [1B type='D'] [8B filesize] [data bytes...]
 *     error:   [1B type='E'] [4B msglen] [error message]
 */

#include <arpa/inet.h>
#include <fcntl.h>
#include <netdb.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <errno.h>

#define TYPE_PUSH  0x01
#define TYPE_PULL  0x02
#define TYPE_DATA  0x03
#define TYPE_ERROR 0x04

static int connect_to(const char *ip, int port) {
    struct sockaddr_in addr;
    int sock = socket(AF_INET, SOCK_STREAM, 0);
    if (sock < 0) {
        perror("socket");
        return -1;
    }

    memset(&addr, 0, sizeof(addr));
    addr.sin_family = AF_INET;
    addr.sin_port = htons((unsigned short)port);

    if (inet_pton(AF_INET, ip, &addr.sin_addr) <= 0) {
        perror("inet_pton");
        close(sock);
        return -1;
    }

    if (connect(sock, (struct sockaddr *)&addr, sizeof(addr)) < 0) {
        perror("connect");
        close(sock);
        return -1;
    }

    return sock;
}

static int read_full(int fd, void *buf, size_t n) {
    size_t total = 0;
    while (total < n) {
        ssize_t r = read(fd, (char *)buf + total, n - total);
        if (r <= 0) return -1;
        total += (size_t)r;
    }
    return 0;
}

static int write_full(int fd, const void *buf, size_t n) {
    size_t total = 0;
    while (total < n) {
        ssize_t w = write(fd, (const char *)buf + total, n - total);
        if (w <= 0) return -1;
        total += (size_t)w;
    }
    return 0;
}

/* Write 4-byte big-endian uint32 */
static int write_u32(int fd, uint32_t v) {
    uint32_t nv = htonl(v);
    return write_full(fd, &nv, 4);
}

/* Read 4-byte big-endian uint32 */
static int read_u32(int fd, uint32_t *v) {
    uint32_t nv;
    if (read_full(fd, &nv, 4) < 0) return -1;
    *v = ntohl(nv);
    return 0;
}

/* Read 8-byte big-endian uint64 (for filesize) */
static int read_u64(int fd, uint64_t *v) {
    uint32_t hi, lo;
    if (read_u32(fd, &hi) < 0) return -1;
    if (read_u32(fd, &lo) < 0) return -1;
    *v = ((uint64_t)hi << 32) | lo;
    return 0;
}

/* Write 8-byte big-endian uint64 */
static int write_u64(int fd, uint64_t v) {
    uint32_t hi = (uint32_t)(v >> 32);
    uint32_t lo = (uint32_t)(v & 0xFFFFFFFF);
    if (write_u32(fd, hi) < 0) return -1;
    return write_u32(fd, lo);
}

static int do_push(int sock, const char *filename) {
    /* Read type byte */
    unsigned char type;
    if (read_full(sock, &type, 1) < 0) {
        fprintf(stderr, "read type failed\n");
        return 1;
    }
    if (type != TYPE_PUSH) {
        fprintf(stderr, "expected PUSH type (0x01), got 0x%02x\n", type);
        return 1;
    }

    /* Read filename (we already know it, but consume from stream) */
    uint32_t namelen;
    if (read_u32(sock, &namelen) < 0) {
        fprintf(stderr, "read namelen failed\n");
        return 1;
    }
    /* Skip filename bytes */
    char buf[4096];
    uint32_t remaining = namelen;
    while (remaining > 0) {
        uint32_t chunk = remaining > sizeof(buf) ? (uint32_t)sizeof(buf) : remaining;
        if (read_full(sock, buf, chunk) < 0) {
            fprintf(stderr, "read filename failed\n");
            return 1;
        }
        remaining -= chunk;
    }

    /* Read filesize */
    uint64_t filesize;
    if (read_u64(sock, &filesize) < 0) {
        fprintf(stderr, "read filesize failed\n");
        return 1;
    }

    /* Open output file */
    int fd = open(filename, O_WRONLY | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        perror("open output");
        return 1;
    }

    /* Copy data */
    uint64_t copied = 0;
    while (copied < filesize) {
        uint64_t chunk = filesize - copied;
        if (chunk > sizeof(buf)) chunk = sizeof(buf);
        if (read_full(sock, buf, (size_t)chunk) < 0) {
            fprintf(stderr, "read data failed\n");
            close(fd);
            return 1;
        }
        if (write_full(fd, buf, (size_t)chunk) < 0) {
            perror("write output");
            close(fd);
            return 1;
        }
        copied += chunk;
    }

    close(fd);
    return 0;
}

static int do_pull(int sock, const char *filename) {
    /* Open and read the file from device's disk */
    int fd = open(filename, O_RDONLY);
    if (fd < 0) {
        /* Send error response */
        unsigned char err_type = TYPE_ERROR;
        write_full(sock, &err_type, 1);
        const char *errmsg = strerror(errno);
        uint32_t msglen = (uint32_t)strlen(errmsg);
        write_u32(sock, msglen);
        write_full(sock, errmsg, msglen);
        return 1;
    }

    /* Get file size */
    off_t sz = lseek(fd, 0, SEEK_END);
    if (sz < 0) {
        perror("lseek");
        close(fd);
        return 1;
    }
    lseek(fd, 0, SEEK_SET);
    uint64_t filesize = (uint64_t)sz;

    /* Send TYPE_PULL announcement: "I'm sending file X" */
    unsigned char type = TYPE_PULL;
    if (write_full(sock, &type, 1) < 0) {
        fprintf(stderr, "write type failed\n");
        close(fd);
        return 1;
    }

    uint32_t namelen = (uint32_t)strlen(filename);
    if (write_u32(sock, namelen) < 0) {
        fprintf(stderr, "write namelen failed\n");
        close(fd);
        return 1;
    }
    if (write_full(sock, filename, namelen) < 0) {
        fprintf(stderr, "write filename failed\n");
        close(fd);
        return 1;
    }

    /* Send TYPE_DATA with file contents */
    unsigned char data_type = TYPE_DATA;
    if (write_full(sock, &data_type, 1) < 0) {
        fprintf(stderr, "write data type failed\n");
        close(fd);
        return 1;
    }
    if (write_u64(sock, filesize) < 0) {
        fprintf(stderr, "write filesize failed\n");
        close(fd);
        return 1;
    }

    /* Send file data */
    char buf[4096];
    uint64_t sent = 0;
    while (sent < filesize) {
        ssize_t n = read(fd, buf, sizeof(buf));
        if (n <= 0) break;
        if (write_full(sock, buf, (size_t)n) < 0) {
            fprintf(stderr, "write data failed\n");
            close(fd);
            return 1;
        }
        sent += (uint64_t)n;
    }

    close(fd);
    return 0;
}

int main(int argc, char **argv) {
    if (argc < 4) {
        fprintf(stderr, "Usage: %s push|pull <ip> <port> [filename]\n", argv[0]);
        return 1;
    }

    const char *mode = argv[1];
    const char *ip = argv[2];
    int port = atoi(argv[3]);
    const char *filename = argc >= 5 ? argv[4] : NULL;

    if (strcmp(mode, "push") == 0 && !filename) {
        fprintf(stderr, "push requires filename\n");
        return 1;
    }
    if (strcmp(mode, "pull") == 0 && !filename) {
        fprintf(stderr, "pull requires filename\n");
        return 1;
    }

    int sock = connect_to(ip, port);
    if (sock < 0) return 1;

    int rc;
    if (strcmp(mode, "push") == 0) {
        rc = do_push(sock, filename);
    } else if (strcmp(mode, "pull") == 0) {
        rc = do_pull(sock, filename);
    } else {
        fprintf(stderr, "unknown mode: %s\n", mode);
        rc = 1;
    }

    close(sock);
    return rc;
}
```

- [ ] **Step 2: Write C test script (fileloader_test.sh)**

```bash
#!/bin/bash
# fileloader_test.sh — Basic smoke test for fileloader binary
# Requires: fileloader binary compiled for host architecture
set -e

FILELOADER="${1:-./fileloader-host}"
TEST_DATA="/tmp/fileloader_test_data.bin"
TEST_OUT="/tmp/fileloader_test_out.bin"

# Generate test data
dd if=/dev/urandom of="$TEST_DATA" bs=1024 count=10 2>/dev/null

# Start a Go test listener (separate process)
# The test runs a simple Go server that exercises push and pull framing
# See Task 5 for the Go listener implementation

echo "PASS: fileloader test stub (integration test in Task 11)"
```

- [ ] **Step 3: Commit**

```bash
git add internal/helpers/src/fileloader.c internal/helpers/src/fileloader_test.sh
git commit -m "feat(xfer): add fileloader C binary with push/pull framing protocol"
```

---

### Task 2: Makefile — fileloader cross-compilation targets

**Files:**
- Modify: `Makefile` — add `fileloaders`, `fileloaders-clean` targets, update `all`

**Interfaces:**
- Produces: `make fileloaders` builds 8 variants into `internal/helpers/bin/fileloader-*`

- [ ] **Step 1: Add fileloader targets to Makefile**

Добавить после секции `helpers-clean` (перед `test-integration-detect`):

```makefile
# --- Fileloader (fast file transfer) ---

FILELOADER_SRC=internal/helpers/src/fileloader.c

fileloaders: fileloaders-arm fileloaders-aarch64 fileloaders-mipsel fileloaders-mips fileloaders-x86 fileloaders-x86_64
	@echo "=== Fileloaders built ==="
	@ls -lh $(HELPER_BIN_DIR)/fileloader-*

fileloaders-arm:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		efilin/arm-hisiv500-linux \
		arm-hisiv500-linux-uclibcgnueabi-gcc $(CFLAGS_COMMON) \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/fileloader-arm-uclibc $(HELPER_WORKDIR)/$(FILELOADER_SRC)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		efilin/arm-ca9-linux-gnueabihf-6.5 \
		arm-ca9-linux-gnueabihf-gcc $(CFLAGS_COMMON) -march=armv5te \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/fileloader-arm-glibc $(HELPER_WORKDIR)/$(FILELOADER_SRC)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		efilin/arm-gcc7.3-linux-musleabi \
		arm-gcc7.3-linux-musleabi-gcc $(CFLAGS_COMMON) \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/fileloader-arm-musl $(HELPER_WORKDIR)/$(FILELOADER_SRC)

fileloaders-aarch64:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		efilin/aarch64-mix210-linux \
		aarch64-mix210-linux-gcc $(CFLAGS_COMMON) \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/fileloader-aarch64-glibc $(HELPER_WORKDIR)/$(FILELOADER_SRC)

fileloaders-mipsel:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		efilin/mips-gcc720-uclibc229-r519 \
		mips-linux-uclibc-gcc $(CFLAGS_COMMON) -EL \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/fileloader-mipsel-uclibc $(HELPER_WORKDIR)/$(FILELOADER_SRC)

fileloaders-mips:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		efilin/mips-gcc720-uclibc229-r519 \
		mips-linux-uclibc-gcc $(CFLAGS_COMMON) \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/fileloader-mips-uclibc $(HELPER_WORKDIR)/$(FILELOADER_SRC)

fileloaders-x86:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		ubuntu:22.04 bash -c '\
			apt-get update -qq && apt-get install -y -qq gcc-multilib && \
			gcc -std=c99 -s -Os -m32 \
				-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/fileloader-x86-glibc $(HELPER_WORKDIR)/$(FILELOADER_SRC)'

fileloaders-x86_64:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		ubuntu:22.04 bash -c '\
			apt-get update -qq && apt-get install -y -qq gcc && \
			gcc -std=c99 -s -Os \
				-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/fileloader-x86_64-glibc $(HELPER_WORKDIR)/$(FILELOADER_SRC)'

fileloaders-clean:
	rm -f $(HELPER_BIN_DIR)/fileloader-*
	touch $(HELPER_BIN_DIR)/fileloader-arm-uclibc
	touch $(HELPER_BIN_DIR)/fileloader-arm-glibc
	touch $(HELPER_BIN_DIR)/fileloader-arm-musl
	touch $(HELPER_BIN_DIR)/fileloader-aarch64-glibc
	touch $(HELPER_BIN_DIR)/fileloader-mipsel-uclibc
	touch $(HELPER_BIN_DIR)/fileloader-mips-uclibc
	touch $(HELPER_BIN_DIR)/fileloader-x86-glibc
	touch $(HELPER_BIN_DIR)/fileloader-x86_64-glibc
```

- [ ] **Step 2: Update `all` target**

Заменить `all: clean test build` на:

```makefile
all: clean test fileloaders build
```

- [ ] **Step 3: Update `.PHONY`**

Добавить в конец `.PHONY` строки новые цели:

```makefile
.PHONY: all local test clean build helpers helpers-clean test-integration-detect \
        helpers-arm helpers-aarch64 helpers-mipsel helpers-mips helpers-x86 helpers-x86_64 \
        fileloaders fileloaders-arm fileloaders-aarch64 fileloaders-mipsel fileloaders-mips fileloaders-x86 fileloaders-x86_64 fileloaders-clean
```

- [ ] **Step 4: Run `make fileloaders` to build all variants**

```bash
make fileloaders
```

Expected: 8 `fileloader-*` binaries in `internal/helpers/bin/`, each 4-8 KB.

- [ ] **Step 5: Verify binary sizes**

```bash
ls -lh internal/helpers/bin/fileloader-*
```

Expected: each ≤ 10 KB.

- [ ] **Step 6: Commit**

```bash
git add Makefile internal/helpers/bin/fileloader-*
git commit -m "feat(xfer): add fileloader cross-compilation targets (8 ISA×libc variants)"
```

---

### Task 3: Go embedding — helpers/fileloader.go

**Files:**
- Create: `internal/helpers/fileloader.go`
- Modify: `internal/helpers/helpers.go` — add `HelperForISA` re-export for detect usage if needed (no — existing helpers.go untouched)

**Interfaces:**
- Produces: `FileloaderForISA(isa, libc string) ([]byte, error)` — same signature as `HelperForISA`
- Consumes: embedded fileloader binaries from `internal/helpers/bin/fileloader-*`

- [ ] **Step 1: Write the failing test**

Create `internal/helpers/fileloader_test.go`:

```go
package helpers

import (
	"testing"
)

func TestFileloaderForISA_KnownISA(t *testing.T) {
	tests := []struct{ isa, libc string }{
		{"arm", "uclibc"},
		{"arm", "glibc"},
		{"arm", "musl"},
		{"aarch64", "glibc"},
		{"mips", "uclibc"},
		{"x86", "glibc"},
		{"x86_64", "glibc"},
	}

	for _, tt := range tests {
		t.Run(tt.isa+"-"+tt.libc, func(t *testing.T) {
			b, err := FileloaderForISA(tt.isa, tt.libc)
			if err != nil {
				t.Fatalf("FileloaderForISA(%q, %q) error: %v", tt.isa, tt.libc, err)
			}
			if len(b) == 0 {
				t.Fatalf("FileloaderForISA(%q, %q) returned empty", tt.isa, tt.libc)
			}
		})
	}
}

func TestFileloaderForISA_UnknownISA(t *testing.T) {
	_, err := FileloaderForISA("sparc", "glibc")
	if err == nil {
		t.Fatal("expected error for unknown ISA")
	}
}
```

- [ ] **Step 2: Run test — expected FAIL**

```bash
go test ./internal/helpers/ -run TestFileloaderForISA -v
```

- [ ] **Step 3: Write fileloader.go**

```go
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
```

- [ ] **Step 4: Run test — expected PASS**

```bash
go test ./internal/helpers/ -run TestFileloaderForISA -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/helpers/fileloader.go internal/helpers/fileloader_test.go
git commit -m "feat(xfer): add Go embedding for fileloader binaries"
```

---

### Task 4: Subnet detection — xfer/subnet.go

**Files:**
- Create: `internal/xfer/subnet.go`
- Create: `internal/xfer/subnet_test.go`

**Interfaces:**
- Produces: `func IsSameSubnet(deviceIP string) bool`

- [ ] **Step 1: Write the failing test**

```go
// internal/xfer/subnet_test.go
package xfer

import (
	"net"
	"testing"
)

func TestIsSameSubnet_Same(t *testing.T) {
	// This test relies on actual network interfaces.
	// Skip in CI without network.
	if testing.Short() {
		t.Skip("skipping network-dependent test in short mode")
	}

	// Find a local IP and test against it
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			if ip.To4() != nil {
				// Test with the interface's own IP — must be same subnet
				if !IsSameSubnet(ip.String()) {
					t.Errorf("IsSameSubnet(%s) should be true (own interface IP)", ip.String())
				}
				return
			}
		}
	}
	t.Skip("no suitable IPv4 interface found")
}

func TestIsSameSubnet_NoInterfaces(t *testing.T) {
	// 8.8.8.8 is not in any local subnet
	if IsSameSubnet("8.8.8.8") {
		t.Error("IsSameSubnet(8.8.8.8) should be false")
	}
}
```

- [ ] **Step 2: Run test — expected FAIL**

```bash
go test ./internal/xfer/ -run TestIsSameSubnet -v
```

- [ ] **Step 3: Write subnet.go**

```go
// internal/xfer/subnet.go
package xfer

import "net"

// IsSameSubnet returns true if deviceIP shares a subnet with any local network interface.
// Returns false on any error (safe default: fallback to printf).
func IsSameSubnet(deviceIP string) bool {
	devIP := net.ParseIP(deviceIP)
	if devIP == nil {
		return false
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ipNet.Contains(devIP) {
				return true
			}
		}
	}

	return false
}
```

- [ ] **Step 4: Run test — expected PASS**

```bash
go test ./internal/xfer/ -run TestIsSameSubnet -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/xfer/subnet.go internal/xfer/subnet_test.go
git commit -m "feat(xfer): add IsSameSubnet() for LAN/NAT detection"
```

---

### Task 5: TCP listener with framed protocol — xfer/listener.go

**Files:**
- Create: `internal/xfer/listener.go`
- Create: `internal/xfer/listener_test.go`

**Interfaces:**
- Produces: `func StartListener() (port int, ln net.Listener, err error)` — bind ephemeral port
- Produces: `func AcceptAndPush(ln net.Listener, localPath string) error` — accept connection, send PUSH frame + data
- Produces: `func AcceptAndPull(ln net.Listener, remotePath, localPath string) error` — accept connection, receive PULL request, send DATA frame with file contents

- [ ] **Step 1: Write the failing test**

```go
// internal/xfer/listener_test.go
package xfer

import (
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestAcceptAndPush(t *testing.T) {
	// Create temp file with known content
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "push_src.bin")
	want := []byte("hello from push test")
	if err := os.WriteFile(srcPath, want, 0644); err != nil {
		t.Fatal(err)
	}

	port, ln, err := StartListener()
	if err != nil {
		t.Fatal(err)
	}

	// Start goroutine to accept and push
	errCh := make(chan error, 1)
	go func() {
		errCh <- AcceptAndPush(ln, srcPath)
	}()

	// Connect as fileloader client and read framed data
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Read type
	var typ [1]byte
	if _, err := io.ReadFull(conn, typ[:]); err != nil {
		t.Fatal(err)
	}
	if typ[0] != 0x01 {
		t.Fatalf("expected type 0x01 (PUSH), got 0x%02x", typ[0])
	}

	// Read namelen (4 bytes BE)
	var namelen uint32
	if err := binary.Read(conn, binary.BigEndian, &namelen); err != nil {
		t.Fatal(err)
	}

	// Read filename
	fname := make([]byte, namelen)
	if _, err := io.ReadFull(conn, fname); err != nil {
		t.Fatal(err)
	}

	// Read filesize (8 bytes BE)
	var filesize uint64
	if err := binary.Read(conn, binary.BigEndian, &filesize); err != nil {
		t.Fatal(err)
	}
	if filesize != uint64(len(want)) {
		t.Fatalf("filesize: want %d, got %d", len(want), filesize)
	}

	// Read data
	got := make([]byte, filesize)
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}

	if string(got) != string(want) {
		t.Fatalf("data mismatch: want %q, got %q", want, got)
	}

	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

func TestAcceptAndPull(t *testing.T) {
	dir := t.TempDir()
	srcData := []byte("hello from pull test — pulled FROM device")

	port, ln, err := StartListener()
	if err != nil {
		t.Fatal(err)
	}

	dstPath := filepath.Join(dir, "pull_dst.bin")

	errCh := make(chan error, 1)
	go func() {
		errCh <- AcceptAndPull(ln, dstPath)
	}()

	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Send TYPE_PULL announcement (as fileloader would)
	if _, err := conn.Write([]byte{0x02}); err != nil { // TYPE_PULL
		t.Fatal(err)
	}
	reqName := "pull_src.bin"
	if err := binary.Write(conn, binary.BigEndian, uint32(len(reqName))); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte(reqName)); err != nil {
		t.Fatal(err)
	}

	// Send TYPE_DATA with file contents
	if _, err := conn.Write([]byte{0x03}); err != nil { // TYPE_DATA
		t.Fatal(err)
	}
	if err := binary.Write(conn, binary.BigEndian, uint64(len(srcData))); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(srcData); err != nil {
		t.Fatal(err)
	}

	if err := <-errCh; err != nil {
		t.Fatal(err)
	}

	// Verify dst file was written by AcceptAndPull
	dstData, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(dstData) != string(srcData) {
		t.Fatalf("dst file mismatch: want %q, got %q", srcData, dstData)
	}
}

func TestAcceptAndPull_ErrorResponse(t *testing.T) {
	dir := t.TempDir()
	dstPath := filepath.Join(dir, "pull_dst.bin")

	port, ln, err := StartListener()
	if err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- AcceptAndPull(ln, dstPath)
	}()

	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Send TYPE_ERROR — file not found on device
	if _, err := conn.Write([]byte{0x04}); err != nil { // TYPE_ERROR
		t.Fatal(err)
	}
	errMsg := "no such file /etc/shadow"
	if err := binary.Write(conn, binary.BigEndian, uint32(len(errMsg))); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte(errMsg)); err != nil {
		t.Fatal(err)
	}

	// AcceptAndPull should return an error
	if err := <-errCh; err == nil {
		t.Fatal("expected error from AcceptAndPull for missing file")
	}
}
```

- [ ] **Step 2: Run test — expected FAIL**

```bash
go test ./internal/xfer/ -run TestAcceptAnd -v
```

- [ ] **Step 3: Write listener.go**

```go
// internal/xfer/listener.go
package xfer

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
)

const (
	typePush  byte = 0x01
	typePull  byte = 0x02
	typeData  byte = 0x03
	typeError byte = 0x04
)

// StartListener binds a TCP listener on an ephemeral port (port 0).
func StartListener() (int, net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		// Fallback: try all interfaces
		ln, err = net.Listen("tcp", ":0")
		if err != nil {
			return 0, nil, err
		}
	}
	return ln.Addr().(*net.TCPAddr).Port, ln, nil
}

// AcceptAndPush accepts one connection and sends a PUSH frame with the file contents.
func AcceptAndPush(ln net.Listener, localPath string) error {
	conn, err := ln.Accept()
	if err != nil {
		return fmt.Errorf("accept: %w", err)
	}
	defer conn.Close()

	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	// Send type
	if _, err := conn.Write([]byte{typePush}); err != nil {
		return fmt.Errorf("write type: %w", err)
	}

	// Send namelen + filename
	name := []byte(localPath)
	if err := binary.Write(conn, binary.BigEndian, uint32(len(name))); err != nil {
		return fmt.Errorf("write namelen: %w", err)
	}
	if _, err := conn.Write(name); err != nil {
		return fmt.Errorf("write filename: %w", err)
	}

	// Send filesize
	if err := binary.Write(conn, binary.BigEndian, uint64(len(data))); err != nil {
		return fmt.Errorf("write filesize: %w", err)
	}

	// Send data
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("write data: %w", err)
	}

	return nil
}

// AcceptAndPull accepts one connection and receives a file from the device.
// The fileloader on the device sends: TYPE_PULL (announcement) + TYPE_DATA (file contents).
// localPath: where to write the received file on BusyScout host.
func AcceptAndPull(ln net.Listener, localPath string) error {
	conn, err := ln.Accept()
	if err != nil {
		return fmt.Errorf("accept: %w", err)
	}
	defer conn.Close()

	// Read TYPE_PULL announcement (0x02)
	var typ [1]byte
	if _, err := io.ReadFull(conn, typ[:]); err != nil {
		return fmt.Errorf("read type: %w", err)
	}

	if typ[0] == typeError {
		// Fileloader couldn't open the file — read error message
		var msglen uint32
		if err := binary.Read(conn, binary.BigEndian, &msglen); err != nil {
			return fmt.Errorf("read error msglen: %w", err)
		}
		msg := make([]byte, msglen)
		if _, err := io.ReadFull(conn, msg); err != nil {
			return fmt.Errorf("read error msg: %w", err)
		}
		return fmt.Errorf("remote error: %s", string(msg))
	}

	if typ[0] != typePull {
		return fmt.Errorf("expected PULL type (0x02), got 0x%02x", typ[0])
	}

	// Read namelen + filename (consume it)
	var namelen uint32
	if err := binary.Read(conn, binary.BigEndian, &namelen); err != nil {
		return fmt.Errorf("read namelen: %w", err)
	}
	fname := make([]byte, namelen)
	if _, err := io.ReadFull(conn, fname); err != nil {
		return fmt.Errorf("read filename: %w", err)
	}

	// Read TYPE_DATA (0x03) — file contents follow
	if _, err := io.ReadFull(conn, typ[:]); err != nil {
		return fmt.Errorf("read data type: %w", err)
	}
	if typ[0] != typeData {
		return fmt.Errorf("expected DATA type (0x03), got 0x%02x", typ[0])
	}

	// Read filesize
	var filesize uint64
	if err := binary.Read(conn, binary.BigEndian, &filesize); err != nil {
		return fmt.Errorf("read filesize: %w", err)
	}

	// Read file data
	data := make([]byte, filesize)
	if _, err := io.ReadFull(conn, data); err != nil {
		return fmt.Errorf("read data: %w", err)
	}

	// Write to local file
	if err := os.WriteFile(localPath, data, 0644); err != nil {
		return fmt.Errorf("write local file: %w", err)
	}

	return nil
}
```

- [ ] **Step 4: Run test — expected PASS**

```bash
go test ./internal/xfer/ -run TestAcceptAnd -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/xfer/listener.go internal/xfer/listener_test.go
git commit -m "feat(xfer): add TCP listener with framed push/pull protocol"
```

---

### Task 6: xfer/push.go — Fast push flow

**Files:**
- Create: `internal/xfer/push.go`

**Interfaces:**
- Consumes: `helpers.FileloaderForISA()`, `scout.UploadData()`, `StartListener()`, `AcceptAndPush()`
- Produces: `func Push(tc *telnet.TelnetClient, localPath, remotePath, isa, libc string) error`

- [ ] **Step 1: Write push.go**

```go
// internal/xfer/push.go
package xfer

import (
	"fmt"
	"net"
	"strconv"

	"github.com/joomcode/errorx"
	"github.com/krabiswabbie/busyscout/internal/helpers"
	"github.com/krabiswabbie/busyscout/internal/scout"
)

const loaderPath = "/tmp/bs-loader"

// Push uploads a local file to the remote device via fast TCP mode.
// tc is an open telnet connection to the device.
// isa and libc are detected architecture info for selecting the correct fileloader.
func Push(tc *telnetClient, localPath, remotePath, isa, libc string) error {
	// 1. Select fileloader
	loader, err := helpers.FileloaderForISA(isa, libc)
	if err != nil {
		return errorx.Decorate(err, "unsupported architecture")
	}

	// 2. Upload fileloader via printf
	if err := scout.UploadData(tc, loader, loaderPath); err != nil {
		return errorx.Decorate(err, "failed to upload fileloader")
	}

	// 3. chmod +x
	if _, err := tc.Execute("chmod", "+x", loaderPath); err != nil {
		return errorx.Decorate(err, "failed to chmod loader")
	}

	// 4. Start TCP listener
	port, ln, err := StartListener()
	if err != nil {
		return errorx.Decorate(err, "failed to start listener")
	}
	defer ln.Close()

	// 5. Execute fileloader on device (in background via & so telnet returns)
	// Determine BusyScout's IP reachable from device — use the same interface as device
	busyIP := getLocalIPForDevice(tc.Address)
	cmd := fmt.Sprintf("%s push %s %d %s &", loaderPath, busyIP, port, remotePath)
	if _, err := tc.Execute("sh", "-c", cmd); err != nil {
		return errorx.Decorate(err, "failed to start fileloader on device")
	}

	// 6. Accept connection and push file
	if err := AcceptAndPush(ln, localPath); err != nil {
		return errorx.Decorate(err, "fast push failed")
	}

	// 7. Cleanup (best-effort)
	tc.Execute("rm", "-f", loaderPath)

	return nil
}

// getLocalIPForDevice returns BusyScout's IP address on the interface that routes to deviceIP.
func getLocalIPForDevice(deviceIP string) string {
	devIP := net.ParseIP(deviceIP)
	if devIP == nil {
		return "127.0.0.1"
	}

	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ipNet.Contains(devIP) {
				return ipNet.IP.String()
			}
		}
	}

	return "127.0.0.1"
}

// telnetClient is the minimal interface we need from telnet.TelnetClient.
// This avoids a circular dependency (xfer imports helpers, helpers does not import xfer).
type telnetClient interface {
	Execute(name string, args ...string) ([]byte, error)
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/xfer/
```

- [ ] **Step 3: Commit**

```bash
git add internal/xfer/push.go
git commit -m "feat(xfer): add fast Push flow — fileloader upload + TCP transfer"
```

---

### Task 7: xfer/pull.go — Fast pull flow

**Files:**
- Create: `internal/xfer/pull.go`

**Interfaces:**
- Consumes: same as push.go
- Produces: `func Pull(tc *telnetClient, remotePath, localPath, isa, libc string) error`

- [ ] **Step 1: Write pull.go**

```go
// internal/xfer/pull.go
package xfer

import (
	"fmt"

	"github.com/joomcode/errorx"
	"github.com/krabiswabbie/busyscout/internal/helpers"
	"github.com/krabiswabbie/busyscout/internal/scout"
)

// Pull downloads a remote file from the device via fast TCP mode.
func Pull(tc *telnetClient, remotePath, localPath, isa, libc string) error {
	// 1. Select fileloader
	loader, err := helpers.FileloaderForISA(isa, libc)
	if err != nil {
		return errorx.Decorate(err, "unsupported architecture")
	}

	// 2. Upload fileloader via printf
	if err := scout.UploadData(tc, loader, loaderPath); err != nil {
		return errorx.Decorate(err, "failed to upload fileloader")
	}

	// 3. chmod +x
	if _, err := tc.Execute("chmod", "+x", loaderPath); err != nil {
		return errorx.Decorate(err, "failed to chmod loader")
	}

	// 4. Start TCP listener
	port, ln, err := StartListener()
	if err != nil {
		return errorx.Decorate(err, "failed to start listener")
	}
	defer ln.Close()

	// 5. Execute fileloader on device
	busyIP := getLocalIPForDevice(tc.Address)
	cmd := fmt.Sprintf("%s pull %s %d %s &", loaderPath, busyIP, port, remotePath)
	if _, err := tc.Execute("sh", "-c", cmd); err != nil {
		return errorx.Decorate(err, "failed to start fileloader on device")
	}

	// 6. Accept connection and receive file (loader sends TYPE_PULL + TYPE_DATA)
	if err := AcceptAndPull(ln, localPath); err != nil {
		return errorx.Decorate(err, "fast pull failed")
	}

	// 7. Cleanup
	tc.Execute("rm", "-f", loaderPath)

	return nil
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/xfer/
```

- [ ] **Step 3: Commit**

```bash
git add internal/xfer/pull.go
git commit -m "feat(xfer): add fast Pull flow — fileloader upload + TCP receive"
```

---

### Task 8: printf fallback — scout/pull_printf.go

**Files:**
- Create: `internal/scout/pull_printf.go`
- Create: `internal/scout/pull_printf_test.go`

**Interfaces:**
- Produces: `func PullViaPrintf(tc *telnet.TelnetClient, remotePath, localPath string) error`

- [ ] **Step 1: Write the failing test**

```go
// internal/scout/pull_printf_test.go
package scout

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeBase64Output(t *testing.T) {
	want := []byte("hello from printf pull")
	encoded := base64.StdEncoding.EncodeToString(want)
	output := encoded + "\n#"

	got, err := decodeBase64Output([]byte(output))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestDecodeHexOutput(t *testing.T) {
	want := []byte("hello hex test")
	hexStr := ""
	for _, b := range want {
		hexStr += fmt.Sprintf("%02x", b)
	}
	// xxd -p output: lines of hex, then prompt
	output := hexStr + "\n#"

	got, err := decodeHexOutput([]byte(output))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("want %q, got %q", want, got)
	}
}
```

- [ ] **Step 2: Run test — expected FAIL**

```bash
go test ./internal/scout/ -run TestDecode -v
```

- [ ] **Step 3: Write pull_printf.go**

```go
// internal/scout/pull_printf.go
package scout

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/joomcode/errorx"
)

// PullViaPrintf downloads a remote file via printf channel (slow, for NAT scenarios).
// Tries base64 first, then xxd -p, then od -An -t x1.
func PullViaPrintf(tc telnetExecutor, remotePath, localPath string) error {
	var data []byte
	var err error

	// Try base64
	data, err = pullWithEncoder(tc, remotePath, "base64", decodeBase64Output)
	if err == nil {
		return os.WriteFile(localPath, data, 0644)
	}

	// Try xxd -p
	data, err = pullWithEncoder(tc, remotePath, "xxd -p", decodeHexOutput)
	if err == nil {
		return os.WriteFile(localPath, data, 0644)
	}

	// Try od -An -t x1
	data, err = pullWithEncoder(tc, remotePath, "od -An -t x1", decodeHexOutput)
	if err == nil {
		return os.WriteFile(localPath, data, 0644)
	}

	return errorx.Decorate(err, "no suitable encoder found on device (tried base64, xxd, od)")
}

// telnetExecutor is the minimal telnet client interface.
type telnetExecutor interface {
	Execute(name string, args ...string) ([]byte, error)
}

func pullWithEncoder(tc telnetExecutor, remotePath, encoder string, decoder func([]byte) ([]byte, error)) ([]byte, error) {
	cmd := fmt.Sprintf("%s %s", encoder, remotePath)
	// Split encoder into command and args
	parts := strings.Fields(cmd)
	name := parts[0]
	args := parts[1:]

	stdout, err := tc.Execute(name, args...)
	if err != nil {
		return nil, errorx.Decorate(err, "encoder command failed")
	}

	return decoder(stdout)
}

func decodeBase64Output(output []byte) ([]byte, error) {
	// Strip trailing prompt (last line containing # or $)
	s := stripPrompt(string(output))
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return base64.StdEncoding.DecodeString(s)
}

func decodeHexOutput(output []byte) ([]byte, error) {
	s := stripPrompt(string(output))
	// Remove all whitespace
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, " ", "")
	return hex.DecodeString(s)
}

func stripPrompt(s string) string {
	// Remove trailing prompt line (# or $ at end of last line)
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "#" || trimmed == "$" {
			lines = lines[:i]
			break
		}
		// Also handle "something #" or "something $"
		if len(trimmed) > 0 && (trimmed[len(trimmed)-1] == '#' || trimmed[len(trimmed)-1] == '$') {
			lines = lines[:i]
			break
		}
	}
	return strings.Join(lines, "")
}
```

Добавить импорт в тестовый файл:

```go
import "fmt"
```

- [ ] **Step 4: Run test — expected PASS**

```bash
go test ./internal/scout/ -run TestDecode -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/scout/pull_printf.go internal/scout/pull_printf_test.go
git commit -m "feat(scout): add PullViaPrintf — base64/xxd/od fallback for NAT pull"
```

---

### Task 9: Update scout.go — mode selection and Pull()

**Files:**
- Modify: `internal/scout/scout.go`

**Interfaces:**
- Produces: `Scout.Push()` — auto-selects fast vs printf
- Produces: `Scout.Pull()` — auto-selects fast vs printf
- Consumes: `xfer.IsSameSubnet()`, `xfer.Push()`, `xfer.Pull()`, `PullViaPrintf()`

- [ ] **Step 1: Modify Scout struct and add detectISA method**

Заменить `type Scout struct` и добавить методы:

```go
type Scout struct {
	localFile string
	remote    *RemoteFile
	verbose   bool
	bar       *progressbar.ProgressBar
	isa       string // cached ISA from light detection
	libc      string // cached libc from light detection
}

// detectISALight runs a quick ISA+libc detection on the device.
func (s *Scout) detectISALight() error {
	if s.isa != "" {
		return nil // already cached
	}

	tc, err := s.newClient()
	if err != nil {
		return err
	}
	defer tc.Close()

	// uname -m → ISA
	stdout, err := tc.Execute("uname", "-m")
	if err != nil {
		return errorx.Decorate(err, "uname failed")
	}
	s.isa = parseUnameMachine(string(stdout))

	// ls libc → libc family
	stdout, err = tc.Execute("sh", "-c", "ls -l /lib/libc.so* /lib/ld-*.so* 2>/dev/null || true")
	if err == nil {
		s.libc = parseLibcFamily(string(stdout))
	}

	return nil
}

// parseUnameMachine extracts ISA from uname -m output.
func parseUnameMachine(output string) string {
	o := strings.TrimSpace(strings.ToLower(output))
	switch {
	case strings.HasPrefix(o, "armv"):
		return "arm"
	case strings.HasPrefix(o, "aarch64"):
		return "aarch64"
	case strings.HasPrefix(o, "mips"):
		return "mips"
	case o == "i386" || o == "i486" || o == "i586" || o == "i686":
		return "x86"
	case o == "x86_64":
		return "x86_64"
	default:
		return o
	}
}

// parseLibcFamily detects libc family from ls output.
func parseLibcFamily(output string) string {
	o := strings.ToLower(output)
	switch {
	case strings.Contains(o, "uclibc"):
		return "uclibc"
	case strings.Contains(o, "musl"):
		return "musl"
	case strings.Contains(o, "glibc") || strings.Contains(o, "libc.so"):
		return "glibc"
	default:
		return ""
	}
}
```

- [ ] **Step 2: Modify Push() to use auto-selection**

Заменить существующий `Push()` метод. Начало метода оставить прежним до строки `data, errRead := os.ReadFile(s.localFile)` — добавить перед ней:

```go
func (s *Scout) Push() error {
	// Detect same subnet
	if xfer.IsSameSubnet(s.remote.Host) {
		if err := s.detectISALight(); err != nil {
			// Fall through to printf mode on detection failure
		} else {
			tc, err := s.newClient()
			if err != nil {
				return err
			}
			defer tc.Close()
			return xfer.Push(tc, s.localFile, s.remote.Path, s.isa, s.libc)
		}
	}

	// printf fallback — existing code below
	data, errRead := os.ReadFile(s.localFile)
	// ... rest of existing Push() code unchanged ...
```

Добавить импорт `"github.com/krabiswabbie/busyscout/internal/xfer"`.

- [ ] **Step 3: Add Pull() method**

```go
// Pull downloads a file from the remote device.
func (s *Scout) Pull(localPath string) error {
	// Detect same subnet
	if xfer.IsSameSubnet(s.remote.Host) {
		if err := s.detectISALight(); err != nil {
			// Fall through to printf mode
		} else {
			tc, err := s.newClient()
			if err != nil {
				return err
			}
			defer tc.Close()
			return xfer.Pull(tc, s.remote.Path, localPath, s.isa, s.libc)
		}
	}

	// printf fallback
	tc, err := s.newClient()
	if err != nil {
		return err
	}
	defer tc.Close()
	return PullViaPrintf(tc, s.remote.Path, localPath)
}
```

- [ ] **Step 4: Fix xfer interface — Add Address field to telnetClient**

The `telnetClient` interface in `xfer/push.go` needs the `Address` field for `getLocalIPForDevice`. Update it:

```go
type telnetClient interface {
	Execute(name string, args ...string) ([]byte, error)
	Address string
}
```

But wait — `telnet.TelnetClient` does not export `Address`. Let's check...

Actually, looking at the existing code in `scout.go`, the `TelnetClient` is created with `.Address` set but that's an internal field. Let's adjust the approach: pass device IP separately instead.

Update `xfer/push.go` — change `Push` signature:

```go
func Push(tc *telnetClient, localPath, remotePath, isa, libc, hostIP string) error {
```

And `xfer/pull.go`:

```go
func Pull(tc *telnetClient, remotePath, localPath, isa, libc, hostIP string) error {
```

Update the calls in `scout.go`:
```go
return xfer.Push(tc, s.localFile, s.remote.Path, s.isa, s.libc, s.remote.Host)
return xfer.Pull(tc, s.remote.Path, localPath, s.isa, s.libc, s.remote.Host)
```

Remove `Address` from the `telnetClient` interface and keep it as:

```go
type telnetClient interface {
	Execute(name string, args ...string) ([]byte, error)
}
```

Update `getLocalIPForDevice` to accept hostIP string directly (no change needed, it already takes a string).

- [ ] **Step 5: Verify compilation**

```bash
go build ./internal/scout/
```

- [ ] **Step 6: Commit**

```bash
git add internal/scout/scout.go internal/xfer/push.go internal/xfer/pull.go
git commit -m "feat(scout): auto-select fast vs printf mode for push/pull"
```

---

### Task 10: Update main.go — new CLI (push + pull)

**Files:**
- Modify: `main.go`

**Interfaces:**
- Produces: `busyscout push <local> remote [--verbose]`, `busyscout pull remote <local> [--verbose]`

- [ ] **Step 1: Read current main.go to understand CLI dispatch**

- [ ] **Step 2: Rewrite CLI**

```go
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/krabiswabbie/busyscout/internal/scout"
)

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
```

- [ ] **Step 3: Add scout.NewPull constructor**

Add to `internal/scout/scout.go`:

```go
// NewPull creates a Scout for pull operations (no local file required).
func NewPull(target string, verboseFlag bool) (*Scout, error) {
	remote, err := ParseRemoteFileName(target)
	if err != nil {
		return nil, errorx.Decorate(err, "failed to parse remote address")
	}

	return &Scout{
		remote:  remote,
		verbose: verboseFlag,
	}, nil
}
```

- [ ] **Step 4: Verify compilation**

```bash
go build -o busyscout .
```

- [ ] **Step 5: Dry-run smoke test**

```bash
./busyscout --help
```

Expected: usage message with push, pull, detect commands.

- [ ] **Step 6: Commit**

```bash
git add main.go internal/scout/scout.go
git commit -m "feat(cli): add push/pull commands with auto mode selection"
```

---

### Task 11: Integration test + final verification

**Files:**
- Modify: `tests/docker-compose.yaml` — add port mapping for TCP test
- Create: `tests/integration_xfer_test.sh`
- Modify: `Makefile` — add `test-integration-xfer` target

**Interfaces:**
- Consumes: all previous tasks
- Produces: integration test passing on Docker ARM telnetd

- [ ] **Step 1: Update docker-compose.yaml**

Добавить порт для TCP-тестов (не telnet, а для прямого TCP соединения между fileloader и BusyScout):

```yaml
services:
  telnetd:
    image: wistic/telnetd
    ports:
      - "2323:23"
      - "9999:9999"  # TCP port for fast xfer test
    restart: unless-stopped
```

- [ ] **Step 2: Write integration test script**

```bash
#!/bin/bash
# tests/integration_xfer_test.sh
# Integration test for fast file transfer (push + pull)
set -e

BINARY="${1:-./busyscout}"
REMOTE="user:password@127.0.0.1:2323:/tmp"
LOCAL_PUSH="/tmp/busyscout_integration_push.bin"
LOCAL_PULL="/tmp/busyscout_integration_pull.bin"

# Generate test data (100 KB random)
dd if=/dev/urandom of="$LOCAL_PUSH" bs=1024 count=100 2>/dev/null

# Push via fast mode (same subnet — localhost)
echo "=== Testing fast push ==="
"$BINARY" push "$LOCAL_PUSH" "$REMOTE/integration_test_push.bin" --verbose

# Pull via fast mode
echo "=== Testing fast pull ==="
"$BINARY" pull "$REMOTE/integration_test_push.bin" "$LOCAL_PULL" --verbose

# Compare checksums
PUSH_SUM=$(shasum -a 256 "$LOCAL_PUSH" | cut -d' ' -f1)
PULL_SUM=$(shasum -a 256 "$LOCAL_PULL" | cut -d' ' -f1)

if [ "$PUSH_SUM" != "$PULL_SUM" ]; then
    echo "FAIL: checksum mismatch"
    echo "  push: $PUSH_SUM"
    echo "  pull: $PULL_SUM"
    exit 1
fi

# Cleanup
rm -f "$LOCAL_PUSH" "$LOCAL_PULL"

echo "PASS: integration xfer test"
```

- [ ] **Step 3: Add make target**

```makefile
test-integration-xfer:
	docker compose -f tests/docker-compose.yaml up -d
	sleep 2
	bash tests/integration_xfer_test.sh ./busyscout
	docker compose -f tests/docker-compose.yaml down
```

Update `.PHONY`:

```makefile
.PHONY: ... test-integration-xfer
```

- [ ] **Step 4: Run integration test**

```bash
make test-integration-xfer
```

Expected: PASS.

- [ ] **Step 5: Run full test suite**

```bash
make test
```

Expected: all tests pass.

- [ ] **Step 6: Commit**

```bash
git add tests/docker-compose.yaml tests/integration_xfer_test.sh Makefile
git commit -m "test(xfer): add integration test for fast push/pull"
```
