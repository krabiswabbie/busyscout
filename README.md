# BusyScout

BusyScout transfers files to and from BusyBox-based devices over telnet. It is intended for embedded systems such as IP cameras and NVRs that provide a telnet shell but no practical SSH, SCP, or FTP service.

BusyScout can:

- upload and download files with `push` and `pull`;
- choose automatically between fast reverse TCP transfer and a telnet-only fallback;
- identify the target CPU, ABI, libc, and OS profile with `detect`.

> **Security:** telnet does not encrypt credentials or session data. Use BusyScout only on a trusted or otherwise protected network.

## Installation

Download a workstation binary from [GitHub Releases](https://github.com/krabiswabbie/busyscout/releases/latest). Releases currently include Linux amd64, macOS amd64, and Windows amd64 binaries.

To build from source, see [Building from source](#building-from-source).

## Usage

The common target format is:

```text
user:pass@host[:port]:/path
```

The telnet port defaults to `23`. The colon before `/path` is required; IPv6 addresses must be enclosed in square brackets.

Examples:

```text
root:password@192.168.1.100:/tmp
root:password@192.168.1.100:2323:/tmp
root:password@[2001:db8::1]:/tmp
```

Credentials in the target may be visible in shell history or process listings.

### Commands

| Command | Usage | Description |
| --- | --- | --- |
| `push` | `busyscout push <local> <target>` | Upload a local file. If the remote path is a directory, the local filename is used. |
| `pull` | `busyscout pull <target> <local>` | Download a remote file to the specified local path. |
| `detect` | `busyscout detect <target>` | Identify the target architecture and collect a best-effort OS profile. |

Add `--verbose` to any command for connection and transfer details.

Typical commands:

```sh
busyscout push firmware.bin root:password@192.168.1.100:/tmp/
busyscout pull root:password@192.168.1.100:/tmp/config.db ./config.db
busyscout detect root:password@192.168.1.100:/
```

`detect` reports the architecture, word size, endianness, ARM float ABI when applicable, libc, toolchain hint, and available OS information. For example:

```text
Architecture:     ARMV7 (32-bit, little-endian)
Float ABI:        hard-float (VFP)
libc:             uClibc 1.0.31
Toolchain:        armv7-linux-uclibceabihf

OS Profile:
  Kernel:    Linux (none) 4.9.84 #1 PREEMPT Thu Dec 21 14:08:11 CST 2023 armv7l GNU/Linux
  BusyBox:   v1.20.2
  Device:    SStar Soc (Flattened Device Tree) (INFINITY6B0 SSC009A-S01A QFN88)
  Uptime:    10h 27m
  RAM:       41 MB
  Disk (/):  4.4M / 0 (100%)
  Mounts:
    / : squashfs,ro,relatime
    /tmp : tmpfs,rw,relatime
    /var : tmpfs,rw,relatime
    /mnt : squashfs,ro,relatime
  Tools:     wget
```

## Transfer modes

BusyScout selects the transfer method automatically:

- **Reverse TCP:** a small matching fileloader connects from the device back to BusyScout. This is the fast path when the device can reach the workstation.
- **Telnet fallback:** data is sent through the shell using `printf` and redirection. It is slower, but works when reverse connectivity is unavailable and does not depend on optional tools such as `base64`, `xxd`, or `nc`.

BusyScout adapts the loader transfer to command-length limits found on restricted BusyBox systems. Important files should be verified separately: BusyScout reports transfer errors but does not provide encryption or cryptographic integrity verification.

## Supported target platforms

The embedded helper variants cover:

| Platform | Coverage |
| --- | --- |
| ARM 32-bit (v5/v6/v7) | Little-endian; glibc, uClibc, and musl; applicable soft- and hard-float variants |
| AArch64 | Little-endian; glibc |
| MIPS 32-bit | Little- and big-endian; uClibc |
| x86 32-bit | Little-endian; glibc |
| x86_64 64-bit | Little-endian; glibc and musl |

The helper must match the device CPU, byte order, ABI, libc, and dynamic loader.

## Building from source

Requirements:

- Go 1.22.2 or newer;
- `make`;
- Docker to rebuild embedded helpers and fileloaders;
- Docker Compose for integration tests.

```sh
make local       # build a local binary with placeholder helpers
make local-full  # build a local binary with all embedded helpers
make build       # build release binaries in releases/
make all         # run the complete default pipeline
```

`make local` is sufficient for Go-level tests, but target operations require `make local-full`. Generated helper binaries are build artifacts and are not intended to be committed. See [`docs/V2.md`](docs/V2.md) for implementation details and design history.

## Testing

```sh
make test
make test-integration-detect
make test-integration-xfer-x86_64
make test-integration-xfer-aarch64
make test-integration-xfer-arm
```

The integration containers use `user:password` and expose telnet on ports `2323` through `2325`. The ARM test uses QEMU when necessary.

## Troubleshooting

If a target helper reports `not found` even though the file exists, the binary may have the wrong CPU, endianness, ARM sub-architecture, float ABI, libc, or dynamic linker. Start with:

```sh
uname -a
cat /proc/cpuinfo
cat /proc/version
ls -l /lib/libc.so* /lib/ld-*.so* /lib/ld-uClibc* /lib/ld-musl* 2>/dev/null
od -An -t x1 -N20 /bin/busybox | head -1
```

The target must allow telnet login and provide writable temporary storage for staging a helper or transfer file. OS fields are best-effort and may be empty on stripped firmware.

## License

[MIT License](LICENSE)
