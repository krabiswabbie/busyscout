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

	// Device-tree model (supplements cpuinfo Hardware)
	if out, err := tc.Execute("cat", "/proc/device-tree/model"); err == nil && len(out) > 0 {
		model := strings.TrimSpace(string(out))
		if model != "" {
			if fp.DeviceModel != "" {
				fp.DeviceModel += " (" + model + ")"
			} else {
				fp.DeviceModel = model
			}
		}
	}

	// Glibc version via .so execution
	if out, err := tc.Execute("sh", "-c", "/lib/libc.so.6 2>&1 || true"); err == nil && len(out) > 0 {
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

	// df /
	if out, err := tc.Execute("sh", "-c", "df / 2>/dev/null || df 2>/dev/null"); err == nil && len(out) > 0 {
		fp.RootFSUsage = parseDFRoot(string(out))
	}

	// mounts
	if out, err := tc.Execute("sh", "-c", "mount 2>/dev/null || cat /proc/mounts"); err == nil && len(out) > 0 {
		fp.Mounts = parseMountsFiltered(string(out))
	}

	// /proc/uptime
	if out, err := tc.Execute("cat", "/proc/uptime"); err == nil && len(out) > 0 {
		fp.Uptime = parseUptimeSeconds(string(out))
	}

	// Net tools
	if out, err := tc.Execute(
		"sh", "-c",
		"ls /usr/bin/curl /usr/bin/wget /bin/nc /usr/sbin/nc /usr/bin/openssl /usr/bin/tftp /usr/bin/ftpget /usr/bin/ncat 2>/dev/null",
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
		kb := 0
		fmt.Sscanf(m[1], "%d", &kb)
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
	// Typical columns: Filesystem 1K-blocks Used Available Use% Mounted
	used := data[2]
	avail := data[3]
	pct := ""
	if len(data) >= 5 {
		pct = data[4]
	}
	if pct != "" {
		return fmt.Sprintf("%s / %s (%s)", used, avail, pct)
	}
	return fmt.Sprintf("%s / %s", used, avail)
}

// parseMountsFiltered filters mount output to interesting mount points.
// Keeps: /, /tmp, /var, /mnt, /config, /data, /home, /system.
// Format: "/tmp : tmpfs,rw"
func parseMountsFiltered(output string) []string {
	interesting := map[string]bool{
		"/": true, "/tmp": true, "/var": true, "/mnt": true,
		"/config": true, "/data": true, "/home": true, "/system": true,
	}

	var result []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		mountPoint := fields[1] // /proc/mounts: device mountpoint fstype options
		if _, ok := interesting[mountPoint]; ok {
			fstype := fields[2]
			flags := ""
			if len(fields) >= 4 {
				flags = strings.TrimPrefix(fields[3], "(")
				flags = strings.TrimSuffix(flags, ")")
			}
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
