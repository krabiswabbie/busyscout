# Target Format Simplification — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `:/path` optional in the target format — `detect` drops it, `push` defaults to `/tmp`, `pull` keeps it required.

**Architecture:** Three targeted changes: parser relaxed to accept missing `:/`, `New` adds `/tmp` default, `NewPull` rejects empty path. No new files, no new interfaces.

**Tech Stack:** Go 1.22.2+, standard library only (no new dependencies).

## Global Constraints

- Full backward compatibility: existing targets with `:/path` must continue working.
- No new flags, configs, or user-facing options.
- `/tmp` is the hardcoded default for push — no auto-detection of writable dirs.

---

### Task 1: Relax ParseRemoteFileName — `:/` optional

**Files:**
- Modify: `internal/scout/files.go:18-95`
- Modify: `internal/scout/files_test.go` (add cases, fix existing)

**Interfaces:**
- Produces: `ParseRemoteFileName` now returns `Path: ""` (not error) when `:/` absent.

- [ ] **Step 1: Add new test cases and fix the existing one**

In `internal/scout/files_test.go`, change the error case `{"login:pass@192.168.10.18", ...}` to a success case with empty path, and add new cases without `:/`:

```go
// OLD (error case — make it success):
// {"login:pass@192.168.10.18", "login", "pass", "192.168.10.18", "23", "", true},

// NEW — no :/ → empty path, no error:
{"login:pass@192.168.10.18", "login", "pass", "192.168.10.18", "23", "", false},
{"user@192.168.10.18", "user", "", "192.168.10.18", "23", "", false},
{"192.168.10.18", "", "", "192.168.10.18", "23", "", false},

// With port, no path:
{"login:pass@192.168.10.18:2323", "login", "pass", "192.168.10.18", "2323", "", false},

// IPv6, no path:
{"root:pass@[2001:db8::1]", "root", "pass", "[2001:db8::1]", "23", "", false},
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && go test ./internal/scout/ -run TestParseRemoteFileName -v
```

Expected: FAIL — `"login:pass@192.168.10.18"` still returns error "missing path separator".

- [ ] **Step 3: Rewrite ParseRemoteFileName to make `:/` optional**

In `internal/scout/files.go`, replace the hostPart/path extraction block:

```go
// OLD:
hostEnd := strings.Index(input, ":/")
if hostEnd == -1 {
    return nil, errors.New("invalid format: missing path separator")
}

hostPart := input[:hostEnd]
path = input[hostEnd+1:]

// NEW:
hostPart := input
path = ""
if hostEnd := strings.Index(input, ":/"); hostEnd != -1 {
    hostPart = input[:hostEnd]
    path = input[hostEnd+1:]
}
```

Also remove the now-unnecessary path validation at the bottom of the function:

```go
// REMOVE this block:
if strings.Count(path, ":") > 0 {
    return nil, errors.New("invalid path format")
}
```

The path validation is no longer needed because when `:/` is present, the parser already split correctly, and when absent, path is empty — no risk of colons in path.

- [ ] **Step 4: Run test to verify it passes**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && go test ./internal/scout/ -run TestParseRemoteFileName -v
```

Expected: PASS.

- [ ] **Step 5: Run full scout package tests**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && go test ./internal/scout/ -v
```

Expected: PASS (existing tests should not break).

- [ ] **Step 6: Commit**

```bash
git add internal/scout/files.go internal/scout/files_test.go
git commit -m "feat: make :/path optional in ParseRemoteFileName"
```

---

### Task 2: Default push path to /tmp

**Files:**
- Modify: `internal/scout/scout.go:37-65` (New function)

**Interfaces:**
- Consumes: `ParseRemoteFileName` returning empty Path from Task 1.
- Produces: `New` fills `remote.Path = "/tmp/<filename>"` when empty.

- [ ] **Step 1: Add /tmp default in New**

In `internal/scout/scout.go`, in the `New` function, after `ParseRemoteFileName` and before `checkIsRemoteDirectory`, add:

```go
// Default to /tmp if no remote path specified
if remote.Path == "" {
    remote.Path = "/tmp/" + filepath.Base(source)
}
```

The exact insertion point — after line ~52 (`remote: remote,`) and before the `checkIsRemoteDirectory` block:

```go
s := &Scout{
    localFile: source,
    remote:    remote,
    verbose:   verboseFlag,
}

// Default to /tmp if no remote path specified
if remote.Path == "" {
    remote.Path = "/tmp/" + filepath.Base(source)
}

// Add the target filename if only target directory is specified
isDir, errDir := s.checkIsRemoteDirectory(remote.Path)
```

- [ ] **Step 2: Verify build**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && go build ./...
```

Expected: no errors.

- [ ] **Step 3: Run scout package tests**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && go test ./internal/scout/ -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/scout/scout.go
git commit -m "feat: default push path to /tmp when not specified"
```

