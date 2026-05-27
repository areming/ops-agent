package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Uninstall removes ops from the local machine.
//
// On Windows it removes the binary directory and PATH entry installed by
// install.ps1. On Linux it detects whether this is an enrolled (systemd)
// install and, if so, stops the service and removes all system artifacts;
// otherwise it removes just the current binary.
//
// State and config are kept by default. Pass purge=true to delete them too.
func Uninstall(purge bool) error {
	switch runtime.GOOS {
	case "windows":
		return uninstallWindows(purge)
	case "linux":
		return uninstallLinux(purge)
	default:
		return uninstallGeneric()
	}
}

func uninstallWindows(purge bool) error {
	installDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "ops")
	statePath := windowsStateDir()

	fmt.Println("will remove:")
	fmt.Printf("  binary directory  %s\n", installDir)
	fmt.Printf("  user PATH entry   %s\n", installDir)
	if purge && statePath != "" {
		fmt.Printf("  state/config      %s  (--purge)\n", statePath)
	} else if statePath != "" {
		fmt.Printf("  state/config      %s  (kept — pass --purge to delete)\n", statePath)
	}
	fmt.Println()

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
	return uninstallLinuxLocal()
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

func uninstallLinuxLocal() error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate binary: %w", err)
	}

	fmt.Println("will remove:")
	fmt.Printf("  binary  %s\n", execPath)
	fmt.Println()

	if !confirmUninstall() {
		return fmt.Errorf("aborted")
	}

	if err := os.Remove(execPath); err != nil {
		return fmt.Errorf("remove binary: %w", err)
	}
	fmt.Printf("removed %s\n", execPath)
	fmt.Println("uninstalled.")
	return nil
}

func uninstallGeneric() error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate binary: %w", err)
	}

	fmt.Printf("will remove: %s\n\n", execPath)
	if !confirmUninstall() {
		return fmt.Errorf("aborted")
	}

	if err := os.Remove(execPath); err != nil {
		return fmt.Errorf("remove binary: %w", err)
	}
	fmt.Println("uninstalled.")
	return nil
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

// windowsStateDir returns the opsagent state directory on Windows
// (mirrors the path computed by config.defaultStateDir).
func windowsStateDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "opsagent")
	}
	return ""
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
