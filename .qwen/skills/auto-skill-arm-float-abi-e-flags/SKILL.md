---
name: arm-float-abi-e-flags
description: Detect ARM float ABI from ELF e_flags without readelf or helper — parse od hex dump bytes 36-39 for EF_ARM_ABI_FLOAT_HARD/SOFT
source: auto-skill
extracted_at: '2026-07-15T12:25:00.000Z'
---

# ARM Float ABI from ELF e_flags

On stripped BusyBox binaries without `readelf` and without section headers, ARM float ABI can still be determined from the ELF header's `e_flags` field — no helper upload needed.

## The Technique

ARM EABI stores float ABI in `e_flags` (32-bit word at offset 36 in ELF header):

| Flag | Value | Bit | Byte 37 check |
|------|-------|-----|---------------|
| EF_ARM_ABI_FLOAT_HARD | 0x400 | 10 | `flagsByte & 0x04 != 0` |
| EF_ARM_ABI_FLOAT_SOFT | 0x200 | 9 | `flagsByte & 0x02 != 0` |

## How to Get e_flags

Use `od -An -t x1 -N40 /bin/busybox` (40 bytes — covers e_flags at offset 36).

Then parse the hex dump:

```go
fields := strings.Fields(odOutput)
if len(fields) < 40 {
    return // output too short
}

// ... parse class, endianness, e_machine from bytes 4, 5, 18-19 ...

// ARM float ABI from e_flags byte 37
if fp.ISA == "arm" && fp.FloatABI == "" && fp.Endianness == "little" {
    var flagsByte byte
    fmt.Sscanf(fields[37], "%x", &flagsByte)
    if flagsByte&0x04 != 0 {
        fp.FloatABI = "hard"   // EF_ARM_ABI_FLOAT_HARD
    } else if flagsByte&0x02 != 0 {
        fp.FloatABI = "soft"   // EF_ARM_ABI_FLOAT_SOFT
    }
}
```

## Why This Works

- `e_flags` is always in the ELF header (not in section headers), so it survives stripping
- `od` is commonly available on BusyBox devices (unlike `readelf`)
- Only 40 bytes need to be transferred (vs ~8 KB helper binary)
- Works without knowing the device's libc (no helper ABI compatibility needed)

## Limitations

- Only works for ARM 32-bit little-endian
- Does not provide CPU sub-architecture version (v5/v6/v7) — that's still in `.ARM.attributes` section headers
- Requires `od` to be available on the device
