package safety

import (
	"regexp"
	"runtime"
	"slices"
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

// IsPatrolAutoRemedy reports whether patrol may run cmd unattended as a
// self-heal action. It is intentionally narrow: only `systemctl start` or
// `systemctl restart` (optionally via `sudo`, since the agent runs as a
// non-root service user) of a unit the operator explicitly listed for
// patrol, and never anything matching a danger rule. This is the single
// point that lets patrol perform a write without a human; chat-path writes
// still go through Classify and require confirmation.
func IsPatrolAutoRemedy(cmd string, allowedUnits []string) bool {
	cmd = strings.TrimSpace(cmd)
	if matchDanger(cmd) != "" {
		return false
	}
	// No shell metacharacters: a remediation command is a single plain
	// invocation, never a pipeline, redirect, or sequence.
	if strings.ContainsAny(cmd, ">|`&;") || strings.Contains(cmd, "$(") {
		return false
	}
	fields := strings.Fields(cmd)

	// Allow an optional leading `sudo` with flags (e.g. `sudo -n`).
	if len(fields) > 0 && baseName(fields[0]) == "sudo" {
		fields = fields[1:]
		for len(fields) > 0 && strings.HasPrefix(fields[0], "-") {
			fields = fields[1:]
		}
	}

	if len(fields) != 3 {
		return false
	}
	if baseName(fields[0]) != "systemctl" {
		return false
	}
	if fields[1] != "start" && fields[1] != "restart" {
		return false
	}
	return slices.Contains(allowedUnits, fields[2])
}

// baseName strips a leading path from a binary token (/usr/bin/ps -> ps).
func baseName(bin string) string {
	if idx := strings.LastIndexByte(bin, '/'); idx >= 0 {
		return bin[idx+1:]
	}
	return bin
}

// readOnlyBins are binaries that cannot change system state regardless of
// their arguments, on any platform. Names that only make sense on (or are
// only safe on) Windows live in windowsReadOnlyBins. Commands with mixed
// read/write modes (systemctl, git, docker, wmic, …) are handled by dedicated
// checks below.
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

// windowsReadOnlyBins are Windows query commands that cannot change system
// state. They are consulted only when the target is Windows, so names that are
// dangerous elsewhere (e.g. `find`, which deletes on Unix but only searches
// text on Windows) are never auto-allowed on the wrong platform.
var windowsReadOnlyBins = map[string]bool{
	"where": true, "systeminfo": true, "tasklist": true, "ver": true,
	"vol": true, "getmac": true, "driverquery": true, "tree": true,
	"type": true, "find": true, "findstr": true, "dir": true,
	"set": true, "path": true,
}

// probeFlags are version/help queries. A command whose only argument is one of
// these never changes state, whatever the binary — so `node --version`,
// `git --version`, `go version` and friends auto-allow without per-tool rules.
var probeFlags = map[string]bool{
	"--version": true, "-version": true, "-v": true, "version": true,
	"--help": true, "-h": true, "help": true, "-?": true, "/?": true,
}

// gitReadOnlyVerbs are git subcommands that never modify the repo or remote.
// Mixed-mode verbs (config, remote, branch, tag, stash) are deliberately
// excluded — with arguments they write.
var gitReadOnlyVerbs = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true, "describe": true,
	"rev-parse": true, "ls-files": true, "ls-remote": true, "blame": true,
	"shortlog": true, "reflog": true, "cat-file": true, "whatchanged": true,
	"grep": true, "name-rev": true, "merge-base": true, "var": true,
}

// dockerReadOnlyVerbs are docker subcommands that only inspect state.
var dockerReadOnlyVerbs = map[string]bool{
	"ps": true, "info": true, "images": true, "version": true, "inspect": true,
	"logs": true, "stats": true, "top": true, "port": true, "history": true,
	"events": true, "df": true, "search": true,
}

