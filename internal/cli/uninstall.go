package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/areming/ops-agent/internal/config"
)

// Uninstall removes ops from the local machine.
//
// On Windows it removes the binary directory and PATH entry installed by
// install.ps1. On Linux it detects whether this is an enrolled (systemd)
// install and, if so, stops the service and removes all system artifacts;
// otherwise it removes just the current binary.
//
// State and config are kept by default. Pass purge=true for a clean, full
// uninstall that also deletes all data (keystore, audit/session DB, knowledge)
// and, on an enrolled Linux host, the service user — leaving nothing behind.
func Uninstall(purge bool) error {
	switch runtime.GOOS {
	case "windows":
		return uninstallWindows(purge)
	case "linux":
		return uninstallLinux(purge)
	default:
		return uninstallBinaryOnly(purge)
	}
}

func uninstallWindows(purge bool) error {
	installDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "ops")
	statePath := localStateDir()

	fmt.Println("will remove:")
	fmt.Printf("  binary directory  %s\n", installDir)
	fmt.Printf("  user PATH entry   %s\n", installDir)
	if purge && statePath != "" {
		fmt.Printf("  state/config      %s  (--purge)\n", statePath)
	} else if statePath != "" {
		fmt.Printf("  state/config      %s  (kept — pass --purge to delete)\n", statePath)
	}
	fmt.Println()
	printPurgeWarning(purge)

	if !confirmUninstall() {
		return fmt.Errorf("aborted")
	}

	if err := windowsRemoveFromPath(installDir); err != nil {
		fmt.Fprintf(os.Stderr, "warning: PATH update failed: %v\n", err)
	} else {
		fmt.Println("removed PATH entry")
	}

	// Windows cannot delete a running exe but allows renaming it.
	binPath := filepath.Join(installDir, "ops.exe")
	_ = os.Remove(binPath + ".old")
	_ = os.Rename(binPath, binPath+".old")

	if err := os.RemoveAll(installDir); err != nil {
		fmt.Printf("note: %s still contains the running binary — delete it manually after closing this terminal.\n", installDir)
	} else {
		fmt.Printf("removed %s\n", installDir)
	}

	if purge && statePath != "" {
		if err := os.RemoveAll(statePath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not remove state dir: %v\n", err)
		} else {
			fmt.Printf("removed state dir %s\n", statePath)
		}
	}

	fmt.Println("\nuninstalled. open a new terminal — ops is no longer on PATH.")
	return nil
}

func uninstallLinux(purge bool) error {
	enrolled := pathExists(unitPath) && pathExists(installBinPath)
	if enrolled {
		return uninstallLinuxEnrolled(purge)
	}
	return uninstallBinaryOnly(purge)
}

func uninstallLinuxEnrolled(purge bool) error {
	fmt.Println("will remove (enrolled install):")
	fmt.Printf("  service  opsagent.service (stop + disable)\n")
	fmt.Printf("  binary   %s\n", installBinPath)
	if pathExists(legacyBinPath) {
		fmt.Printf("  symlink  %s\n", legacyBinPath)
	}
	fmt.Printf("  unit     %s\n", unitPath)
	fmt.Printf("  sudoers  %s\n", sudoersPath)
	if purge {
		fmt.Printf("  state    %s  (--purge)\n", stateDir)
		fmt.Printf("  user     opsagent  (--purge)\n")
	} else {
		fmt.Printf("  state    %s  (kept — pass --purge to delete)\n", stateDir)
	}
	fmt.Println()
	printPurgeWarning(purge)

	if !confirmUninstall() {
		return fmt.Errorf("aborted")
	}

	sudoExec("systemctl", "stop", "opsagent")
	sudoExec("systemctl", "disable", "opsagent")
	sudoRemove(unitPath)
	sudoExec("systemctl", "daemon-reload")
	sudoRemove(sudoersPath)
	sudoRemove(installBinPath)
	if pathExists(legacyBinPath) {
		sudoRemove(legacyBinPath)
	}

	if purge {
		sudoExec("rm", "-rf", stateDir)
		sudoExec("userdel", "-r", "opsagent")
	}

	fmt.Println("uninstalled.")
	return nil
}

