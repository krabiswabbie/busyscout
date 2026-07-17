# OS Detection — Device Profile

**Date:** 2026-07-15
**Status:** draft
**Parent:** `detect` command (architecture fingerprinting)

## Summary

Дополнить режим `detect` сбором информации об операционной системе целевого устройства (IP-камера/NVR с BusyBox). Это дополнение к архитектурному детекту — не влияет на выбор хелпера или toolchain hint, чисто информационный профиль устройства.

## New fields in Fingerprint

```go
// OS profile — all best-effort, may be empty
KernelVersion  string   // "Linux 3.10.0-hi3516 (armv7l) #1 Tue Sep 15 10:00:00 CST 2020"
KernelBuild    string   // /proc/version: compiler + build date
BusyBoxVersion string   // "v1.31.1"
DeviceModel    string   // "HiSilicon hi3516ev200"
LibcVersion    string   // "2.28" / "0.9.33.2"
Uptime         string   // "3d 12:45"
TotalRAM       string   // "128 MB"
RootFSUsage    string   // "12M / 128M (9%)"
Mounts         []string // ["/ : squashfs,ro", "/tmp : tmpfs,rw"]
NetTools       []string // ["curl", "wget", "nc"]
```

## Data sources

### From existing Phase 1 commands (parse existing output, no new telnet calls)

| Field | Source | Parser change |
|---|---|---|
| `KernelVersion` | `uname -a` output | `parseUname()` — save full string |
| `KernelBuild` | `/proc/version` output | `parseProcVersion()` — save full string |
| `DeviceModel` | `/proc/cpuinfo` field `Hardware` | `parseCPUInfo()` — extract Hardware |
| `LibcVersion` | `ls -l /lib/libc.so* ...` | `parseLibc()` — already extracts uClibc version; extend for glibc |

### New OS probe commands (probeOS method)

All commands are best-effort — failure or empty output leaves the field empty.

| Command | Field | Parser |
|---|---|---|
| `busybox 2>&1` | `BusyBoxVersion` | First line of output |
| `cat /proc/device-tree/model 2>/dev/null` | `DeviceModel` | Full line, supplements cpuinfo Hardware |
| `/lib/libc.so.6 2>&1 \|\| true` | `LibcVersion` | glibc prints version to stderr; parse semver |
| `cat /proc/meminfo 2>/dev/null` | `TotalRAM` | First line: `MemTotal: NNN kB` → "NNN MB" |
| `df / 2>/dev/null \|\| df 2>/dev/null` | `RootFSUsage` | Parse used/available, format as "12M / 128M (9%)" |
| `mount 2>/dev/null \|\| cat /proc/mounts` | `Mounts` | Filter: keep `/`, `/tmp`, `/var`, `/mnt`; format as "path : fstype,flags" |
| `cat /proc/uptime 2>/dev/null` | `Uptime` | Parse first number (seconds) → "3d 12:45" |
| `ls /usr/bin/curl /usr/bin/wget /bin/nc /usr/sbin/nc /usr/bin/openssl /usr/bin/tftp /usr/bin/ftpget /usr/bin/ncat 2>/dev/null` | `NetTools` | For each existing file, extract basename |

## Architecture

### New file: `internal/detect/osprobe.go`

```
probeOS(tc *telnet.Client, fp *Fingerprint)
├── runAndParse("busybox", fp, parseBusyBoxVersion)
├── runAndParse("cat /proc/device-tree/model", fp, parseDeviceModel)
├── runAndParse("/lib/libc.so.6", fp, parseGlibcVersion)   // 2>&1, || true
├── runAndParse("cat /proc/meminfo", fp, parseMemory)
├── runAndParse("df / || df", fp, parseDiskUsage)
├── runAndParse("mount || cat /proc/mounts", fp, parseMounts)
├── runAndParse("cat /proc/uptime", fp, parseUptime)
└── runAndParse("ls /usr/bin/curl /usr/bin/wget ...", fp, parseNetTools)
```

Each `runAndParse` helper:
- Calls `tc.Execute(cmd...)`
- On error or empty output: returns silently (field stays empty)
- On success: calls the parser with the output string

### Modified file: `internal/detect/phase1.go`

