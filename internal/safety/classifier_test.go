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
