# Architecture Detection — Design Spec

**Date:** 2026-07-15
**Status:** approved
**Approach:** C — Native + staged binary helper + fallback

## Overview

Add a `detect` subcommand to BusyScout that connects to a target device (IP camera / NVR with BusyBox) via telnet and determines its CPU architecture, libc, endianness, word size, ARM float ABI, and sub-architecture. This enables selecting or building the correct cross-compiled binary for the device.

## CLI

```
busyscout detect user:pass@host:/tmp [--verbose]
```

The path (`/tmp`) is used as the working directory for uploading and running the helper binary. Output is human-readable:

```
Architecture:     ARMv7 (32-bit, little-endian)
Float ABI:        hard-float (VFP)
libc:             uClibc 0.9.33
SoC hint:         HiSilicon hi3516
Toolchain hint:   arm-linux-uclibcgnueabihf
```

If `--verbose` is set, raw command output is printed in addition to the parsed result.

Fields that could not be determined are marked `[uncertain]`.

## Two-Phase Detection Flow

### Phase 1 — Lightweight (single telnet session)

Commands executed on the device:

```
uname -a
cat /proc/cpuinfo
cat /proc/version
ls -l /lib/libc.so* /lib/ld-*.so* 2>/dev/null
cat /proc/hisi_chipid 2>/dev/null
ls /proc/umap 2>/dev/null
dmesg | grep -iE 'hisi|chip|soc' 2>/dev/null | head -5
```

Additionally, probe for optional tools:

```
file /bin/busybox 2>/dev/null
od -An -t x1 -N20 /bin/busybox 2>/dev/null
readelf -A /bin/busybox 2>/dev/null
```

**After Phase 1 we know:**
- ISA family (`uname -m` → armv7l, aarch64, mips, x86_64, etc.)
- Word size (32/64)
- Endianness (derived from ISA + `/proc/cpuinfo`)
- ARM features from `/proc/cpuinfo` (`vfp`, `neon`, `half`, `thumb`)
- libc family from library filenames (glibc / uClibc / musl)
- SoC hints

**Early exit:** If we have all required ELF data (class, e_machine, float ABI for ARM) from any combination of `file`, `od`, and `readelf` — skip Phase 2. This means: `file` alone may suffice, or `od` (ELF bytes) + `readelf -A` (float ABI) together.

### Phase 2 — Binary Helper (only if ELF / float ABI data is incomplete)

1. **Select helper** from embedded binaries based on ISA family from Phase 1:

| uname -m pattern | Helper |
|---|---|
| `armv*` | `elfreader-arm` |
| `aarch64` | `elfreader-aarch64` |
| `mips`, `mips64` | `elfreader-mips` or `elfreader-mipsel` |
| `i*86` | `elfreader-x86` |
| `x86_64` | `elfreader-x86_64` |

Endianness for MIPS is determined from `/proc/cpuinfo` (`byteorder`).

2. **Upload helper** to `/tmp/bs-helper` using the existing printf-based upload (`scout.UploadData`).

3. **Execute** `/tmp/bs-helper /bin/busybox`, parse `key=value` output:

```
class=32
endian=little
machine=arm
float_abi=hard
cpu_arch=v7
```

4. **Cleanup**: `rm /tmp/bs-helper`.

If helper upload or execution fails — detect returns an error with context.

## Helper Binary (`elfreader`)

- Written in C, statically linked, stripped
- Uses only raw `read()` + `write()` syscalls — no libc dependency beyond startup
- Reads ELF header of the file passed as argument
- Output fields in `key=value` format, one per line
- Target size: ~4 KB per binary

**Build targets (6 binaries):**

| Target triple | Covers |
|---|---|
| `arm-linux-gnueabi` (armv5te, soft-float) | ARMv5/v6/v7 LE |
| `aarch64-linux-gnu` | AArch64 |
| `mipsel-linux-gnu` (soft-float) | MIPS32 LE |
| `mips-linux-gnu` (soft-float) | MIPS32 BE |
| `i386-linux-gnu` | x86 |
| `x86_64-linux-gnu` | x86_64 |

