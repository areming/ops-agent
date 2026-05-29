package safety

import "testing"

func boolp(b bool) *bool { return &b }

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		act  Action
		want Decision
	}{
		// Dangerous patterns always confirm, even if the model claims safe.
		{"rm -rf", Action{Display: "rm -rf /var/data"}, Confirm},
		{"rm -rf model-says-reversible", Action{Display: "rm -rf /tmp/x", Eval: SelfEval{Reversible: boolp(true), Risk: "low"}}, Confirm},
		{"dd to device", Action{Display: "dd if=/dev/zero of=/dev/sda"}, Confirm},
		{"mkfs", Action{Display: "mkfs.ext4 /dev/sdb1"}, Confirm},
		{"reboot", Action{Display: "reboot"}, Confirm},
		{"drop database", Action{Display: `mysql -e "DROP DATABASE prod"`}, Confirm},
		{"redirect to device", Action{Display: "cat x > /dev/sda"}, Confirm},

		// Read-only commands auto-allow.
		{"ps", Action{Display: "ps aux"}, Allow},
		{"piped readonly", Action{Display: "ps aux | grep nginx"}, Allow},
		{"systemctl status", Action{Display: "systemctl status nginx"}, Allow},
		{"journalctl read", Action{Display: "journalctl -u nginx --no-pager"}, Allow},
		{"read-only tool", Action{ReadOnly: true, Display: "read /etc/hosts"}, Allow},

		// Writes / unknown commands confirm.
		{"systemctl restart", Action{Display: "systemctl restart nginx"}, Confirm},
		{"redirect write", Action{Display: "echo hi > /tmp/f"}, Confirm},
		{"journalctl vacuum", Action{Display: "journalctl --vacuum-time=2d"}, Confirm},
		{"unknown binary", Action{Display: "foobar --do-stuff"}, Confirm},
		{"write tool", Action{ReadOnly: false, Display: "write /etc/nginx/nginx.conf"}, Confirm},

		// Model self-assessment escalates an otherwise-readonly action.
		{"readonly but model high risk", Action{Display: "ps aux", Eval: SelfEval{Risk: "high"}}, Confirm},
		{"model says irreversible", Action{Display: "ls /data", Eval: SelfEval{Reversible: boolp(false)}}, Confirm},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Classify(c.act).Decision
			if got != c.want {
				t.Errorf("Classify(%q).Decision = %v, want %v", c.act.Display, got, c.want)
			}
		})
	}
}

func TestClassifyDangerFlag(t *testing.T) {
	cases := []struct {
		name string
		act  Action
		want bool
	}{
		{"hard danger rule sets flag", Action{Display: "rm -rf /var/data"}, true},
		{"danger even if model says safe", Action{Display: "mkfs.ext4 /dev/sdb1", Eval: SelfEval{Reversible: boolp(true), Risk: "low"}}, true},
		{"plain write is not danger", Action{Display: "systemctl restart nginx"}, false},
		{"model-escalated risk is not danger", Action{Display: "ps aux", Eval: SelfEval{Risk: "high"}}, false},
		{"read-only is not danger", Action{Display: "ps aux"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.act).Danger; got != c.want {
				t.Errorf("Classify(%q).Danger = %v, want %v", c.act.Display, got, c.want)
			}
		})
	}
}

