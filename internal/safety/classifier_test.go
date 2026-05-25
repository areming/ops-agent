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
