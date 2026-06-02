package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestSanitizeLine(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "hello", "hello"},
		{"strip CSI color", "\x1b[31mred\x1b[0m", "red"},
		{"collapse CR progress", "Step 1\rStep 2\rStep 3", "Step 3"},
		{"tab to space", "a\tb", "a b"},
		{"drop other control", "a\x07b", "ab"},
		{"keep CJK", "构建镜像", "构建镜像"},
		{"strip OSC title", "\x1b]0;window-title\x07kept", "kept"},
		{"bare ESC", "x\x1by", "xy"},
	}
	for _, c := range cases {
		if got := sanitizeLine(c.in); got != c.want {
			t.Errorf("%s: sanitizeLine(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestLiveOutputPlainStreamsCompleteLines(t *testing.T) {
	var buf bytes.Buffer
	l := newLiveOutput(&buf, false, 0, 0)

	// A partial line is held until its newline arrives.
	l.feed([]byte("first\nsec"))
	if strings.Contains(buf.String(), "sec") {
		t.Errorf("partial line printed before newline: %q", buf.String())
	}
	l.feed([]byte("ond\n"))
	l.feed([]byte("trailing-no-newline"))
	l.finish() // flushes the trailing partial

	out := buf.String()
	for _, want := range []string{"first", "second", "trailing-no-newline"} {
		if !strings.Contains(out, want) {
			t.Errorf("plain output missing %q; got %q", want, out)
		}
	}
}

func TestLiveOutputWindowCapClamp(t *testing.T) {
	var buf bytes.Buffer
	if c := newLiveOutput(&buf, true, 40, 4).cap; c != liveMinLines {
		t.Errorf("short terminal cap = %d, want %d", c, liveMinLines)
	}
	if c := newLiveOutput(&buf, true, 40, 200).cap; c != liveMaxLines {
		t.Errorf("tall terminal cap = %d, want %d", c, liveMaxLines)
	}
	if newLiveOutput(&buf, false, 0, 0).window {
		t.Error("window enabled without a terminal size")
	}
}

func TestLiveOutputWindowAdaptiveHeight(t *testing.T) {
	var buf bytes.Buffer
	l := newLiveOutput(&buf, true, 40, 16) // cap = min(max(8,3),10) = 8
	if l.cap != 8 {
		t.Fatalf("cap = %d, want 8", l.cap)
	}
	// Few lines: the window stays small (grows with content).
	for i := range 3 {
		l.feed(fmt.Appendf(nil, "l%d\n", i))
	}
	if l.rendered != 3 {
		t.Errorf("rendered = %d, want 3 (window grows with content)", l.rendered)
	}
	// Many lines: the window is capped and scrolls.
	for i := 3; i < 20; i++ {
		l.feed(fmt.Appendf(nil, "l%d\n", i))
	}
	if l.rendered != 8 {
		t.Errorf("rendered = %d, want 8 (capped at adaptive height)", l.rendered)
	}
	if l.total != 20 {
		t.Errorf("total = %d, want 20", l.total)
	}
}

func TestLiveOutputWindowCollapsesToSummary(t *testing.T) {
	var buf bytes.Buffer
	l := newLiveOutput(&buf, true, 40, 16)
	for i := range 3 {
		l.feed(fmt.Appendf(nil, "line%d\n", i))
	}
	l.feed([]byte("tail-without-newline")) // counted in the summary
	l.finish()

	if !strings.Contains(buf.String(), "⎿ 输出 4 行") {
		t.Errorf("missing collapse summary; got %q", buf.String())
	}
}

func TestLiveOutputTruncatesToWidth(t *testing.T) {
	l := newLiveOutput(&bytes.Buffer{}, true, 14, 16) // avail = 14 - 4 = 10 cells

	got := l.truncate("abcdefghijklmnopqrstuvwxyz")
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncate did not append ellipsis: %q", got)
	}
	width := 0
	for _, r := range got {
		width += runeWidth(r)
	}
	if width > 10 {
		t.Errorf("truncated width = %d, want <= 10 (%q)", width, got)
	}
	if got := l.truncate("short"); got != "short" {
		t.Errorf("a fitting line was altered: %q", got)
	}
}
