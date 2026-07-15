package detect

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/krabiswabbie/busyscout/internal/telnet"
)

// probeOS collects OS-level information from the target device.
// All commands are best-effort — failures leave fields empty.
// probeOS never returns an error.
func probeOS(tc *telnet.TelnetClient, fp *Fingerprint) {
	// BusyBox version
	if out, err := tc.Execute("busybox"); err == nil && len(out) > 0 {
		fp.BusyBoxVersion = parseBusyBoxVersion(string(out))
	}

	// Device-tree model (supplements cpuinfo Hardware).
	// DT properties are null-terminated — cut at first \x00.
	if out, err := tc.Execute("cat", "/proc/device-tree/model"); err == nil && len(out) > 0 {
		model := string(out)
		if idx := strings.IndexByte(model, 0); idx >= 0 {
			model = model[:idx]
		}
		model = strings.TrimSpace(model)
		if model != "" {
			if fp.DeviceModel != "" {
				fp.DeviceModel += " (" + model + ")"
			} else {
				fp.DeviceModel = model
			}
		}
	}

	// Glibc version via .so execution
	if out, err := tc.Execute("sh -c '/lib/libc.so.6 2>&1 || true'"); err == nil && len(out) > 0 {
		ver := parseGlibcVersion(string(out))
		if ver != "" {
			if fp.LibcVersion == "" || fp.LibcVersion == "glibc" {
				fp.LibcVersion = "glibc " + ver
			}
		}
	}

	// /proc/meminfo → TotalRAM
	if out, err := tc.Execute("cat", "/proc/meminfo"); err == nil && len(out) > 0 {
		fp.TotalRAM = parseMeminfoTotal(string(out))
	}

	// df — use -h for human-readable; fallback to raw 1K-blocks if -h unsupported
	if out, err := tc.Execute("sh -c 'df -h / 2>/dev/null || df -h 2>/dev/null'"); err == nil && len(out) > 0 {
		fp.RootFSUsage = parseDFRoot(string(out))
	}

	// mounts — try /proc/mounts first (correct space-separated format),
	// fall back to `mount` (different format: "device on / type fstype (flags)")
	if out, err := tc.Execute("sh -c 'cat /proc/mounts 2>/dev/null || mount'"); err == nil && len(out) > 0 {
		fp.Mounts = parseMountsFiltered(string(out))
	}

	// /proc/uptime
	if out, err := tc.Execute("cat", "/proc/uptime"); err == nil && len(out) > 0 {
		fp.Uptime = parseUptimeSeconds(string(out))
	}

	// Net tools
	if out, err := tc.Execute(
		"sh -c 'ls /usr/bin/curl /usr/bin/wget /bin/nc /usr/sbin/nc /usr/bin/openssl /usr/bin/tftp /usr/bin/ftpget /usr/bin/ncat 2>/dev/null'",
	); err == nil && len(out) > 0 {
		fp.NetTools = parseNetTools(string(out))
	}
}

// parseBusyBoxVersion extracts version from the first line of busybox output.
// First line format: "BusyBox v1.31.1 (2020-01-15 10:00:00 CST) multi-call binary."
func parseBusyBoxVersion(output string) string {
	idx := strings.Index(output, "\n")
	if idx == -1 {
		idx = len(output)
	}
	first := strings.TrimSpace(output[:idx])

	re := regexp.MustCompile(`BusyBox\s+v([\d.]+)`)
	if m := re.FindStringSubmatch(first); m != nil {
		return "v" + m[1]
	}
	return ""
}

// parseGlibcVersion extracts version from glibc .so execution stderr.
// Output example: "GNU C Library (GNU libc) stable release version 2.28."
func parseGlibcVersion(output string) string {
	re := regexp.MustCompile(`version\s+([\d.]+)`)
	if m := re.FindStringSubmatch(output); m != nil {
		return m[1]
	}
	return ""
}

// parseMeminfoTotal extracts MemTotal and formats as "NNN MB".
// Input: "MemTotal:         128456 kB"
func parseMeminfoTotal(output string) string {
	re := regexp.MustCompile(`MemTotal:\s*(\d+)\s*kB`)
	if m := re.FindStringSubmatch(output); m != nil {
		var kb int
		if _, err := fmt.Sscanf(m[1], "%d", &kb); err != nil {
			return ""
		}
		mb := int(math.Round(float64(kb) / 1024.0))
		if mb < 1 {
			mb = 1
		}
		return fmt.Sprintf("%d MB", mb)
	}
	return ""
}

