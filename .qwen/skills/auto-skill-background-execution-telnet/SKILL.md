---
name: background-execution-telnet
description: Pattern for launching background processes on BusyBox devices via telnet — use sh -c with &, acknowledge that stdout is lost, and pair with connection deadlines
source: auto-skill
extracted_at: '2026-07-15T15:35:00.000Z'
---

# Background Execution via Telnet

BusyScout needs to start the fileloader on the device and then use the same telnet connection for other commands. The fileloader can't block the telnet prompt, so it's launched in the background.

## The Pattern

```go
cmd := fmt.Sprintf("%s push %s %d %s &", loaderPath, busyIP, port, remotePath)
tc.Execute("sh", "-c", cmd)
```

Using `sh -c '... &'` (as separate args to avoid shell issues) launches the process and returns the prompt immediately.

## Gotcha: Stdout/Stderr is Lost

When a process is backgrounded with `&` in a telnet session:
- Stdout and stderr go to the terminal, which is the telnet pty
- But BusyScout's `Execute()` returns immediately — it doesn't capture background output
- If the fileloader crashes, the error message disappears into the ether

**Implication:** BusyScout has ZERO visibility into whether the fileloader started successfully or crashed. Errors like "file not found", "segfault", "wrong architecture" are silently lost.

## Mitigation: Always Set Accept Deadlines

Since we can't detect fileloader failure via telnet, we must detect it via the TCP connection:

```go
// Set deadline — if fileloader doesn't connect within 5s, it failed
if tl, ok := ln.(*net.TCPListener); ok {
    tl.SetDeadline(time.Now().Add(5 * time.Second))
}
conn, err := ln.Accept()
if err != nil {
    return fmt.Errorf("fileloader on device failed to connect: %w", err)
}
```

This turns a silent hang into a clear error.

## Alternative (Future): Check Exit Status

If the device supports `$?`, we could check the exit status of the backgrounded process by querying it later. Most BusyBox shells reset `$?` after the next command, so this requires careful sequencing.

## When to Apply

Use `sh -c '... &'` pattern for:
- Launching fileloader on the device
- Any command that needs to keep running while BusyScout does other work

Always pair with:
- Accept deadline on the listener (5 seconds)
- Best-effort cleanup (`rm -f /tmp/bs-loader`)
