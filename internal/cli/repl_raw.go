// Raw-mode REPL. On a real terminal the client runs this loop so a running
// turn can be interrupted: while the agent streams or runs tools, pressing ESC
// (or Ctrl-C) sends a Cancel frame and returns to the prompt. Raw mode means
// the kernel no longer cooks input (line editing, echo) or output (NL->CRNL),
// so this file hand-rolls a small line editor, decodes keystrokes itself, and
// routes output through crlfWriter. Non-TTY sessions use replCooked instead.
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/areming/ops-agent/internal/transport"
)

// errRawUnavailable signals that raw mode couldn't be entered, so the caller
// should fall back to the line-based loop.
var errRawUnavailable = errors.New("raw mode unavailable")

// replRaw runs the interactive session in raw mode. It returns errRawUnavailable
// (and changes nothing) when the terminal can't be put into raw mode.
func replRaw(conn *transport.Conn, label string) error {
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return errRawUnavailable
	}
	defer term.Restore(fd, old) //nolint:errcheck // best-effort restore on exit

	out := &crlfWriter{w: os.Stdout}
	keys, stopKeys := startKeyReader(os.Stdin)
	defer stopKeys()
	frames, stopFrames := startFrameReader(conn)
	defer stopFrames()

	prompt := fmt.Sprintf("%s %s ", muted("["+label+"]"), accent("›"))
	ed := &lineEditor{}
	for {
		fmt.Fprintf(out, "\n%s", prompt)
		line, outcome := readLine(ed, keys, frames, out)
		if outcome != lineSubmit {
			return nil // Ctrl-D/Ctrl-C at the prompt, or the connection dropped
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "/") {
			quit, err := handleSlashRaw(conn, frames, out, trimmed)
			if err != nil {
				return err
			}
			if quit {
				return nil
			}
			continue
		}
		uf, err := transport.TextFrame(transport.TypeUserInput, line)
		if err != nil {
			return err
		}
		if err := conn.WriteFrame(uf); err != nil {
			return err
		}
		if err := drainRaw(conn, frames, keys, out); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// drainRaw handles one turn until Done, while watching the keyboard: ESC or
// Ctrl-C sends a Cancel frame and suppresses further output for the turn (the
// agent still ends it with Done). Confirm prompts consume keys directly, so ESC
// there means "deny" rather than "cancel the turn".
func drainRaw(conn *transport.Conn, frames <-chan frameOrErr, keys <-chan keyEvent, out io.Writer) error {
	sp := startSpinner("思考中…")
	stopSp := func() {
		if sp != nil {
			sp.stop()
			sp = nil
		}
	}
	var live *liveOutput
	finishLive := func() {
		if live != nil {
			live.finish()
			live = nil
		}
	}
	replyOpen := false
	canceling := false
	for {
		select {
		case ev, ok := <-keys:
			if !ok {
				keys = nil // stdin closed; let the turn finish via frames
				continue
			}
			if !canceling && (ev.kind == keyEsc || ev.kind == keyCtrlC) {
				_ = conn.WriteFrame(transport.Frame{Type: transport.TypeCancel})
				canceling = true
				stopSp()
				finishLive()
				fmt.Fprintf(out, "\n%s\n", muted("⎋ 正在取消…"))
			}
		case fe, ok := <-frames:
			if !ok {
				return io.EOF
			}
			if fe.err != nil {
				return fe.err
			}
			stopSp()
			switch f := fe.frame; f.Type {
			case transport.TypeAssistantDelta:
				if canceling {
					continue
				}
				finishLive()
				s, _ := f.Text()
				if !replyOpen {
					fmt.Fprint(out, "\n")
					replyOpen = true
				}
				fmt.Fprint(out, s)
			case transport.TypeToolStart:
				if canceling {
					continue
				}
				finishLive()
				replyOpen = false
				var p transport.ToolStartPayload
				_ = f.Decode(&p)
				fmt.Fprintf(out, "\n%s %s%s\n", accent("⏺"), bold(p.Tool), muted("("+p.Command+")"))
				sp = startSpinner("执行中…")
			case transport.TypeToolOutput:
				if canceling {
					continue
				}
				replyOpen = false
				if live == nil {
					cols, rows, _ := term.GetSize(int(os.Stdout.Fd()))
					live = newLiveOutput(out, true, cols, rows)
				}
				s, _ := f.Text()
				live.feed([]byte(s))
			case transport.TypeConfirmRequest:
				finishLive()
				replyOpen = false
				var approved, always bool
				if !canceling {
					var p transport.ConfirmRequestPayload
					_ = f.Decode(&p)
					approved, always = askConfirmRaw(keys, out, p)
				}
				reply, err := transport.PayloadFrame(transport.TypeConfirmReply,
					transport.ConfirmReplyPayload{Approved: approved, Always: always})
				if err != nil {
					return err
				}
				if err := conn.WriteFrame(reply); err != nil {
					return err
				}
			case transport.TypeError:
				finishLive()
				s, _ := f.Text()
				fmt.Fprintf(out, "\n%s %s\n", errLabel("✗ 错误"), s)
			case transport.TypeDone:
				finishLive()
				fmt.Fprint(out, "\n")
				return nil
			}
		}
	}
}

// askConfirmRaw renders the arrow-key confirm menu, consuming decoded key events
// (the terminal is already raw). ESC / Ctrl-C deny; Enter chooses the highlighted
// option; j/k and digits work too. A closed key channel denies.
func askConfirmRaw(keys <-chan keyEvent, out io.Writer, p transport.ConfirmRequestPayload) (approved, always bool) {
	fmt.Fprintf(out, "\n%s  %s\n  命令: %s\n  原因: %s\n  %s\n",
		rgb(235, 180, 60, "⚠ 需要确认"),
		muted("risk="+p.Risk+" · "+p.Tool),
		bold(p.Command), p.Reason,
		muted("↑/↓ 选择 · Enter 确认 · Esc 拒绝"))

	sel := 0
	render := func() {
		for i, c := range confirmChoices {
			marker, label := "  ", c.label
			if i == sel {
				marker, label = accent("❯")+" ", bold(c.label)
			}
			fmt.Fprintf(out, "\r\x1b[K%s%s  %s\n", marker, label, muted(c.desc))
		}
	}
	render()
	for ev := range keys {
		switch {
		case ev.kind == keyEsc || ev.kind == keyCtrlC:
			return false, false
		case ev.kind == keyEnter:
			c := confirmChoices[sel]
			return c.approved, c.always
		case ev.kind == keyUp || (ev.kind == keyRune && ev.r == 'k'):
			if sel > 0 {
				sel--
			}
		case ev.kind == keyDown || (ev.kind == keyRune && ev.r == 'j'):
			if sel < len(confirmChoices)-1 {
				sel++
			}
		case ev.kind == keyRune && ev.r >= '1' && ev.r <= '9':
			if i := int(ev.r - '1'); i < len(confirmChoices) {
				c := confirmChoices[i]
				return c.approved, c.always
			}
		default:
			continue // ignore other keys without redrawing
		}
		fmt.Fprintf(out, "\x1b[%dA", len(confirmChoices)) // back to the first option line
		render()
	}
	return false, false
}

// handleSlashRaw is the raw-path /command handler. It mirrors handleSlash but
// reads the agent's control reply from the shared frame channel instead of the
// connection directly (a second reader would race the frame goroutine).
func handleSlashRaw(conn *transport.Conn, frames <-chan frameOrErr, out io.Writer, line string) (quit bool, err error) {
	cmd, arg, _ := strings.Cut(strings.TrimPrefix(line, "/"), " ")
	cmd = strings.ToLower(cmd)
	arg = strings.TrimSpace(arg)
	switch cmd {
	case "help", "?":
		printSlashHelpTo(out)
		return false, nil
	case "quit", "exit", "q":
		return true, nil
	case "clear":
		fmt.Fprint(out, "\x1b[H\x1b[2J")
		return false, sendControlRaw(conn, frames, out, cmd, arg)
	case "models", "model":
		return false, sendControlRaw(conn, frames, out, "models", arg)
	case "logs", "yolo":
		return false, sendControlRaw(conn, frames, out, cmd, arg)
	default:
		fmt.Fprintf(out, "未知命令 /%s（试试 /help）\n", cmd)
		return false, nil
	}
}

// sendControlRaw writes a control request and prints the agent's single reply,
// taken from the frame channel.
func sendControlRaw(conn *transport.Conn, frames <-chan frameOrErr, out io.Writer, cmd, arg string) error {
	req, err := transport.PayloadFrame(transport.TypeControlRequest, transport.ControlRequestPayload{Cmd: cmd, Arg: arg})
	if err != nil {
		return err
	}
	if err := conn.WriteFrame(req); err != nil {
		return err
	}
	fe, ok := <-frames
	if !ok {
		return io.EOF
	}
	if fe.err != nil {
		return fe.err
	}
	if fe.frame.Type != transport.TypeControlReply {
		return fmt.Errorf("expected control reply, got %s", fe.frame.Type)
	}
	var reply transport.ControlReplyPayload
	if err := fe.frame.Decode(&reply); err != nil {
		return err
	}
	if reply.Err != "" {
		fmt.Fprintf(out, "%s %s\n", errLabel("✗"), reply.Err)
		return nil
	}
	fmt.Fprintln(out, reply.Text)
	return nil
}

// --- prompt line editing ----------------------------------------------------

// lineOutcome is how a prompt read ended.
type lineOutcome int

const (
	lineSubmit     lineOutcome = iota // user pressed Enter
	lineExit                          // Ctrl-D / Ctrl-C at the prompt
	lineConnClosed                    // the connection dropped while at the prompt
)

// readLine reads one prompt line, echoing edits to out. It also watches frames
// so a connection drop at the prompt ends the session instead of hanging.
func readLine(ed *lineEditor, keys <-chan keyEvent, frames <-chan frameOrErr, out io.Writer) (string, lineOutcome) {
	ed.reset()
	for {
		select {
		case ev, ok := <-keys:
			if !ok {
				return "", lineExit
			}
			switch ed.feed(ev, out) {
			case editSubmit:
				return ed.line(), lineSubmit
			case editExit:
				if ev.kind == keyCtrlC {
					fmt.Fprint(out, "^C\n")
				}
				return "", lineExit
			}
		case fe, ok := <-frames:
			if !ok || fe.err != nil {
				return "", lineConnClosed
			}
			// No frames are expected between turns; ignore strays.
		}
	}
}

// lineEditor accumulates a prompt line as runes so backspace and width handling
// are correct for CJK input. It supports typing, Backspace, Ctrl-U (kill line),
// Ctrl-W (erase word), Enter, and exit on Ctrl-C / Ctrl-D — matching what the
// kernel's canonical mode used to give for free. Arrow keys and ESC are inert at
// the prompt (ESC's role is interrupting a running turn, handled in drainRaw).
type lineEditor struct {
	buf []rune
}

type editResult int

const (
	editContinue editResult = iota
	editSubmit
	editExit
)

func (e *lineEditor) reset()       { e.buf = e.buf[:0] }
func (e *lineEditor) line() string { return string(e.buf) }

// truncate cuts the buffer down to n runes, erasing the removed tail from the
// screen by its total cell width (the cursor is always at the end).
func (e *lineEditor) truncate(n int, w io.Writer) {
	if n >= len(e.buf) {
		return
	}
	width := 0
	for _, r := range e.buf[n:] {
		width += runeWidth(r)
	}
	e.buf = e.buf[:n]
	if width > 0 {
		eraseCells(w, width)
	}
}

func (e *lineEditor) feed(ev keyEvent, w io.Writer) editResult {
	switch ev.kind {
	case keyRune:
		e.buf = append(e.buf, ev.r)
		io.WriteString(w, string(ev.r))
		return editContinue
	case keyBackspace:
		if n := len(e.buf); n > 0 {
			r := e.buf[n-1]
			e.buf = e.buf[:n-1]
			eraseCells(w, runeWidth(r))
		}
		return editContinue
	case keyCtrlU:
		e.truncate(0, w)
		return editContinue
	case keyCtrlW:
		n := len(e.buf)
		for n > 0 && e.buf[n-1] == ' ' { // skip trailing spaces
			n--
		}
		for n > 0 && e.buf[n-1] != ' ' { // then the word itself
			n--
		}
		e.truncate(n, w)
		return editContinue
	case keyEnter:
		io.WriteString(w, "\n")
		return editSubmit
	case keyCtrlC:
		return editExit
	case keyCtrlD:
		if len(e.buf) == 0 {
			return editExit
		}
		return editContinue
	default:
		return editContinue
	}
}

// eraseCells removes the last glyph (1 or 2 cells wide) from the screen.
func eraseCells(w io.Writer, width int) {
	if width < 1 {
		width = 1
	}
	io.WriteString(w, strings.Repeat("\b", width))
	io.WriteString(w, strings.Repeat(" ", width))
	io.WriteString(w, strings.Repeat("\b", width))
}

// runeWidth reports a rune's terminal cell width: 0 for control runes, 2 for
// East Asian wide/fullwidth (the CJK case that matters for backspace), else 1.
func runeWidth(r rune) int {
	if r == 0 || r < 0x20 || (r >= 0x7f && r < 0xa0) {
		return 0
	}
	if isWide(r) {
		return 2
	}
	return 1
}

func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115f, // Hangul Jamo
		r >= 0x2e80 && r <= 0x303e,   // CJK radicals, Kangxi, CJK symbols
		r >= 0x3041 && r <= 0x33ff,   // Hiragana, Katakana, CJK symbols
		r >= 0x3400 && r <= 0x4dbf,   // CJK ext A
		r >= 0x4e00 && r <= 0x9fff,   // CJK unified
		r >= 0xa000 && r <= 0xa4cf,   // Yi
		r >= 0xac00 && r <= 0xd7a3,   // Hangul syllables
		r >= 0xf900 && r <= 0xfaff,   // CJK compatibility
		r >= 0xfe30 && r <= 0xfe4f,   // CJK compatibility forms
		r >= 0xff00 && r <= 0xff60,   // Fullwidth forms
		r >= 0xffe0 && r <= 0xffe6,   // Fullwidth signs
		r >= 0x20000 && r <= 0x3fffd: // CJK ext B and beyond
		return true
	}
	return false
}

