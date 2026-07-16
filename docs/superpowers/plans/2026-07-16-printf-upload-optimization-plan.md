# Printf Upload Auto-Fallback — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Speed up fileloader upload by trying larger printf chunk sizes (1024→512→256→128) with automatic fallback.

**Architecture:** `UploadData()` gains a `lineSize` parameter. `xfer.Push()` and `xfer.Pull()` wrap their fileloader upload call in a fallback loop that creates a fresh telnet connection per attempt. Existing callers pass `DefaultLineSize = 128`.

**Tech Stack:** Go 1.x, telnet, printf shell commands

## Global Constraints

- Octal escapes only (`\NNN`), never hex (`\xNN`) — BusyBox uClibc compatibility
- `sh -c` wrapping kept per command (no batching)
- No caching of working lineSize — one-shot per session
- `DefaultLineSize = 128` exported for backward compatibility

---

### Task 1: Parameterize UploadData with lineSize

**Files:**
- Modify: `internal/helpers/upload.go:1-38`
- Modify: `internal/detect/phase2.go:40`
- Modify: `internal/scout/scout.go:290`

**Interfaces:**
- Produces: `func UploadData(tc *telnet.TelnetClient, data []byte, targetFileName string, lineSize int) error`
- Produces: `const DefaultLineSize = 128`

- [ ] **Step 1: Update UploadData signature and add DefaultLineSize constant**

Edit `internal/helpers/upload.go`:

```go
package helpers

import (
	"fmt"
	"strings"

	"github.com/krabiswabbie/busyscout/internal/telnet"
)

// UploadData sends binary data to a remote file via printf over an already-open telnet connection.
// The caller owns the connection lifecycle (Dial/Close).
func UploadData(tc *telnet.TelnetClient, data []byte, targetFileName string, lineSize int) error {
	targetFileName = toUnixPath(targetFileName)
	redirectMode := ">"

	for i := 0; i < len(data); i += lineSize {
		end := i + lineSize
		if end > len(data) {
			end = len(data)
		}

		cmd := "printf '"
		for _, bt := range data[i:end] {
			cmd += fmt.Sprintf("\\%03o", bt)
		}
		cmd += fmt.Sprintf("' %s %s\n", redirectMode, targetFileName)
		redirectMode = ">>"

		if _, err := tc.Execute(cmd); err != nil {
			return err
		}
	}

	return nil
}

// DefaultLineSize is the safe printf chunk size for unknown devices.
const DefaultLineSize = 128
```

- [ ] **Step 2: Update detect/phase2.go caller**

Edit `internal/detect/phase2.go`, line 40 — add `helpers.DefaultLineSize`:

```go
	// Upload helper binary
	if err := helpers.UploadData(tc, helperData, helperRemotePath, helpers.DefaultLineSize); err != nil {
		return errorx.Decorate(err, "failed to upload helper binary")
	}
```

- [ ] **Step 3: Update scout.go caller**

Edit `internal/scout/scout.go`, line 290 — add `helpers.DefaultLineSize`:

```go
	if errSend := helpers.UploadData(tc, data, toUnixPath(targetFileName), helpers.DefaultLineSize); errSend != nil {
		return 0, errSend
	}
```

- [ ] **Step 4: Build and run unit tests**

Run: `make test`
Expected: PASS (all existing callers compile with new signature)

- [ ] **Step 5: Commit**

```bash
git add internal/helpers/upload.go internal/detect/phase2.go internal/scout/scout.go
git commit -m "refactor: parameterize UploadData with lineSize, export DefaultLineSize=128"
```

---

### Task 2: Auto-fallback loop in xfer.Push()

**Files:**
- Modify: `internal/xfer/push.go:14-30`

**Interfaces:**
- Consumes: `helpers.UploadData(tc, data, targetFileName, lineSize)` (from Task 1), `helpers.DefaultLineSize = 128`
- Produces: (no new exports — internal change)

- [ ] **Step 1: Wrap fileloader upload with fallback loop**

Edit `internal/xfer/push.go` — replace lines 20-25 (the "Upload fileloader via printf" block):

```go
	// 2. Upload fileloader via printf with auto-fallback chunk size
	var uploadErr error
	for _, lineSize := range []int{1024, 512, 256, 128} {
		uploadErr = helpers.UploadData(tc, loader, loaderPath, lineSize)
		if uploadErr == nil {
			break
		}
	}
	if uploadErr != nil {
		return errorx.Decorate(uploadErr, "failed to upload fileloader")
	}
```

- [ ] **Step 2: Build and run tests**

Run: `make test`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/xfer/push.go
git commit -m "feat(xfer): auto-fallback lineSize for fileloader upload in Push"
```

---

### Task 3: Auto-fallback loop in xfer.Pull()

**Files:**
- Modify: `internal/xfer/pull.go:20-27`

**Interfaces:**
- Consumes: `helpers.UploadData(tc, data, targetFileName, lineSize)` (from Task 1)
- Produces: (no new exports — internal change)

- [ ] **Step 1: Wrap fileloader upload with fallback loop**

Edit `internal/xfer/pull.go` — replace lines 22-27 (the "Upload fileloader via printf" block):

```go
	// 2. Upload fileloader via printf with auto-fallback chunk size
	var uploadErr error
	for _, lineSize := range []int{1024, 512, 256, 128} {
		uploadErr = helpers.UploadData(tc, loader, loaderPath, lineSize)
		if uploadErr == nil {
			break
		}
	}
	if uploadErr != nil {
		return errorx.Decorate(uploadErr, "failed to upload fileloader")
	}
```

- [ ] **Step 2: Build and run tests**

Run: `make test`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/xfer/pull.go
git commit -m "feat(xfer): auto-fallback lineSize for fileloader upload in Pull"
```
