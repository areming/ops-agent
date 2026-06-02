package cli

import (
	"bufio"
	"bytes"
	"net"
	"strings"
	"testing"

	"github.com/areming/ops-agent/internal/transport"
)

func TestCrlfWriter(t *testing.T) {
	cases := []struct {
		name   string
		writes []string
		want   string
	}{
		{"bare lf", []string{"a\nb"}, "a\r\nb"},
		{"already crlf", []string{"a\r\nb"}, "a\r\nb"},
		{"multiple lf", []string{"x\ny\n"}, "x\r\ny\r\n"},
		{"cr split across writes", []string{"x\r", "\n"}, "x\r\n"},
		{"no newline", []string{"plain"}, "plain"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var sink bytes.Buffer
			w := &crlfWriter{w: &sink}
			for _, s := range c.writes {
				n, err := w.Write([]byte(s))
				if err != nil {
					t.Fatalf("write: %v", err)
				}
				if n != len(s) {
					t.Errorf("Write returned %d, want %d", n, len(s))
				}
			}
			if got := sink.String(); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestRuneWidth(t *testing.T) {
	cases := []struct {
		r    rune
		want int
	}{
		{'a', 1},
		{'é', 1},
		{'\n', 0},
		{0x07, 0},    // bell (control)
		{'中', 2},     // CJK unified
		{'。', 2},     // CJK punctuation
		{'Ａ', 2},     // fullwidth A
		{'한', 2},     // Hangul syllable
		{0x1f600, 1}, // emoji: not in our wide ranges (best-effort)
		{0x20000, 2}, // CJK ext B
	}
	for _, c := range cases {
		if got := runeWidth(c.r); got != c.want {
			t.Errorf("runeWidth(%#U) = %d, want %d", c.r, got, c.want)
		}
	}
}

func TestLineEditorTypingAndBackspace(t *testing.T) {
	var echo bytes.Buffer
	ed := &lineEditor{}
	ed.reset()

	for _, r := range "hi" {
		if got := ed.feed(keyEvent{kind: keyRune, r: r}, &echo); got != editContinue {
			t.Fatalf("typing %q: got %v", r, got)
		}
	}
	if ed.line() != "hi" {
		t.Fatalf("line = %q, want %q", ed.line(), "hi")
	}
	if echo.String() != "hi" {
		t.Fatalf("echo = %q, want %q", echo.String(), "hi")
	}

	echo.Reset()
	ed.feed(keyEvent{kind: keyBackspace}, &echo)
	if ed.line() != "h" {
		t.Errorf("after backspace line = %q, want %q", ed.line(), "h")
	}
	if echo.String() != "\b \b" {
		t.Errorf("ascii backspace erase = %q, want %q", echo.String(), "\b \b")
	}
}

func TestLineEditorWideBackspace(t *testing.T) {
	var echo bytes.Buffer
	ed := &lineEditor{}
	ed.reset()
	ed.feed(keyEvent{kind: keyRune, r: '中'}, &echo)
	echo.Reset()
	ed.feed(keyEvent{kind: keyBackspace}, &echo)
	if ed.line() != "" {
		t.Errorf("line = %q, want empty", ed.line())
	}
	if echo.String() != "\b\b  \b\b" {
		t.Errorf("wide backspace erase = %q, want %q", echo.String(), "\b\b  \b\b")
	}
}

func TestLineEditorKillLine(t *testing.T) {
	var echo bytes.Buffer
	ed := &lineEditor{}
	ed.reset()
	for _, r := range "ab中" { // widths 1+1+2 = 4 cells
		ed.feed(keyEvent{kind: keyRune, r: r}, &echo)
	}
	echo.Reset()
	ed.feed(keyEvent{kind: keyCtrlU}, &echo)
	if ed.line() != "" {
		t.Errorf("after Ctrl-U line = %q, want empty", ed.line())
	}
	want := strings.Repeat("\b", 4) + strings.Repeat(" ", 4) + strings.Repeat("\b", 4)
	if echo.String() != want {
		t.Errorf("Ctrl-U erase = %q, want %q", echo.String(), want)
	}
}

func TestLineEditorEraseWord(t *testing.T) {
	var echo bytes.Buffer
	ed := &lineEditor{}
	ed.reset()
	for _, r := range "foo bar" {
		ed.feed(keyEvent{kind: keyRune, r: r}, &echo)
	}
	echo.Reset()
	ed.feed(keyEvent{kind: keyCtrlW}, &echo) // erases "bar"
	if ed.line() != "foo " {
		t.Errorf("after Ctrl-W line = %q, want %q", ed.line(), "foo ")
	}
	want := strings.Repeat("\b", 3) + strings.Repeat(" ", 3) + strings.Repeat("\b", 3)
	if echo.String() != want {
		t.Errorf("Ctrl-W erase = %q, want %q", echo.String(), want)
	}
	// A second Ctrl-W eats the trailing space and "foo".
	ed.feed(keyEvent{kind: keyCtrlW}, &echo)
	if ed.line() != "" {
		t.Errorf("after second Ctrl-W line = %q, want empty", ed.line())
	}
}

func TestLineEditorControlKeys(t *testing.T) {
	var echo bytes.Buffer
	ed := &lineEditor{}

	ed.reset()
	ed.feed(keyEvent{kind: keyRune, r: 'x'}, &echo)
	if got := ed.feed(keyEvent{kind: keyEnter}, &echo); got != editSubmit {
		t.Errorf("Enter: got %v, want editSubmit", got)
	}
	if ed.line() != "x" {
		t.Errorf("submitted line = %q, want %q", ed.line(), "x")
	}

	ed.reset()
	if got := ed.feed(keyEvent{kind: keyCtrlD}, &echo); got != editExit {
		t.Errorf("Ctrl-D on empty: got %v, want editExit", got)
	}
	ed.feed(keyEvent{kind: keyRune, r: 'a'}, &echo)
	if got := ed.feed(keyEvent{kind: keyCtrlD}, &echo); got != editContinue {
		t.Errorf("Ctrl-D with text: got %v, want editContinue", got)
	}
	if got := ed.feed(keyEvent{kind: keyCtrlC}, &echo); got != editExit {
		t.Errorf("Ctrl-C: got %v, want editExit", got)
	}
}

// decodeAll drains every key event decodable from b.
func decodeAll(t *testing.T, b []byte) []keyEvent {
	t.Helper()
	br := bufio.NewReader(bytes.NewReader(b))
	var out []keyEvent
	for {
		ev, err := decodeKey(br)
		if err != nil {
			return out
		}
		out = append(out, ev)
	}
}

func TestDecodeKeySingles(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want keyEvent
	}{
		{"ascii", []byte("a"), keyEvent{kind: keyRune, r: 'a'}},
		{"cr", []byte("\r"), keyEvent{kind: keyEnter}},
		{"lf", []byte("\n"), keyEvent{kind: keyEnter}},
		{"del", []byte{0x7f}, keyEvent{kind: keyBackspace}},
		{"bs", []byte{0x08}, keyEvent{kind: keyBackspace}},
		{"ctrl-c", []byte{0x03}, keyEvent{kind: keyCtrlC}},
		{"ctrl-d", []byte{0x04}, keyEvent{kind: keyCtrlD}},
		{"ctrl-u", []byte{0x15}, keyEvent{kind: keyCtrlU}},
		{"ctrl-w", []byte{0x17}, keyEvent{kind: keyCtrlW}},
		{"lone esc", []byte{0x1b}, keyEvent{kind: keyEsc}},
		{"up", []byte{0x1b, '[', 'A'}, keyEvent{kind: keyUp}},
		{"down", []byte{0x1b, '[', 'B'}, keyEvent{kind: keyDown}},
		{"right", []byte{0x1b, '[', 'C'}, keyEvent{kind: keyRight}},
		{"left", []byte{0x1b, '[', 'D'}, keyEvent{kind: keyLeft}},
		{"cjk rune", []byte("中"), keyEvent{kind: keyRune, r: '中'}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decodeAll(t, c.in)
			if len(got) != 1 || got[0] != c.want {
				t.Fatalf("decode(%v) = %+v, want [%+v]", c.in, got, c.want)
			}
		})
	}
}

func TestDecodeKeySequence(t *testing.T) {
	// Arrow followed by a printable rune must decode as two distinct events
	// from one buffered burst.
	got := decodeAll(t, []byte{0x1b, '[', 'A', 'x'})
	want := []keyEvent{{kind: keyUp}, {kind: keyRune, r: 'x'}}
	if len(got) != len(want) {
		t.Fatalf("got %d events %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestDrainRawEscSendsCancel drives drainRaw over a pipe: an ESC keystroke must
// send a Cancel frame, suppress further output, and still end cleanly on Done.
func TestDrainRawEscSendsCancel(t *testing.T) {
	a, c := net.Pipe()
	t.Cleanup(func() { a.Close(); c.Close() })
	conn := transport.NewConn(a) // drainRaw writes here
	peer := transport.NewConn(c) // the test reads the Cancel here

	frames := make(chan frameOrErr, 1)
	keys := make(chan keyEvent, 1)
	var out bytes.Buffer

	done := make(chan error, 1)
	go func() { done <- drainRaw(conn, frames, keys, &out) }()

	keys <- keyEvent{kind: keyEsc}

	f, err := peer.ReadFrame()
	if err != nil {
		t.Fatalf("read cancel: %v", err)
	}
	if f.Type != transport.TypeCancel {
		t.Fatalf("got %s, want %s", f.Type, transport.TypeCancel)
	}

	// A delta arriving after the cancel must not be printed.
	delta, _ := transport.TextFrame(transport.TypeAssistantDelta, "should be hidden")
	frames <- frameOrErr{frame: delta}
	frames <- frameOrErr{frame: transport.Frame{Type: transport.TypeDone}}

	if err := <-done; err != nil {
		t.Fatalf("drainRaw: %v", err)
	}
	if strings.Contains(out.String(), "should be hidden") {
		t.Errorf("output not suppressed after cancel: %q", out.String())
	}
}

func TestDecodeKeyLongCSISwallowed(t *testing.T) {
	// ESC [ 3 ~ (Delete) is consumed whole and reported as keyOther, leaving the
	// trailing rune intact.
	got := decodeAll(t, []byte{0x1b, '[', '3', '~', 'z'})
	want := []keyEvent{{kind: keyOther}, {kind: keyRune, r: 'z'}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