---

### Task 3: Reject empty path in NewPull

**Files:**
- Modify: `internal/scout/scout.go:433-444` (NewPull function)

**Interfaces:**
- Consumes: `ParseRemoteFileName` returning empty Path from Task 1.
- Produces: `NewPull` returns error when Path is empty.

- [ ] **Step 1: Add path check in NewPull**

In `internal/scout/scout.go`, in `NewPull`, after `ParseRemoteFileName`:

```go
func NewPull(target string, verboseFlag bool) (*Scout, error) {
    remote, err := ParseRemoteFileName(target)
    if err != nil {
        return nil, errorx.Decorate(err, "failed to parse remote address")
    }

    if remote.Path == "" {
        return nil, errors.New("pull requires a remote file path")
    }

    return &Scout{
        remote:  remote,
        verbose: verboseFlag,
    }, nil
}
```

Note: `errors` is already imported in scout.go.

- [ ] **Step 2: Verify build**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && go build ./...
```

Expected: no errors.

- [ ] **Step 3: Run scout package tests**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && go test ./internal/scout/ -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/scout/scout.go
git commit -m "feat: reject empty path in NewPull"
```

---

### Task 4: Update CLI usage text

**Files:**
- Modify: `main.go:25-36` (printUsage), `main.go:54-57` (cmdPush usage), `main.go:68-71` (cmdDetect usage)

- [ ] **Step 1: Update printUsage**

```go
func printUsage() {
    fmt.Println(`BusyScout — push/pull files to embedded devices (IP cameras, NVR) via telnet.

Usage:
  busyscout push <local> user:pass@host[:port][:/path] [--verbose]
  busyscout pull user:pass@host[:port]:/path <local> [--verbose]
  busyscout detect user:pass@host[:port] [--verbose]

Mode selection is automatic:
  Same subnet → fast TCP (~6-8 KB loader + line-speed transfer)
  Different subnet → printf over telnet (slower but NAT-safe)`)
}
```

- [ ] **Step 2: Update cmdPush usage**

```go
fmt.Println("Usage: busyscout push <local> user:pass@host[:port][:/path] [--verbose]")
```

- [ ] **Step 3: Update cmdDetect usage**

```go
fmt.Println("Usage: busyscout detect user:pass@host[:port] [--verbose]")
```

- [ ] **Step 4: Verify build**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && go build ./...
```

Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add main.go
git commit -m "docs: update CLI usage text for optional path format"
```

---

### Task 5: Update README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update target format description**

Change:
```markdown
The common target format is:

```text
user:pass@host[:port]:/path
```

The telnet port defaults to `23`. The colon before `/path` is required; IPv6 addresses must be enclosed in square brackets.
```

To:
```markdown
The common target format is:

```text
user:pass@host[:port][:/path]
```

The telnet port defaults to `23`. The `:/path` suffix is optional — `push` defaults to `/tmp`, `detect` ignores it, `pull` requires it. IPv6 addresses must be enclosed in square brackets.
```

- [ ] **Step 2: Update examples**

Change:
```text
root:password@192.168.1.100:/tmp
root:password@192.168.1.100:2323:/tmp
root:password@[2001:db8::1]:/tmp
```

To:
```text
root:password@192.168.1.100            # push (→ /tmp), detect
root:password@192.168.1.100:2323       # with custom port
root:password@192.168.1.100:/tmp       # explicit path (still works)
root:password@[2001:db8::1]:/tmp       # IPv6
```

- [ ] **Step 3: Update command examples**

Change:
```sh
busyscout push firmware.bin root:password@192.168.1.100:/tmp/
busyscout pull root:password@192.168.1.100:/tmp/config.db ./config.db
busyscout detect root:password@192.168.1.100:/
```

To:
```sh
busyscout push firmware.bin root:password@192.168.1.100
busyscout pull root:password@192.168.1.100:/tmp/config.db ./config.db
busyscout detect root:password@192.168.1.100
```

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: update README for optional path format"
```

---

### Task 6: Final verification

- [ ] **Step 1: Run all unit tests**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && go test ./... -v
```

Expected: all PASS.

- [ ] **Step 2: Build local binary**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && make local
```

Expected: build succeeds.

- [ ] **Step 3: Verify CLI help output**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && ./busyscout --help
```

Expected: updated usage text with optional path format.

- [ ] **Step 4: Verify detect help**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && ./busyscout detect 2>&1 || true
```

Expected: `Usage: busyscout detect user:pass@host[:port] [--verbose]`

- [ ] **Step 5: Verify push help**

```bash
cd /Users/eafilin/Home/ipeye/busyscout && ./busyscout push 2>&1 || true
```

Expected: `Usage: busyscout push <local> user:pass@host[:port][:/path] [--verbose]`
