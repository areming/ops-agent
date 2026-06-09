package cli

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"strings"
	"testing"
	"time"

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
		{"tab", []byte("\t"), keyEvent{kind: keyTab}},
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

// TestStatusBody checks the animated status text: a plain label under one
// second, then a climbing elapsed-seconds counter (what signals "not frozen").
func TestStatusBody(t *testing.T) {
	if got := statusBody("执行中", 200*time.Millisecond); got != "执行中…" {
		t.Errorf("sub-second = %q, want 执行中…", got)
	}
	if got := statusBody("思考中", 12*time.Second); got != "思考中… 12s" {
		t.Errorf("with elapsed = %q, want 思考中… 12s", got)
	}
}

// TestDrainRawAnimatesDuringSilentGap proves the status line fills a silent
// gap: after a tool starts and then nothing arrives, the debounced ticker must
// draw an "执行中" indicator so the session never looks frozen.
func TestDrainRawAnimatesDuringSilentGap(t *testing.T) {
	a, c := net.Pipe()
	t.Cleanup(func() { a.Close(); c.Close() })
	conn := transport.NewConn(a)
	_ = c

	frames := make(chan frameOrErr, 2)
	keys := make(chan keyEvent)
	var out bytes.Buffer

	done := make(chan error, 1)
	go func() { done <- drainRaw(conn, frames, keys, &out) }()

	ts, _ := transport.PayloadFrame(transport.TypeToolStart, transport.ToolStartPayload{Tool: "shell", Command: "sleep 1"})
	frames <- frameOrErr{frame: ts}
	time.Sleep(350 * time.Millisecond) // longer than debounce + a couple of ticks
	frames <- frameOrErr{frame: transport.Frame{Type: transport.TypeDone}}

	if err := <-done; err != nil {
		t.Fatalf("drainRaw: %v", err)
	}
	if !strings.Contains(out.String(), "执行中") {
		t.Errorf("expected an 执行中 status during the silent gap, got %q", out.String())
	}
}

func TestCompleteCommand(t *testing.T) {
	// built-ins plus a couple of custom commands sharing a prefix.
	cands := []string{"clear", "commands", "exit", "help", "logs", "model", "yolo", "restart-db", "restart-nginx"}
	cases := []struct {
		name       string
		token      string
		wantExtend string
		wantCount  int
	}{
		{"unique grows fully", "hel", "p", 1},
		{"already complete unique", "help", "", 1},
		{"no match", "zzz", "", 0},
		{"shared prefix grows to common", "r", "estart-", 2},
		{"at common prefix lists", "restart-", "", 2},
		{"case insensitive", "HE", "lp", 1},
		{"two builtins common prefix", "c", "", 2}, // clear, commands → lcp "c"
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			extend, matches := completeCommand(c.token, cands)
			if extend != c.wantExtend {
				t.Errorf("extend = %q, want %q", extend, c.wantExtend)
			}
			if len(matches) != c.wantCount {
				t.Errorf("matches = %v, want %d of them", matches, c.wantCount)
			}
		})
	}
}

func TestLineEditorComplete(t *testing.T) {
	// A completer over a fixed candidate set, prefix-matched like the real one.
	cands := []string{"commands", "clear", "model", "restart-db", "restart-nginx"}
	comp := func(token string) (string, []string) { return completeCommand(token, cands) }
	const prompt = "> "

	// Unique match fills in the rest and opens an argument slot with a space.
	ed := &lineEditor{}
	ed.reset()
	ed.append("/mod", io.Discard)
	var echo bytes.Buffer
	ed.complete(comp, prompt, &echo)
	if ed.line() != "/model " {
		t.Errorf("unique completion line = %q, want %q", ed.line(), "/model ")
	}

	// Several matches: line is grown only to the common prefix, then listed.
	ed.reset()
	ed.append("/r", io.Discard)
	echo.Reset()
	ed.complete(comp, prompt, &echo)
	if ed.line() != "/restart-" {
		t.Errorf("ambiguous completion line = %q, want %q", ed.line(), "/restart-")
	}
	// A second Tab at the common prefix lists both and reprints the prompt.
	echo.Reset()
	ed.complete(comp, prompt, &echo)
	if ed.line() != "/restart-" {
		t.Errorf("line changed on list = %q, want unchanged", ed.line())
	}
	if !strings.Contains(echo.String(), "restart-db") || !strings.Contains(echo.String(), "restart-nginx") {
		t.Errorf("listing missing candidates: %q", echo.String())
	}
	if !strings.Contains(echo.String(), prompt+"/restart-") {
		t.Errorf("listing should reprint prompt + line: %q", echo.String())
	}

	// No match: line is untouched, a notice is shown.
	ed.reset()
	ed.append("/zzz", io.Discard)
	echo.Reset()
	ed.complete(comp, prompt, &echo)
	if ed.line() != "/zzz" {
		t.Errorf("no-match line = %q, want unchanged", ed.line())
	}
	if !strings.Contains(echo.String(), "无匹配命令") {
		t.Errorf("no-match should print a notice: %q", echo.String())
	}

	// Inert once an argument has begun (a space is present).
	ed.reset()
	ed.append("/model gpt", io.Discard)
	echo.Reset()
	ed.complete(comp, prompt, &echo)
	if ed.line() != "/model gpt" || echo.Len() != 0 {
		t.Errorf("Tab in args should be inert: line=%q echo=%q", ed.line(), echo.String())
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