// --- key decoding -----------------------------------------------------------

type keyKind int

const (
	keyRune keyKind = iota
	keyEnter
	keyBackspace
	keyEsc
	keyCtrlC
	keyCtrlD
	keyCtrlU // kill whole line
	keyCtrlW // erase previous word
	keyUp
	keyDown
	keyLeft
	keyRight
	keyOther
)

type keyEvent struct {
	kind keyKind
	r    rune // valid when kind == keyRune
}

// startKeyReader decodes r in a goroutine and delivers key events on the
// returned channel. The stop func lets the goroutine exit if it is parked on a
// send; a goroutine blocked in a read is released when stdin closes (process
// exit), which is the only way replRaw ends.
func startKeyReader(r io.Reader) (<-chan keyEvent, func()) {
	br := bufio.NewReader(r)
	ch := make(chan keyEvent)
	done := make(chan struct{})
	go func() {
		defer close(ch)
		for {
			ev, err := decodeKey(br)
			if err != nil {
				return
			}
			select {
			case ch <- ev:
			case <-done:
				return
			}
		}
	}()
	return ch, func() { close(done) }
}

// decodeKey reads one key from br. Escape sequences (arrows) are recognized by
// peeking the buffer: a real arrow key arrives as one ESC-[-X burst, so bytes
// are already buffered after the ESC; a lone ESC press leaves the buffer empty.
func decodeKey(br *bufio.Reader) (keyEvent, error) {
	b, err := br.ReadByte()
	if err != nil {
		return keyEvent{}, err
	}
	switch {
	case b == 0x1b: // ESC
		if br.Buffered() == 0 {
			return keyEvent{kind: keyEsc}, nil
		}
		b2, err := br.ReadByte()
		if err != nil {
			return keyEvent{kind: keyEsc}, nil
		}
		if b2 != '[' && b2 != 'O' {
			return keyEvent{kind: keyOther}, nil
		}
		b3, err := br.ReadByte()
		if err != nil {
			return keyEvent{kind: keyOther}, nil
		}
		switch b3 {
		case 'A':
			return keyEvent{kind: keyUp}, nil
		case 'B':
			return keyEvent{kind: keyDown}, nil
		case 'C':
			return keyEvent{kind: keyRight}, nil
		case 'D':
			return keyEvent{kind: keyLeft}, nil
		default:
			// Longer CSI (e.g. ESC [ 3 ~): swallow up to the final byte.
			for br.Buffered() > 0 {
				nb, _ := br.ReadByte()
				if nb >= 0x40 && nb <= 0x7e {
					break
				}
			}
			return keyEvent{kind: keyOther}, nil
		}
	case b == '\r' || b == '\n':
		return keyEvent{kind: keyEnter}, nil
	case b == 0x7f || b == 0x08: // DEL or Backspace
		return keyEvent{kind: keyBackspace}, nil
	case b == 0x03:
		return keyEvent{kind: keyCtrlC}, nil
	case b == 0x04:
		return keyEvent{kind: keyCtrlD}, nil
	case b == 0x15: // Ctrl-U
		return keyEvent{kind: keyCtrlU}, nil
	case b == 0x17: // Ctrl-W
		return keyEvent{kind: keyCtrlW}, nil
	case b < 0x20:
		return keyEvent{kind: keyOther}, nil
	default:
		r, err := decodeRune(br, b)
		if err != nil {
			return keyEvent{}, err
		}
		return keyEvent{kind: keyRune, r: r}, nil
	}
}