1. `parseUname()` — add: `fp.KernelVersion = output`
2. `parseCPUInfo()` — add: extract `Hardware` field → `fp.DeviceModel`
3. `parseProcVersion()` — add: `fp.KernelBuild = output`
4. `parseLibc()` — add: save `fp.LibcVersion` (already extracted for uClibc; for glibc save "glibc" until probeOS refines it)
5. `runPhase1()` — add: `probeOS(tc, fp)` call after all parsing, before `needsPhase2()`

### Modified file: `internal/detect/detect.go`

- `Format()` — add OS Profile section after architecture section
- Each line only shown if field is non-empty
- Entire "OS Profile" section skipped if all OS fields are empty

## Output format

```
Architecture: ARMv7 (32-bit, little-endian)
Float ABI: hard-float (VFP)
libc: uClibc 0.9.33.2
SoC: HiSilicon hi3516
Toolchain: armv7-linux-uclibceabihf

OS Profile:
  Kernel:    Linux 3.10.0-hi3516 (armv7l) #1 Tue Sep 15 10:00:00 CST 2020
  Build:     gcc 4.8.3 (Hisilicon_v300) — Tue Sep 15 10:00:00 CST 2020
  BusyBox:   v1.26.2
  Device:    HiSilicon hi3516ev200
  Uptime:    3d 12:45
  RAM:       128 MB
  Disk (/):  12M / 128M (9%)
  Mounts:
    /        squashfs (ro)
    /tmp     tmpfs (rw)
  Tools:     curl, wget, nc
```

Field label renames in architecture section for consistency:
- `SoC hint:` → `SoC:`
- `Toolchain hint:` → `Toolchain:`

## Field merge rules

Two fields can be set from multiple sources. Priority rules:

### DeviceModel

1. `parseCPUInfo()` — extracts `Hardware` field from `/proc/cpuinfo` (primary source)
2. `probeOS()` — reads `/proc/device-tree/model` as fallback
3. If cpuinfo already set `DeviceModel`, device-tree result is **appended** with comma: `"HiSilicon (HI3516EV200)"`
4. If cpuinfo didn't set it, device-tree result becomes the value directly

### LibcVersion

1. `parseLibc()` — extracts version from `ls -l` path (works for uClibc: `libuClibc-0.9.33.2.so`)
2. `probeOS()` — executes `/lib/libc.so.6` (works for glibc: prints version to stderr)
3. probeOS result **overrides** parseLibc result when more specific:
   - `parseLibc` returns "glibc" → probeOS returns "2.28" → final: "glibc 2.28"
   - `parseLibc` returns "uClibc 0.9.33.2" → probeOS returns "" → final: "uClibc 0.9.33.2"
   - Both empty → field stays empty

## Error handling

- `probeOS()` never returns an error
- Each command inside probeOS is best-effort: error → field stays empty, continue
- Connection loss mid-probeOS: fields collected so far are saved, rest stay empty
- Architecture detection is never affected by OS probe failures

## Testing

### Unit tests (`internal/detect/osprobe_test.go`)

| Test | What |
|---|---|
| `TestParseBusyBoxVersion` | First line extraction from multi-line busybox output |
| `TestParseDeviceModel` | Hardware field from cpuinfo |
| `TestParseUptime` | Seconds → "3d 12:45" formatting (0, 1h, 1d, mixed) |
| `TestParseNetTools` | ls output → []string of basenames |
| `TestParseLibcVersionUclibc` | Version from library path |
| `TestParseLibcVersionGlibc` | Version from .so execution stderr |
| `TestParseMemory` | MemTotal kB → MB string |
| `TestParseDiskUsage` | df output parsing |
| `TestParseMounts` | mount output → filtered []string |
| `TestFormatWithOS` | Full output includes OS section |
| `TestFormatWithoutOS` | Empty OS fields → OS section absent |

### Integration test

`tests/docker-compose.yaml` — extend existing telnetd test:
- Verify `detect` against the test container produces non-empty OS fields
- Makefile target: `test-integration-detect`

## Scope boundaries

**In scope:**
- BusyBox-based IP cameras and NVRs
- All existing architectures: ARM, AArch64, MIPS, x86, x86_64
- All existing libc families: glibc, uClibc, musl

**Out of scope:**
- Full Linux distro detection (non-BusyBox systems)
- Non-Linux OS (RTOS, VxWorks, bare metal)
- Package manager detection (opkg, dpkg)
- Running process list
- Network configuration (ip, ifconfig)