// parseDFRoot extracts usage info for root filesystem.
// Handles both "df /" and full "df" output — takes the second line (first data row).
//
// With -h flag: columns are human-readable (e.g. "12M", "8.1M").
// Without -h: columns are raw 1K-blocks — converted to human-readable in parser.
func parseDFRoot(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		return ""
	}
	// Second line is the first data row
	data := strings.Fields(lines[1])
	if len(data) < 4 {
		return ""
	}
	// Typical columns: Filesystem Size/1K-blocks Used Available Use% Mounted
	used := data[2]
	avail := data[3]
	pct := ""
	if len(data) >= 5 {
		pct = data[4]
	}
	// If values have no size suffix (no trailing letter), they are raw 1K-blocks
	if !hasSizeSuffix(used) {
		used = formatSize1K(used)
	}
	if !hasSizeSuffix(avail) {
		avail = formatSize1K(avail)
	}
	if pct != "" {
		return fmt.Sprintf("%s / %s (%s)", used, avail, pct)
	}
	return fmt.Sprintf("%s / %s", used, avail)
}

// hasSizeSuffix reports whether s ends with a size suffix letter (K, M, G, T, etc.).
func hasSizeSuffix(s string) bool {
	if len(s) == 0 {
		return false
	}
	last := s[len(s)-1]
	return (last >= 'A' && last <= 'Z') || (last >= 'a' && last <= 'z')
}

// formatSize1K converts a raw 1K-block count string (e.g. "8304") to human-readable (e.g. "8M").
func formatSize1K(raw string) string {
	var blocks int64
	if _, err := fmt.Sscanf(raw, "%d", &blocks); err != nil {
		return raw // return as-is on parse failure
	}
	if blocks == 0 {
		return "0"
	}
	bytes := blocks * 1024
	switch {
	case bytes >= 1073741824: // 1 GiB
		return fmt.Sprintf("%dG", bytes/1073741824)
	case bytes >= 1048576: // 1 MiB
		return fmt.Sprintf("%dM", bytes/1048576)
	case bytes >= 1024: // 1 KiB
		return fmt.Sprintf("%dK", bytes/1024)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

// parseMountsFiltered filters mount output to interesting mount points.
// Keeps: /, /tmp, /var, /mnt, /config, /data, /home, /system.
// Format: "/tmp : tmpfs,rw"
//
// Handles two input formats:
//   - /proc/mounts: "device mountpoint fstype options ..." (space-separated)
//   - mount command: "device on mountpoint type fstype (flags)" (with " on " and " type ")
func parseMountsFiltered(output string) []string {
	interesting := map[string]bool{
		"/": true, "/tmp": true, "/var": true, "/mnt": true,
		"/config": true, "/data": true, "/home": true, "/system": true,
	}

	var result []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		var mountPoint, fstype, flags string

		if strings.Contains(line, " on ") && strings.Contains(line, " type ") {
			// mount command format: "rootfs on / type rootfs (rw,relatime)"
			fields := strings.Fields(line)
			if len(fields) < 6 {
				continue
			}
			// fields: [device, "on", mountpoint, "type", fstype, "(flags)"]
			mountPoint = fields[2]
			fstype = fields[4]
			flags = strings.TrimPrefix(fields[5], "(")
			flags = strings.TrimSuffix(flags, ")")
		} else {
			// /proc/mounts format: "device mountpoint fstype options ..."
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			mountPoint = fields[1]
			fstype = fields[2]
			if len(fields) >= 4 {
				flags = fields[3]
			}
		}

		if _, ok := interesting[mountPoint]; ok {
			if flags != "" {
				result = append(result, fmt.Sprintf("%s : %s,%s", mountPoint, fstype, flags))
			} else {
				result = append(result, fmt.Sprintf("%s : %s", mountPoint, fstype))
			}
		}
	}
	return result
}

// parseUptimeSeconds converts /proc/uptime first field (seconds) to human-readable.
// Input: "123456.78 98765.43"
// Output: "1d 10:17"
func parseUptimeSeconds(output string) string {
	fields := strings.Fields(strings.TrimSpace(output))
	if len(fields) < 1 {
		return ""
	}
	var secs float64
	fmt.Sscanf(fields[0], "%f", &secs)
	if secs < 1 {
		return ""
	}

	totalSecs := int64(secs)
	days := totalSecs / 86400
	hours := (totalSecs % 86400) / 3600
	minutes := (totalSecs % 3600) / 60

	if days > 0 {
		return fmt.Sprintf("%dd %02d:%02d", days, hours, minutes)
	}
	return fmt.Sprintf("%d:%02d", hours, minutes)
}

// parseNetTools extracts basenames of existing network tools from ls output.
// Input: "/usr/bin/curl\n/usr/bin/wget\n/bin/nc\n"
// Output: ["curl", "wget", "nc"]
func parseNetTools(output string) []string {
	var tools []string
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		// Extract basename
		idx := strings.LastIndex(path, "/")
		if idx < 0 {
			continue
		}
		name := path[idx+1:]
		if !seen[name] {
			seen[name] = true
			tools = append(tools, name)
		}
	}
	return tools
}