// decodeRune completes a UTF-8 rune whose leading byte is b, reading any
// continuation bytes from br. This keeps multibyte input (e.g. Chinese) intact.
func decodeRune(br *bufio.Reader, b byte) (rune, error) {
	if b < utf8.RuneSelf {
		return rune(b), nil
	}
	var n int
	switch {
	case b&0xe0 == 0xc0:
		n = 1
	case b&0xf0 == 0xe0:
		n = 2
	case b&0xf8 == 0xf0:
		n = 3
	default:
		return utf8.RuneError, nil
	}
	buf := make([]byte, 1, 4)
	buf[0] = b
	for i := 0; i < n; i++ {
		cb, err := br.ReadByte()
		if err != nil {
			return utf8.RuneError, err
		}
		buf = append(buf, cb)
	}
	r, _ := utf8.DecodeRune(buf)
	return r, nil
}

// --- frame reading + output -------------------------------------------------

// frameOrErr carries one read result from the connection: a frame, or the error
// that ended the stream.
type frameOrErr struct {
	frame transport.Frame
	err   error
}

// startFrameReader reads frames from conn in a goroutine. One reader owns the
// connection for the session so drainRaw and the prompt can both observe frames
// without a second reader racing it. The stop func releases a parked send.
func startFrameReader(conn *transport.Conn) (<-chan frameOrErr, func()) {
	ch := make(chan frameOrErr)
	done := make(chan struct{})
	go func() {
		defer close(ch)
		for {
			f, err := conn.ReadFrame()
			select {
			case ch <- frameOrErr{frame: f, err: err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return ch, func() { close(done) }
}

// crlfWriter rewrites a bare '\n' to '\r\n' so output lands correctly while the
// terminal is in raw mode, which disables the kernel's own NL->CRNL mapping. A
// '\n' already preceded by '\r' is passed through untouched.
type crlfWriter struct {
	w    io.Writer
	last byte
}

func (c *crlfWriter) Write(p []byte) (int, error) {
	out := make([]byte, 0, len(p)+8)
	for _, b := range p {
		if b == '\n' && c.last != '\r' {
			out = append(out, '\r')
		}
		out = append(out, b)
		c.last = b
	}
	if _, err := c.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}