## Code Structure

```
internal/
  detect/
    detect.go     — Detect(remote, verbose) (*Fingerprint, error)
    phase1.go     — runPhase1: execute native commands, parse output
    phase2.go     — runPhase2: select helper, upload via scout.UploadData, parse key=value
  helpers/
    helpers.go    — //go:embed of 6 helper binaries, map ISA → []byte
    bin/          — compiled elfreader-* binaries (not committed, built by Makefile)
  scout/
    scout.go      — Push() refactored: sendChunk delegates to UploadData
    upload.go     — UploadData(client, data, remotePath): printf-based file upload
    files.go      — unchanged
  telnet/
    telnet.go     — unchanged
```

### Key types

```go
// detect/detect.go
type Fingerprint struct {
    ISA         string // "arm", "aarch64", "mips", "x86", "x86_64"
    WordSize    int    // 32 or 64
    Endianness  string // "little" or "big"
    ARMSubArch  string // "v5", "v6", "v7", "" if not ARM
    FloatABI    string // "hard", "soft", "" if not applicable
    Libc        string // "glibc", "uClibc", "musl", ""
    SoCHint     string // e.g. "HiSilicon hi3516", ""
    ToolchainHint string // e.g. "arm-linux-uclibcgnueabihf"
}
```

## UploadData extraction

Currently `sendChunk` in `scout.go` opens its own telnet connection, sends data via printf, and closes it. For the helper upload we need the same printf-based upload but controlled externally (connection already open, helper binary data from `[]byte`).

`UploadData(client *telnet.TelnetClient, data []byte, remotePath string) error` — sends data in 128-byte printf lines to the given path using an already-open client. `sendChunk` in `Push()` is refactored to call `UploadData`.

## Error Handling

- **No telnet access**: `Detect()` wraps connection errors via `errorx`
- **Partial data in Phase 1**: if ISA cannot be determined → error (cannot proceed); other missing fields → marked `[uncertain]`
- **Helper upload failure** (disk full, no /tmp permissions): error with context
- **Helper execution failure** (wrong arch, segfault): error, but does not crash the process
- **Timeouts**: inherited from `TelnetClient.Timeout` (default 10s)

## Testing

| Test | Location | Type |
|---|---|---|
| Parse uname / cpuinfo / libc output | `detect/phase1_test.go` | Unit (fixtures) |
| Parse helper key=value output | `detect/phase2_test.go` | Unit |
| Helper binary smoke test | `tests/` | Integration (Docker + emulated ELF) |
| Existing `files_test.go` | unchanged | Unit |

Integration test: extend `tests/docker-compose.yaml` with a custom image that provides `/proc/cpuinfo`, `/bin/busybox` stub, and libc symlinks, simulating a typical camera environment.

## Build Changes (Makefile)

```makefile
helpers:
    CC=arm-linux-gnueabi-gcc ... -o internal/helpers/bin/elfreader-arm
    CC=aarch64-linux-gnu-gcc ... -o internal/helpers/bin/elfreader-aarch64
    CC=mipsel-linux-gnu-gcc ... -o internal/helpers/bin/elfreader-mipsel
    CC=mips-linux-gnu-gcc ... -o internal/helpers/bin/elfreader-mips
    CC=gcc ... -o internal/helpers/bin/elfreader-x86
    CC=gcc ... -o internal/helpers/bin/elfreader-x86_64
```

Helper binaries are built via `make helpers` and committed to the repo (small, ~24 KB total). Regular `make build` does not require cross-compilers — helpers are pre-built.

## Out of scope

- Machine-readable output (JSON) — deferred
- Auto-selecting and uploading the correct BusyScout binary itself — remains a separate manual step
- CRC / integrity verification beyond file size