// wmicWriteVerbs mark a wmic invocation as state-changing, e.g.
// `wmic product call uninstall` or `wmic process where ... delete`.
var wmicWriteVerbs = map[string]bool{
	"call": true, "create": true, "delete": true, "set": true, "add": true,
}

// ipconfigWriteVerbs mark an ipconfig invocation as state-changing.
var ipconfigWriteVerbs = map[string]bool{
	"/release": true, "/release6": true, "/renew": true, "/renew6": true,
	"/flushdns": true, "/registerdns": true, "/setclassid": true,
	"/setclassid6": true,
}

// benignRedirect matches redirections that only discard or merge output and so
// cannot write meaningful data: `2>nul`, `>nul`, `2>/dev/null`, `2>&1`, `>&2`.
var benignRedirect = regexp.MustCompile(`(?i)\s*\d*>>?\s*(?:nul|/dev/null)\b|\s*\d*>&\s*\d+`)

// cmdSeparator splits a command line into independently-evaluated segments.
// Whatever the operator (sequence, and, or, pipe, background), the whole line
// is read-only iff every segment is — so we validate them in isolation.
var cmdSeparator = regexp.MustCompile(`&&|\|\||[;&|]`)

// isReadOnlyCommand reports whether a command can be auto-allowed on the
// machine that will run it (the agent runs co-located with its shell).
func isReadOnlyCommand(cmd string) bool {
	return isReadOnlyCommandFor(cmd, runtime.GOOS)
}

// isReadOnlyCommandFor is isReadOnlyCommand with the target platform made
// explicit, so both platforms' behaviour is testable from anywhere. A command
// is read-only when it has no command substitution, every redirection only
// discards or merges output, and every segment is a read-only invocation.
func isReadOnlyCommandFor(cmd, goos string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	if strings.Contains(cmd, "$(") || strings.Contains(cmd, "`") {
		return false
	}
	cmd = benignRedirect.ReplaceAllString(cmd, "")
	if strings.ContainsAny(cmd, "><") {
		return false // a redirection that writes a real file or reads input
	}

	sawSegment := false
	for _, seg := range cmdSeparator.Split(cmd, -1) {
		if strings.TrimSpace(seg) == "" {
			continue
		}
		sawSegment = true
		if !readOnlySegment(seg, goos) {
			return false
		}
	}
	return sawSegment
}

func readOnlySegment(seg, goos string) bool {
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

	// A lone version/help query never changes state, whatever the binary.
	if isVersionProbe(rest) {
		return true
	}

	switch bin {
	case "systemctl":
		return systemctlReadOnly(rest)
	case "service":
		return len(rest) > 0 && rest[len(rest)-1] == "status"
	case "journalctl":
		return journalctlReadOnly(rest)
	case "git":
		return len(rest) > 0 && gitReadOnlyVerbs[rest[0]]
	case "docker":
		return len(rest) > 0 && dockerReadOnlyVerbs[rest[0]]
	}

	if goos == "windows" {
		wb := strings.ToLower(bin)
		// `echo.` / `echo:` is the cmd idiom for printing a blank line.
		if wb == "echo" || strings.HasPrefix(wb, "echo.") || strings.HasPrefix(wb, "echo:") {
			return true
		}
		switch wb {
		case "wmic":
			return wmicReadOnly(rest)
		case "ipconfig":
			return ipconfigReadOnly(rest)
		}
		if windowsReadOnlyBins[wb] {
			return true
		}
	}
	return readOnlyBins[bin]
}

// isVersionProbe reports whether args is a single version/help flag.
func isVersionProbe(args []string) bool {
	return len(args) == 1 && probeFlags[strings.ToLower(args[0])]
}

func wmicReadOnly(args []string) bool {
	for _, a := range args {
		if wmicWriteVerbs[strings.ToLower(a)] {
			return false
		}
	}
	return true
}

func ipconfigReadOnly(args []string) bool {
	for _, a := range args {
		if ipconfigWriteVerbs[strings.ToLower(a)] {
			return false
		}
	}
	return true
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
