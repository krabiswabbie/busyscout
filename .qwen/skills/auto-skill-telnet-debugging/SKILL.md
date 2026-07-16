---
name: telnet-debugging
description: How to debug detect/telnet command output issues — compare raw output with direct telnet session
source: auto-skill
extracted_at: '2026-07-15T12:00:00.000Z'
---

# Debugging Telnet Command Output in BusyScout

When `detect` produces wrong or incomplete output, trace the raw telnet data.

## Process

1. **Run with --verbose** — redirect stderr to a file:
   ```bash
   go run . detect user:pass@host:/tmp --verbose 2>/tmp/debug.log
   ```

2. **Check output sizes** — grep for `Received data with size`:
   ```bash
   grep "Received data" /tmp/debug.log
   ```
   - Size 0 = command produced no output (not available, or sh -c broken)
   - Size < 10 = something went wrong (3 bytes usually means empty file)
   - Compare sizes between commands — if some are large and others 0, the device has those features

3. **Cross-reference with direct telnet** — connect manually and run the same commands:
   ```
   telnet host
   uname -a
   cat /proc/cpuinfo
   ...
   ```
   Compare what the device actually returns vs what detect received.

4. **Check for truncation** — if output is shorter than expected (e.g., `uname -a` gives 21 bytes instead of 80+), suspect prompt detection false positive. Look for `#` or `$` characters in the middle of the expected output.

5. **Missing "Received data" lines** — if a command has no corresponding "Received data" log line, the command likely failed silently. Check:
   - Is `sh -c` properly quoted? (use `sh -c 'cmd'` as a single string, not split args)
   - Does the command exist on the device?
   - Is `/proc` mounted?

## Root Cause Categories

| Symptom | Likely Cause |
|---------|-------------|
| Output truncated at `#` | Prompt regex matches `#` in command output — check `ReadUntilBanner` for `$`/`#` in middle of text, not just at end |
| No "Received data" for sh -c commands | sh -c arguments not properly quoted — use single string: `tc.Execute("sh -c 'cmd'")` |
| Empty output for /proc files | /proc not mounted or file doesn't exist |
| Helper upload but Float ABI missing | Helper compiled for wrong libc, section headers stripped, or timeout — add e_flags check via `od -N40` (see auto-skill-arm-float-abi-e-flags) |
| Float ABI [uncertain] without helper | Extend `od` to read 40 bytes and parse e_flags at offset 36 — EF_ARM_ABI_FLOAT_HARD/SOFT flags in byte 37 |
| Device model has `~` and ` #` artifacts | Device-tree model contains null bytes — cut at first `\x00`: `strings.IndexByte(model, 0)` |
| `uname -a` truncated at `#1` | Prompt detection: `#` in `#1 PREEMPT` matched as shell prompt — use last-char check |
| Phase 2 upload timeout | Device connection unstable — make Phase 2 best-effort (non-fatal), return Phase 1 + OS probe results |
| `df -h` shows `0B` available | `formatSize1K(0)` returns `"0B"` — return `"0"` instead for zero blocks |