// uninstallBinaryOnly handles a non-service install — a local/dev binary on
// Linux, or ops on any other OS. It removes the binary, and with purge also
// deletes this user's state directory so a full uninstall leaves no data
// behind. (The enrolled service has its own path that removes /var/lib/opsagent
// and the service user.)
func uninstallBinaryOnly(purge bool) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate binary: %w", err)
	}
	stateDir := localStateDir()

	fmt.Println("will remove:")
	for _, line := range binaryOnlyPlan(execPath, stateDir, purge) {
		fmt.Printf("  %s\n", line)
	}
	fmt.Println()
	printPurgeWarning(purge)

	if !confirmUninstall() {
		return fmt.Errorf("aborted")
	}

	if err := os.Remove(execPath); err != nil {
		return fmt.Errorf("remove binary: %w", err)
	}
	fmt.Printf("removed %s\n", execPath)

	if purge && stateDir != "" {
		if err := os.RemoveAll(stateDir); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not remove state dir: %v\n", err)
		} else {
			fmt.Printf("removed state dir %s\n", stateDir)
		}
	}

	fmt.Println("uninstalled.")
	return nil
}

// binaryOnlyPlan returns the human-readable removal-plan lines for a
// non-service install: always the binary, plus the state dir (marked deleted
// or kept) unless its path is unknown. Split out from the IO so the --purge
// behavior is unit-tested.
func binaryOnlyPlan(execPath, stateDir string, purge bool) []string {
	lines := []string{"binary  " + execPath}
	if stateDir == "" {
		return lines
	}
	if purge {
		lines = append(lines, "state   "+stateDir+"  (--purge)")
	} else {
		lines = append(lines, "state   "+stateDir+"  (kept — pass --purge to delete)")
	}
	return lines
}

func confirmUninstall() bool {
	fmt.Fprint(os.Stderr, "type 'yes' to continue: ")
	sc := bufio.NewScanner(os.Stdin)
	sc.Scan()
	return strings.TrimSpace(sc.Text()) == "yes"
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// sudoExec runs cmd with sudo, printing it first. Errors are reported as
// warnings so the uninstall continues to remove as much as possible.
func sudoExec(name string, args ...string) {
	all := append([]string{name}, args...)
	fmt.Printf("  sudo %s\n", strings.Join(all, " "))
	cmd := exec.Command("sudo", all...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: sudo %s: %v\n", strings.Join(all, " "), err)
	}
}

func sudoRemove(p string) { sudoExec("rm", "-f", p) }

// localStateDir resolves this user's state directory the same way the agent
// does (honoring OPSAGENT_STATE_DIR), so a --purge also removes a customized
// location. Used for non-service installs (Windows, local/dev Linux, other
// OSes); the enrolled service always lives at the fixed stateDir.
func localStateDir() string {
	return config.Load().StateDir
}

// purgeWarning is the loud, irreversible-data line shown before a --purge
// confirmation; it is empty when not purging. Split out so the message is
// unit-tested.
func purgeWarning(purge bool) string {
	if !purge {
		return ""
	}
	return "⚠ --purge 会永久删除全部数据：密钥库、审计/会话库、知识档案 —— 不可恢复。"
}

// printPurgeWarning shows the purge warning (if any) on stderr, just before
// the confirmation prompt.
func printPurgeWarning(purge bool) {
	if w := purgeWarning(purge); w != "" {
		fmt.Fprintln(os.Stderr, w)
	}
}

// windowsRemoveFromPath removes dir from the current user's PATH stored in
// the Windows registry. Uses PowerShell so no registry package is needed.
func windowsRemoveFromPath(dir string) error {
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		`[Environment]::GetEnvironmentVariable('Path','User')`).Output()
	if err != nil {
		return fmt.Errorf("read PATH: %w", err)
	}
	current := strings.TrimSpace(string(out))

	var kept []string
	for p := range strings.SplitSeq(current, ";") {
		if !strings.EqualFold(strings.TrimSpace(p), strings.TrimSpace(dir)) {
			kept = append(kept, p)
		}
	}
	newPath := strings.Join(kept, ";")
	if newPath == current {
		return nil // dir was not present
	}

	setCmd := exec.Command("powershell", "-NoProfile", "-Command",
		`[Environment]::SetEnvironmentVariable('Path',$env:NEW_PATH,'User')`)
	setCmd.Env = append(os.Environ(), "NEW_PATH="+newPath)
	return setCmd.Run()
}
