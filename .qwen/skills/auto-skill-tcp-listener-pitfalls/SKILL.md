---
name: tcp-listener-pitfalls
description: Common pitfalls with TCP listeners in BusyScout's fileloader pattern — bind to all interfaces, not loopback; always set accept deadlines
source: auto-skill
extracted_at: '2026-07-15T15:32:04.224Z'
---

# TCP Listener Pitfalls for Device Communication

When BusyScout starts a TCP listener that a remote device (fileloader) connects to, two non-obvious bugs consistently appear.

## 1. Bind to `127.0.0.1` — Device Can't Connect

### The Bug

```go
// BROKEN — fileloader on device can't reach this
ln, err := net.Listen("tcp", "127.0.0.1:0")
```

`127.0.0.1:0` **always succeeds** (loopback always exists), so the fallback to `:0` never triggers. The listener is only reachable from localhost, not from the LAN IP that the device connects to.

### The Fix

```go
// Bind to all interfaces — works for both local tests and remote devices
ln, err := net.Listen("tcp", ":0")
```

**Why it works:** `:0` listens on all interfaces (including loopback). Local tests connect via `127.0.0.1:<port>`, remote devices connect via the LAN IP (e.g., `192.168.1.5:<port>`).

**Why it's easy to miss:** Unit tests always pass because the test client connects via `127.0.0.1`, which IS reachable when bound to `127.0.0.1`. The bug only manifests in real LAN scenarios.

### Rule

When a listener needs to accept connections from a remote device — **always use `:0`**, never `127.0.0.1`. Local tests will still work through loopback.

## 2. No Deadline on `Accept()` — Hangs Forever

### The Bug

```go
conn, err := ln.Accept()  // Blocks forever if device never connects
```

If the fileloader binary doesn't start on the device (wrong ISA, missing libc, corrupted binary, permission denied), `tc.Execute("sh -c '... &'")` returns immediately (due to `&`), but Accept() blocks forever. The user sees no error, no progress — just a hang.

### The Fix

Set a deadline on the listener before Accept():

```go
// Give the device 5 seconds to connect
if tl, ok := ln.(*net.TCPListener); ok {
    tl.SetDeadline(time.Now().Add(5 * time.Second))
}

conn, err := ln.Accept()
if err != nil {
    // Timeout or other error — provides clear diagnostic
    return fmt.Errorf("device did not connect: %w", err)
}
```

**Why it works:** After 5 seconds, Accept() returns a timeout error. BusyScout can clean up (close listener, remove /tmp/bs-loader) and report a clear error message.

**Interface note:** `net.Listener` doesn't have `SetDeadline` — only `*net.TCPListener` does. Use a type assertion.

## When to Apply

Always apply both fixes when creating a listener for the fileloader pattern:
1. Bind to `:0` (all interfaces)
2. Set accept deadline (5 seconds recommended)

These apply to: `StartListener()`, `AcceptAndPush()`, `AcceptAndPull()`, and any future listener-based communication with remote devices.
