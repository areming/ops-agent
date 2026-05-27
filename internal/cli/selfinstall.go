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

// IsInstalledLocation reports whether the running binary is already at the
// Windows install path. Always returns true on non-Windows so callers can
// use it as a guard without a runtime.GOOS check.
func IsInstalledLocation() bool {
	if runtime.GOOS != "windows" {
		return true
	}
	exe, err := os.Executable()
	if err != nil {
		return true // can't tell; don't interfere
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	installed := filepath.Join(os.Getenv("LOCALAPPDATA"), "ops", "ops.exe")
	return strings.EqualFold(filepath.Clean(exe), filepath.Clean(installed))
}

// SelfInstall copies the running binary to the Windows install location,
// adds it to the user PATH, and starts ssh-agent (best-effort). Intended
// for the double-click-to-install UX: download ops.exe, run it once from
// anywhere, and it installs itself.
func SelfInstall() error {
	installDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "ops")
	dst := filepath.Join(installDir, "ops.exe")

	src, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self: %w", err)
	}

	fmt.Println("installing ops...")

	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return fmt.Errorf("create install dir: %w", err)
	}
	if err := copyReplace(src, dst); err != nil {
		return fmt.Errorf("copy binary: %w", err)
	}
	fmt.Printf("installed  %s\n", dst)

	if err := windowsAddToPath(installDir); err != nil {
		fmt.Fprintf(os.Stderr, "warning: PATH update failed: %v\n", err)
		fmt.Printf("add %s to your PATH manually\n", installDir)
	} else {
		fmt.Printf("PATH       %s\n", installDir)
	}

	tryStartSSHAgent()

	fmt.Println()
	fmt.Println("done. open a new terminal and run:")
	fmt.Println("  ops setup")
	fmt.Println()
	fmt.Fprint(os.Stderr, "press Enter to close...")
	bufio.NewReader(os.Stdin).ReadString('\n')
	return nil
}

// windowsAddToPath appends dir to the current user's PATH in the Windows
// registry if it is not already present.
func windowsAddToPath(dir string) error {
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		`[Environment]::GetEnvironmentVariable('Path','User')`).Output()
	if err != nil {
		return fmt.Errorf("read PATH: %w", err)
	}
	current := strings.TrimSpace(string(out))
	for p := range strings.SplitSeq(current, ";") {
		if strings.EqualFold(strings.TrimSpace(p), strings.TrimSpace(dir)) {
			return nil // already present
		}
	}
	newPath := dir
	if current != "" {
		newPath = current + ";" + dir
	}
	setCmd := exec.Command("powershell", "-NoProfile", "-Command",
		`[Environment]::SetEnvironmentVariable('Path',$env:NEW_PATH,'User')`)
	setCmd.Env = append(os.Environ(), "NEW_PATH="+newPath)
	return setCmd.Run()
}

// tryStartSSHAgent enables and starts the Windows ssh-agent service. Requires
// admin to change a Disabled service; failures are printed as warnings only.
func tryStartSSHAgent() {
	out, _ := exec.Command("powershell", "-NoProfile", "-Command",
		`(Get-Service ssh-agent -ErrorAction SilentlyContinue).Status`).Output()
	if strings.TrimSpace(string(out)) == "Running" {
		fmt.Println("ssh-agent  already running")
		return
	}
	err := exec.Command("powershell", "-NoProfile", "-Command",
		`try {`+
			` if ((Get-Service ssh-agent).StartType -eq 'Disabled') { Set-Service ssh-agent -StartupType Automatic };`+
			` Start-Service ssh-agent`+
			`} catch { exit 1 }`).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not start ssh-agent (may need admin). run in an admin terminal:")
		fmt.Fprintln(os.Stderr, "  Set-Service ssh-agent -StartupType Automatic; Start-Service ssh-agent")
		return
	}
	fmt.Println("ssh-agent  started")
}
