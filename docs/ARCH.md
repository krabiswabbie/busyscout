# Fingerprinting a Camera/NVR for the Right Build

When a cross-compiled binary reports `not found` even though the file exists, or exits immediately with no output, it is built for the wrong CPU, libc, or ABI for that device. The kernel returns ENOENT for the missing **dynamic linker**, not for the binary itself, which is why `ls` shows the file but exec fails.

This guide lists the commands to run **on the camera** to determine which cross-compilation toolchain to use, or whether a new one needs to be prepared.

## What you need to identify

| Dimension | Affects build choice |
|---|---|
| ISA (ARM / MIPS / AArch64 / x86) | Toolchain family |
| Endianness (little / big) | `mipsel` vs `mips`, `armeb` vs `arm` |
| Word size (32 / 64 bit) | `armv7` vs `aarch64` |
| ARM sub-architecture (v5 / v6 / v7) | `armv5te` vs `armv7` build |
| Float ABI (soft / softfp / hard) | Hard-float vs soft-float build |
| libc (glibc / uClibc / musl) | Must match what the device ships |

All six must match — a single mismatch usually produces `not found` or a silent crash.

## Commands to run on the device

Camera firmwares vary in what utilities they ship. Run as many as work; the more output, the more confident the diagnosis.

### CPU and kernel

```sh
uname -a
cat /proc/cpuinfo
cat /proc/version
```

Look for: `system type`, `cpu model`, `Features` (e.g. `vfp`, `neon`, `half`, `thumb`), and the kernel architecture.

### ELF header of an existing device binary

If `file` is missing (common on stripped firmware), read the ELF header of any working binary on the device — `/bin/busybox` is almost always present:

```sh
file /bin/busybox                          # if available
od -An -t x1 -N20 /bin/busybox | head -1   # otherwise
```

Decode the `od` output:

| Byte | Meaning | Common values |
|---|---|---|
| 4 | ELF class | `01` = 32-bit, `02` = 64-bit |
| 5 | Endianness | `01` = little, `02` = big |
| 18–19 | `e_machine` | `28 00` ARM LE, `00 28` ARM BE, `08 00` MIPS LE, `00 08` MIPS BE, `b7 00` AArch64, `03 00` x86, `3e 00` x86_64 |

### libc family

```sh
ls -l /lib/libc.so* /lib/ld-*.so* /lib/ld-uClibc* /lib/ld-musl* 2>/dev/null
```

| Files seen | Conclusion |
|---|---|
| `libc.so.6`, `ld-linux-*.so.2` | glibc |
| `libuClibc-*.so`, `ld-uClibc.so.0` or `.1` | uClibc |
| `libc.musl-*.so.1`, `ld-musl-*.so.1` | musl |

### ARM float ABI and sub-arch

Only relevant for ARM targets:

```sh
readelf -A /bin/busybox 2>/dev/null | grep -E 'Tag_CPU_arch|Tag_ABI_VFP|Tag_FP_arch'
```

- `Tag_ABI_VFP_args: VFP registers` → hard-float
- absent or `Tag_ABI_VFP_args: AAPCS` → soft-float / softfp
- `Tag_CPU_arch: v5TE` / `v7` → pick a matching toolchain

Cross-check `/proc/cpuinfo` for `vfp`, `neon`, `half`, `thumb` flags.

### SoC hint (HiSilicon / Dahua-derived firmware)

```sh
cat /proc/hisi_chipid 2>/dev/null
ls /proc/umap 2>/dev/null
dmesg | grep -iE 'hisi|chip|soc' | head
```

A chip ID like `0x35180100` or directory names `hi3516`, `hi3536` tell you the SoC family directly.

### Confirming the kernel's actual error

If you still want to know **why** exec failed, these reveal the missing piece:

```sh
strace -f -e trace=execve ./<your-binary> 2>&1 | head -20
LD_TRACE_LOADED_OBJECTS=1 ./<your-binary>
ldd ./<your-binary>
```

`strace` will show the exact `ENOENT` on the interpreter path; `ldd`/`LD_TRACE_LOADED_OBJECTS` lists the `NEEDED` shared libraries.

## Mapping output to a build

Once you have CPU + endianness + word size + ARM sub-arch + float ABI + libc, select or prepare a matching toolchain. Naming conventions follow the pattern `<arch>-<vendor>-<os>-<libc><abi>`:

| Device fingerprint | Toolchain pattern |
|---|---|
| MIPS32 LE, uClibc, soft-float | `mipsel-*-linux-uclibc` |
| ARMv7 LE, glibc, hard-float | `armv7*-linux-gnueabihf` |
| ARMv5TE LE, uClibc, soft-float | `armv5te-*-linux-uclibcgnueabi` |
| AArch64 LE, musl | `aarch64-*-linux-musl` |
| ARMv7 LE, uClibc, hard-float | `arm-*-linux-uclibcgnueabihf` |
| MIPS32 LE, glibc, soft-float | `mipsel-*-linux-glibc` |

If no existing toolchain matches, prepare a new one targeting the identified CPU + libc + float ABI combination.

## Minimal info to ask a vendor for

When triaging an integration ticket, request these four outputs — together they're almost always enough:

```sh
uname -a
cat /proc/cpuinfo
ls -l /lib/libc.so* /lib/ld-*.so* 2>/dev/null
od -An -t x1 -N20 /bin/busybox | head -1
```
