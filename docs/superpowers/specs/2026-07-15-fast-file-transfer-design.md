# Fast File Transfer — Design Spec

**Date:** 2026-07-15
**Status:** draft
**Approach:** Pre-compiled fileloader binary + TCP framing + printf fallback

## Overview

Replace the current printf-only file transfer with a hybrid approach: when the target device is in the same subnet, BusyScout uploads a pre-compiled `fileloader` binary via printf, then uses a dedicated TCP connection for high-speed framed file transfer (push and pull). When the device is behind NAT, it falls back to printf-based transfer.

This is a **major version change** (breaking CLI): old `push` format is replaced, pull is added as a new command.

## Motivation

Current `printf`-based transfer sends 128 bytes per shell command over telnet — slow and asymmetric (no pull). A small TCP-based loader (~6-8 KB) delivered the same way enables bulk transfer at line speed once the connection is established. The loader is ISA×libc-aware (same strategy as the existing `elfreader` helpers), delivered via the same printf method already used in `detect` Phase 2 and `push`.

## CLI

```
busyscout push <local> user:pass@host:/path [--verbose]
busyscout pull user:pass@host:/path <local> [--verbose]
```

Mode selection is automatic:
- **Same subnet** → fast mode (TCP via fileloader)
- **Different subnet / NAT** → printf fallback

Output on success:
```
push:  firmware.bin → 192.168.1.10:/tmp/firmware.bin  1.2 MB  (0.8s, fast)
pull:  192.168.1.10:/etc/shadow → ./shadow  1.2 KB  (0.3s, fast)
```

On fallback:
```
push:  firmware.bin → 10.0.0.5:/tmp/firmware.bin  1.2 MB  (12.4s, printf)
```

## Architecture

### Overall flow

```
CLI: busyscout push|pull <local> <remote>

1. ParseRemoteFileName() — as current
2. IsSameSubnet(deviceIP) — compare with local interfaces
3. Branch:

   ┌─ LAN ─────────────────────────────────────────┐
   │ 1. Detect ISA+libc (light Phase 1)             │
   │ 2. Select fileloader for ISA×libc              │
   │ 3. UploadData(fileloader → /tmp/bs-loader)     │
   │ 4. chmod +x /tmp/bs-loader                     │
   │ 5. BusyScout starts TCP listener (ephemeral)   │
   │ 6. Execute: /tmp/bs-loader <mode> <ip> <port>  │
   │    [<filename>] on device                      │
   │ 7. Framed transfer over TCP                    │
   │ 8. rm /tmp/bs-loader (best-effort)             │
   └────────────────────────────────────────────────┘

   ┌─ NAT (fallback) ───────────────────────────────┐
   │ push: printf directly (current Scout.Push)     │
   │ pull: base64/xxd via telnet → decode locally   │
   └────────────────────────────────────────────────┘
```

### Subnet detection

`IsSameSubnet(deviceIP string) bool`:
- Enumerate local network interfaces (via `net.Interfaces()`)
- For each interface, compare `deviceIP` against the interface's network (IP & mask)
- Return true if any interface shares a subnet with the device
- If no interfaces available → false (safe fallback to printf)

### TCP connection strategy

**Reverse connection only.** The fileloader connects back to BusyScout (`connect` mode). This avoids opening ports on the camera firewall and works reliably in LAN. The `listen` (forward) mode is unnecessary because:
- In LAN: reverse works reliably
- In NAT: neither reverse nor forward work without manual port forwarding

## Protocol (Framing)

Single TCP connection per operation. BusyScout listens, fileloader connects. Connection closes after transfer.

### Push (BusyScout → device)

```
[1B type = 0x01 'P'] [4B namelen BE] [filename] [8B filesize BE] [data bytes...]
```

Total header: 13 bytes + filename.

### Pull (device → BusyScout)

Fileloader на устройстве читает файл и отправляет два сообщения подряд:

Announcement:
```
[1B type = 0x02 'G'] [4B namelen BE] [filename]
```

Data:
```
[1B type = 0x03 'D'] [8B filesize BE] [data bytes...]
```

Error (если файл не найден):
```
[1B type = 0x04 'E'] [4B msglen BE] [error message]
```

### Message types

| Type | Value | Direction | Meaning |
|------|-------|-----------|---------|
| `P` | 0x01 | BusyScout→loader | Push file |
| `G` | 0x02 | loader→BusyScout | Pull announcement («отправляю файл X») |
| `D` | 0x03 | loader→BusyScout | Pull data (содержимое файла) |
| `E` | 0x04 | loader→BusyScout | Pull error (файл не найден) |

All multi-byte integers are big-endian.

## Fileloader (C binary)

### Source

`internal/helpers/src/fileloader.c` — ~100 lines of C.

```
Usage: fileloader <mode> <ip> <port> [filename]

  push <ip> <port> <filename>
    Connect to ip:port, read framed header, write data to filename.

  pull <ip> <port> <filename>
    Connect to ip:port, send pull request for filename,
    read framed response, write data to stdout.
```

Dependencies: standard POSIX sockets (`socket`, `connect`, `read`, `write`, `open`, `close`) + `atoi`. No external libraries.

### Size

~6-8 KB per binary (dynamically linked, same approach as elfreader). Compare: static linking would produce 200-400 KB binaries — too large for printf delivery.

### Build matrix (8 variants)

| Binary | ISA | libc | Toolchain |
|--------|-----|------|-----------|
| `fileloader-arm-uclibc` | ARM32 | uClibc | HiSilicon arm-hisiv300-linux |
| `fileloader-arm-glibc` | ARM32 | glibc | arm-linux-gnueabihf (Cortex-A9) |
| `fileloader-arm-musl` | ARM32 | musl | arm-linux-musleabi |
| `fileloader-aarch64-glibc` | AArch64 | glibc | aarch64-linux-gnu |
| `fileloader-mipsel-uclibc` | MIPS32 LE | uClibc | mipsel-linux-uclibc |
| `fileloader-mips-uclibc` | MIPS32 BE | uClibc | mips-linux-uclibc |
| `fileloader-x86-glibc` | x86 | glibc | i686-linux-gnu (ubuntu:22.04) |
| `fileloader-x86_64-glibc` | x86_64 | glibc | x86_64-linux-gnu (ubuntu:22.04) |

Compilation flags: `-std=c99 -s -Os` (same as elfreader).
Output path: `internal/helpers/bin/fileloader-*`.

### Embedding

`internal/helpers/fileloader.go`:

```go
package helpers

import _ "embed"

//go:embed bin/fileloader-arm-uclibc
var fileloaderArmUclibc []byte

// ... 7 more ...

func FileloaderForISA(isa, libc string) []byte {
    // same mapping logic as HelperForISA
}
```

## Go packages

### New: `internal/xfer/`

| File | Purpose |
|------|---------|
| `listener.go` | `StartListener() (port int, listener net.Listener)` — bind ephemeral port; `AcceptAndPush(conn, filename, data)`, `AcceptAndPull(conn, filename)` — framed read/write |
| `push.go` | `Push(tc *telnet.TelnetClient, localPath, remotePath string)` — full fast push flow |
| `pull.go` | `Pull(tc *telnet.TelnetClient, remotePath, localPath string)` — full fast pull flow |
| `subnet.go` | `IsSameSubnet(deviceIP string) bool` |

### Modified: `internal/scout/`

| File | Change |
|------|--------|
| `scout.go` | `Push()` delegates to `xfer.Push()` or current printf logic based on `IsSameSubnet()`. New `Pull()` method. |
| `upload.go` | No changes — `UploadData()` reused for fileloader delivery |
| `pull_printf.go` | **New.** `PullViaPrintf()` — base64/xxd via telnet, decode locally |

### Modified: `internal/helpers/`

