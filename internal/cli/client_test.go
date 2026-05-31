package cli

import (
	"os"
	"testing"
)

func TestColorAllowedByEnv(t *testing.T) {
	s := func(v string) *string { return &v }
	cases := []struct {
		name      string
		colorterm string
		noColor   *string // nil = NO_COLOR unset
		wt        *string // nil = WT_SESSION unset
		want      bool
	}{
		{"truecolor", "truecolor", nil, nil, true},
		{"24bit", "24bit", nil, nil, true},
		{"256color is not enough", "256color", nil, nil, false},
		{"nothing set", "", nil, nil, false},
		{"no_color overrides truecolor", "truecolor", s("1"), nil, false},
		{"empty no_color is ignored", "truecolor", s(""), nil, true},
		{"windows terminal without colorterm", "", nil, s("xyz"), true},
		{"no_color overrides windows terminal", "", s("1"), s("xyz"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setOrUnset := func(key string, v *string) {
				t.Setenv(key, "") // register restore, then set/unset for this case
				if v == nil {
					_ = os.Unsetenv(key)
				} else {
					_ = os.Setenv(key, *v)
				}
			}
			t.Setenv("COLORTERM", c.colorterm)
			setOrUnset("NO_COLOR", c.noColor)
			setOrUnset("WT_SESSION", c.wt)
			if got := colorAllowedByEnv(); got != c.want {
				t.Errorf("colorAllowedByEnv() = %v, want %v", got, c.want)
			}
		})
	}
}
