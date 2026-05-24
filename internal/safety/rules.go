package safety

import (
	"regexp"
	"strings"
)

// dangerRule pairs a pattern with a human-readable label for the
// confirmation reason.
type dangerRule struct {
	re    *regexp.Regexp
	label string
}

// dangerRules flag irreversible or high-impact commands. A match always
// requires confirmation and cannot be downgraded by the model.
var dangerRules = []dangerRule{
	{regexp.MustCompile(`(?i)\brm\b[^|]*\s-[a-z]*[rf]`), "rm with -r/-f (recursive or forced delete)"},
	{regexp.MustCompile(`(?i)\bmkfs\b`), "mkfs (format filesystem)"},
	{regexp.MustCompile(`(?i)\bdd\b[^|]*\bof=`), "dd writing to a device/file"},
	{regexp.MustCompile(`(?i)>\s*/dev/[sv]d`), "redirect onto a block device"},
	{regexp.MustCompile(`(?i)\b(shutdown|reboot|halt|poweroff)\b`), "power state change"},
	{regexp.MustCompile(`(?i)\b(drop|truncate)\s+(table|database)\b`), "drop/truncate database object"},
	{regexp.MustCompile(`(?i)\b(fdisk|parted|wipefs|mkswap)\b`), "disk partitioning"},
	{regexp.MustCompile(`(?i)\b(userdel|groupdel)\b`), "delete user/group"},
	{regexp.MustCompile(`:\s*\(\)\s*\{`), "fork bomb"},
}

func matchDanger(cmd string) string {
	for _, r := range dangerRules {
		if r.re.MatchString(cmd) {
			return r.label
		}
	}
	return ""
}

// readOnlyBins are binaries that cannot change system state regardless of
// their arguments. Commands with mixed read/write modes (systemctl,
// journalctl, service) are handled by dedicated checks below.
var readOnlyBins = map[string]bool{
	"ps": true, "top": true, "htop": true, "free": true, "df": true, "du": true,
	"ls": true, "cat": true, "head": true, "tail": true, "grep": true,
	"egrep": true, "fgrep": true, "wc": true, "uptime": true, "who": true,
	"w": true, "id": true, "whoami": true, "uname": true, "hostname": true,
	"pwd": true, "echo": true, "env": true, "printenv": true, "stat": true,
	"file": true, "lsblk": true, "lscpu": true, "lsmem": true, "vmstat": true,
	"iostat": true, "mpstat": true, "lsof": true, "netstat": true, "ss": true,
	"last": true,
}

// isReadOnlyCommand reports whether a command can be auto-allowed: it must
// contain no redirection, command substitution, or sequencing, and every
// pipe segment must be a read-only invocation.
func isReadOnlyCommand(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	if strings.ContainsAny(cmd, ">`&;") || strings.Contains(cmd, "$(") {
		return false
	}
	for seg := range strings.SplitSeq(cmd, "|") {
		if !readOnlySegment(seg) {
			return false
		}
	}
	return true
}

func readOnlySegment(seg string) bool {
	fields := strings.Fields(seg)
	// Skip leading VAR=value environment assignments.
	i := 0
	for i < len(fields) && strings.Contains(fields[i], "=") && !strings.HasPrefix(fields[i], "-") {
		i++
	}
	if i >= len(fields) {
		return false
	}
	bin := fields[i]
	if idx := strings.LastIndexByte(bin, '/'); idx >= 0 {
		bin = bin[idx+1:] // /usr/bin/ps -> ps
	}
	rest := fields[i+1:]

	switch bin {
	case "systemctl":
		return systemctlReadOnly(rest)
	case "service":
		return len(rest) > 0 && rest[len(rest)-1] == "status"
	case "journalctl":
		return journalctlReadOnly(rest)
	}
	return readOnlyBins[bin]
}

var systemctlReadVerbs = map[string]bool{
	"status": true, "is-active": true, "is-enabled": true, "is-failed": true,
	"show": true, "cat": true, "list-units": true, "list-unit-files": true,
	"list-timers": true, "list-sockets": true, "get-default": true,
}

func systemctlReadOnly(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue // skip flags like --no-pager
		}
		return systemctlReadVerbs[a] // first verb decides
	}
	return false
}

// journalctl only reads logs unless asked to rotate/vacuum/flush.
func journalctlReadOnly(args []string) bool {
	for _, a := range args {
		switch {
		case a == "--flush", a == "--sync", a == "--rotate",
			strings.HasPrefix(a, "--vacuum"):
			return false
		}
	}
	return true
}