| File | Change |
|------|--------|
| `fileloader.go` | **New.** `//go:embed` + `FileloaderForISA()` (mirrors existing `helpers.go`) |

### Modified: `main.go`

Replace current CLI dispatch:
- `cmdPush` delegates to `scout.Push()` which now auto-selects mode
- `cmdPull` — **new** command, delegates to `scout.Pull()`

### ISA detection for push/pull

When `push` or `pull` is called, a light Phase 1 is performed to determine ISA and libc if not already cached from a prior `detect` call. Two commands only (~1 second total):
- `uname -m` → ISA (armv7l, aarch64, mips, etc.)
- `ls -l /lib/libc.so* /lib/ld-*.so* 2>/dev/null` → libc family (glibc/uClibc/musl)

Full `detect` Phase 1 (cpuinfo, os probe, etc.) is not needed — ISA+libc is sufficient to select the correct fileloader.

## Error handling

| Scenario | Behavior |
|----------|----------|
| Subnet undetermined | Fallback to printf |
| Fileloader not available for detected ISA | Error: "unsupported architecture: ..." |
| TCP connection timeout (3s) | Close listener, error, best-effort cleanup of /tmp/bs-loader |
| Connection drops mid-transfer | Remove incomplete local file, cleanup loader |
| Port already in use on BusyScout | Retry with next ephemeral port (up to 5) |
| No `base64` or `xxd` on device (pull fallback) | Try `base64`, then `xxd -p`, then `od -An -t x1`; error if none available |

## printf fallback — pull

When the device is behind NAT, `pull` uses the printf channel in reverse:

1. Detect available encoder: try `base64 <file>`, then `xxd -p <file>`, then `od -An -t x1 <file>`
2. Execute on device, capture output via `tc.Execute()`
3. Decode locally (base64 or hex) and write to local file

This is slow but functional — same throughput characteristics as printf-based push.

## Testing

### Unit tests (Go)

| Test file | What |
|-----------|------|
| `internal/xfer/subnet_test.go` | `IsSameSubnet()` on known IP+interface pairs |
| `internal/xfer/listener_test.go` | Framed read/write on loopback (no telnet, pure TCP) |
| `internal/scout/pull_printf_test.go` | base64/xxd decoding |

### C tests (fileloader)

```
make test-fileloader
```

- Compile fileloader for host architecture
- Start Go listener on loopback
- `fileloader push 127.0.0.1 <port> /tmp/test_out` → compare output file
- `fileloader pull 127.0.0.1 <port> /tmp/test_in` → compare received file

### Integration tests (Docker)

```
make test-integration-xfer
```

- ARM container with telnetd + busybox (existing `tests/docker-compose.yaml`)
- Full cycle: detect ISA → upload fileloader → push file → pull file → compare checksums
- Fallback test: NAT simulation via separate docker network

## Makefile changes

**New targets:**
- `make fileloaders` — cross-compile 8 fileloader variants via Docker
- `make fileloaders-clean` — remove fileloader binaries
- `make test-fileloader` — C unit tests for fileloader
- `make test-integration-xfer` — integration test

**Modified targets:**
- `make all` = clean + test + fileloaders + build
- `make helpers` — unchanged (elfreader untouched)

## What does NOT change

- **`detect` mode** — unchanged, elfreader remains separate
- **telnet client** — no changes to `internal/telnet/`
- **`scout.UploadData()`** — reused as-is for fileloader delivery, no changes
- **ELF helpers** — `internal/helpers/helpers.go` and `elfreader.c` untouched

## Future considerations

- **Multiple files per session** — framing protocol supports it (type byte could introduce a batch mode), but out of scope for v1
- **Parallel workers** — current printf push uses 10 parallel telnet connections; fast mode uses a single TCP connection. After v1 ships, profile whether parallelism would help for LAN transfers (likely not — TCP is already line speed)
- **Progress bar** — existing progress bar in `scout.go` can be adapted for framed transfer (known filesize from header)