func TestIsReadOnlyCommandFor(t *testing.T) {
	// The three real environment-probe commands the user ran on Windows.
	winEnv := `echo === DISK === & wmic logicaldisk get size,freespace,caption,volumename 2>nul & echo. & echo === CPU === & wmic cpu get name,numberofcores,numberoflogicalprocessors /value 2>nul & echo. & echo === GPU === & wmic path win32_videocontroller get name 2>nul & echo. & echo === ENV === & set & echo. & echo === PATH === & echo %PATH%`
	winTools := `echo === DEV TOOLS === & echo Node: & node --version 2>nul & echo Go: & go version 2>nul & echo Git: & git --version 2>nul & python --version 2>nul & echo. & echo === DOCKER === & docker info 2>nul || echo docker not running/installed`
	winPorts := `echo === LISTENING PORTS === & netstat -an | findstr LISTENING 2>nul & echo. & dir "E:\Program Files\Microsoft VS Code\bin\code*.cmd" 2>nul & where code 2>nul`

	cases := []struct {
		name string
		cmd  string
		goos string
		want bool
	}{
		// The user's real Windows diagnostics now auto-allow.
		{"win env probe", winEnv, "windows", true},
		{"win dev tools probe", winTools, "windows", true},
		{"win listening ports probe", winPorts, "windows", true},

		// Windows query binaries.
		{"wmic get", "wmic logicaldisk get size,freespace", "windows", true},
		{"wmic call uninstall", "wmic product call uninstall", "windows", false},
		{"wmic delete", "wmic process where name='x' delete", "windows", false},
		{"ipconfig all", "ipconfig /all", "windows", true},
		{"ipconfig flushdns", "ipconfig /flushdns", "windows", false},
		{"ipconfig renew", "ipconfig /renew", "windows", false},
		{"where", "where code", "windows", true},
		{"dir with quoted path", `dir "C:\Program Files"`, "windows", true},
		{"findstr pipe", "netstat -an | findstr LISTENING", "windows", true},
		{"echo dot blank line", "echo.", "windows", true},
		{"del is not read-only", "del file.txt", "windows", false},
		{"rmdir is not read-only", "rmdir /s /q C:\\tmp", "windows", false},

		// `find` deletes on Unix but only searches text on Windows — the platform
		// gate keeps it out of the read-only set on the wrong OS.
		{"unix find delete", "find . -name x -delete", "linux", false},
		{"windows find search", `find "text" file.txt`, "windows", true},

		// Version/help probes auto-allow for any binary.
		{"node version", "node --version", "windows", true},
		{"go version subcommand", "go version", "linux", true},
		{"dotnet version", "dotnet --version", "windows", true},
		{"git version not git verb", "git --version", "linux", true},
		{"tool help", "kubectl --help", "linux", true},

		// git/docker read-only subcommands (cross-platform).
		{"git status", "git status", "linux", true},
		{"git log", "git log --oneline", "linux", true},
		{"git push writes", "git push origin main", "linux", false},
		{"git config writes", "git config user.name x", "linux", false},
		{"docker info", "docker info", "linux", true},
		{"docker ps", "docker ps -a", "linux", true},
		{"docker rm writes", "docker rm container", "linux", false},

		// Benign redirects are stripped; real writes/inputs are not.
		{"stderr to null", "ps aux 2>/dev/null", "linux", true},
		{"stderr merge", "ps aux 2>&1", "linux", true},
		{"redirect to file", "echo hi > /tmp/f", "linux", false},
		{"append to file", "echo hi >> /tmp/f", "linux", false},
		{"input redirect", "sort < secrets.txt", "linux", false},

		// Sequencing/and/or of read-only segments is read-only.
		{"semicolon chain", "ps; ls; df", "linux", true},
		{"and chain", "ps && ls", "linux", true},
		{"pipe chain", "ps aux | grep nginx", "linux", true},
		{"chain with one writer", "ps; rm file", "linux", false},

		// Command substitution is opaque — never auto-allow.
		{"dollar subst", "echo $(rm -rf /)", "linux", false},
		{"backtick subst", "echo `whoami`", "linux", false},

		// Empty.
		{"empty", "", "linux", false},
		{"only separators", " & & ", "windows", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isReadOnlyCommandFor(c.cmd, c.goos); got != c.want {
				t.Errorf("isReadOnlyCommandFor(%q, %q) = %v, want %v", c.cmd, c.goos, got, c.want)
			}
		})
	}
}

func TestIsPatrolAutoRemedy(t *testing.T) {
	units := []string{"nginx", "postgresql.service"}
	cases := []struct {
		name string
		cmd  string
		want bool
	}{
		{"restart watched unit", "systemctl restart nginx", true},
		{"start watched unit", "systemctl start nginx", true},
		{"restart watched unit with suffix", "systemctl restart postgresql.service", true},
		{"absolute systemctl path", "/usr/bin/systemctl restart nginx", true},
		{"sudo restart", "sudo systemctl restart nginx", true},
		{"sudo -n restart", "sudo -n systemctl restart nginx", true},

		{"unit not in list", "systemctl restart sshd", false},
		{"sudo unit not in list", "sudo systemctl restart sshd", false},
		{"stop is not a remedy verb", "systemctl stop nginx", false},
		{"disable is not a remedy verb", "systemctl disable nginx", false},
		{"non-systemctl binary", "service nginx restart", false},
		{"extra arguments", "systemctl restart nginx --now", false},
		{"missing unit", "systemctl restart", false},
		{"chained command sneaks danger", "systemctl restart nginx; rm -rf /", false},
		{"pipe", "systemctl restart nginx | tee log", false},
		{"command substitution", "systemctl restart $(echo nginx)", false},
		{"empty", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsPatrolAutoRemedy(c.cmd, units); got != c.want {
				t.Errorf("IsPatrolAutoRemedy(%q) = %v, want %v", c.cmd, got, c.want)
			}
		})
	}
}
