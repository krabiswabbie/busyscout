# Target format simplification

**Date:** 2026-07-22
**Status:** design

## Motivation

Current target format `user:pass@host[:port]:/path` requires the `:/path` suffix for every command, even when the path is unused (`detect`) or always the same (`push` to `/tmp` on IP cameras).

On IP cameras, `/tmp` is the only writable directory (rootfs is squashfs). Requiring users to type `:/tmp` every time is ceremony, not utility.

## Design

### New target format

`user:pass@host[:port][:/path]`

- `:/path` is optional. When omitted, `Path` is empty.
- Existing targets with `:/path` continue to work — full backward compatibility.

### Command behavior

| Command | Path omitted | Path provided |
|---------|-------------|---------------|
| `detect` | OK (path ignored) | OK (path ignored, as before) |
| `push` | Defaults to `/tmp/<filename>` | Used as-is |
| `pull` | **Error**: path required | Used as-is |

### Usage examples

```text
# detect — no path needed
busyscout detect root:pass@192.168.1.100
busyscout detect root:pass@192.168.1.100:2323

# push — /tmp by default
busyscout push firmware.bin root:pass@192.168.1.100
busyscout push firmware.bin root:pass@192.168.1.100:2323

# push — explicit path override (edge case)
busyscout push firmware.bin root:pass@192.168.1.100:/var/tmp

# pull — path required (it identifies the remote file)
busyscout pull root:pass@192.168.1.100:/tmp/config.db ./config.db
```

### Code changes

1. **`internal/scout/files.go` — `ParseRemoteFileName`:**
   - When `:/` is not found, set `Path = ""` instead of returning an error.
   - Remove `"invalid format: missing path separator"` error.

2. **`internal/scout/scout.go` — `New` (push):**
   - After parsing: if `remote.Path == ""` → `remote.Path = "/tmp/" + filepath.Base(source)`.
   - Existing directory-detection logic (`checkIsRemoteDirectory`) is preserved.

3. **`internal/scout/scout.go` — `NewPull`:**
   - After parsing: if `remote.Path == ""` → return error `"pull requires a remote file path"`.

4. **`main.go` — usage text:**
   - `detect`: remove `/path` from usage line.
   - `push`: mark path as optional.

5. **`README.md` — Usage section:**
   - Update format description and examples.

### Tests

- `ParseRemoteFileName` with target without `:/` → empty `Path`, no error.
- `New` with target without path → defaults to `/tmp/<filename>`.
- `NewPull` with target without path → returns error.

### Non-goals

- Changing the `:/` separator itself (backward compatibility).
- Auto-detecting writable directories on the target.
- Adding a config file or flag for default path.
