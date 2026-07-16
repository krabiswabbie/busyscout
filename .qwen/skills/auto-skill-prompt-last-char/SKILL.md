---
name: prompt-detection-last-char
description: Fix telnet prompt false positives — use last-char check (#/$ as final non-whitespace) instead of bare regex to avoid matching # in command output
source: auto-skill
extracted_at: '2026-07-15T12:11:00.000Z'
---

# Prompt Detection: Last-Char Check vs Regex

BusyScout's telnet client reads command output until it sees the shell prompt. The original `ReadUntilBanner` used regex `$|#` which matched these characters ANYWHERE in the output, causing false positives.

## Problem

Regex `$|#` matched `#` in kernel version (`#1 PREEMPT`), truncating `uname -a` output:

```
Linux (none) 4.9.84 #1 PREEMPT Thu Dec 21 ... armv7l GNU/Linux
                     ^^ regex matched here — output truncated
```

This cascaded: truncated uname → ISA not detected → needed Phase 2, truncated /proc/version → KernelBuild empty, etc.

## Solution: Last-Char Check

Replace regex with a check on the **last non-whitespace character** of each chunk. A real shell prompt always ends with `#` (root) or `$` (user):

```go
// BEFORE (broken)
var bannerRe = regexp.MustCompile("\\$|#")
func (tc *TelnetClient) ReadUntilBanner() (output []byte, err error) {
    output, err = tc.ReadUntilPrompt(func(data []byte) bool {
        m := bannerRe.Find(data)
        return len(m) > 0
    })
    output = bannerRe.ReplaceAll(output, []byte{}) // strips ALL # and $
    output = bytes.Trim(output, " ")
    return
}

// AFTER (fixed)
func (tc *TelnetClient) ReadUntilBanner() (output []byte, err error) {
    output, err = tc.ReadUntilPrompt(func(data []byte) bool {
        trimmed := bytes.TrimSpace(data)
        if len(trimmed) == 0 {
            return false
        }
        last := trimmed[len(trimmed)-1]
        return last == '#' || last == '$'
    })
    // Remove only the prompt line (from last \n to end)
    if idx := bytes.LastIndex(output, []byte{'\n'}); idx >= 0 {
        output = output[:idx]
    }
    output = bytes.TrimRight(output, " \r\n")
    return
}
```

## Key Differences

| Aspect | Regex `$`&#124;`#` | Last-char check |
|--------|------|-----------------|
| `#1 PREEMPT` chunk | ❌ Matches `#` | ✅ Last char is `T` — not a prompt |
| `~ # ` (real prompt) | ✅ Matches | ✅ Last char is `#` — prompt |
| Output cleanup | Strip ALL `#`/`$` | Strip only prompt line |

## Apply to Both Methods

Same fix needed in `waitWelcomeSigns()` for auth phase — it also used `bannerRe.Find()`.
