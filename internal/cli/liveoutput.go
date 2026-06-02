package cli

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

// liveOutput renders a running command's streamed output. On a real terminal
// it draws a small, self-updating "tail window": the most recent lines, redrawn
// in place as new output arrives, so a long step (e.g. an image build) shows
// live progress instead of a frozen cursor. The window height is adaptive — it
// grows from nothing up to a cap derived from the terminal height — so short
// commands stay compact and long ones scroll within a bounded box. When the
// command ends the window collapses to a one-line summary.
//
// Off a terminal (piped output, or the cooked-mode fallback) it degrades to
// plain line-by-line streaming with no cursor tricks, which is correct for
// automation and logs.
type liveOutput struct {
	out    io.Writer
	indent string // per-line prefix, e.g. "  │ "
	window bool   // true: redraw a bounded tail window; false: plain streaming
	cap    int    // max visible lines (the window grows up to this)
	width  int    // terminal columns, for truncation in window mode

	lines    []string // ring of recent sanitized complete lines (≤ cap)
	pending  []byte   // bytes of the current line not yet terminated by '\n'
	total    int      // total complete lines seen, for the collapse summary
	rendered int      // window rows currently on screen (window mode)
	started  bool     // any output fed yet
}

const (
	liveMaxLines    = 10 // upper bound on the adaptive window height
	liveMinLines    = 3  // lower bound, so the box is never a sliver
	liveIndentWidth = 4  // visible width of indent ("  │ ")
)

// newLiveOutput builds a renderer. window enables the in-place tail box and
// requires a real terminal with known size; cols/rows come from term.GetSize.
func newLiveOutput(out io.Writer, window bool, cols, rows int) *liveOutput {
	l := &liveOutput{out: out, indent: "  " + muted("│") + " "}
	if window && cols > 0 {
		l.window = true
		l.width = cols
		l.cap = min(max(rows/2, liveMinLines), liveMaxLines)
	}
	return l
}

// feed ingests a streamed chunk: it splits out complete lines and, in window
// mode, redraws the tail box. In plain mode each complete line is printed as it
// is finalized; a trailing partial line is held until its newline arrives.
func (l *liveOutput) feed(p []byte) {
	l.started = true
	l.pending = append(l.pending, p...)
	for {
		i := bytes.IndexByte(l.pending, '\n')
		if i < 0 {
			break
		}
		line := sanitizeLine(string(l.pending[:i]))
		// Compact the remainder to the front so pending never aliases stale tail.
		l.pending = l.pending[:copy(l.pending, l.pending[i+1:])]
		l.total++
		if l.window {
			l.pushRing(line)
		} else {
			fmt.Fprintf(l.out, "%s%s\n", l.indent, line)
		}
	}
	if l.window {
		l.redraw()
	}
}

// finish closes out the current command's output before other content (the
// assistant's reply, the next tool, or the prompt) takes over. In window mode
// it erases the box and prints a one-line summary; in plain mode it flushes any
// trailing partial line.
func (l *liveOutput) finish() {
	if !l.started {
		return
	}
	if l.window {
		if l.rendered > 0 {
			fmt.Fprintf(l.out, "\x1b[%dA\r\x1b[J", l.rendered)
		} else {
			fmt.Fprint(l.out, "\r\x1b[K")
		}
		fmt.Fprintf(l.out, "  %s\n", muted(fmt.Sprintf("⎿ 输出 %d 行", l.lineCount())))
	} else if rest := sanitizeLine(string(l.pending)); rest != "" {
		fmt.Fprintf(l.out, "%s%s\n", l.indent, rest)
	}
	l.reset()
}

// lineCount is the total lines for the summary, counting a non-empty trailing
// partial line (a command may end mid-line).
func (l *liveOutput) lineCount() int {
	n := l.total
	if sanitizeLine(string(l.pending)) != "" {
		n++
	}
	return n
}

// pushRing appends a line, keeping at most cap, compacting in place so the
// backing array stays bounded over a long run.
func (l *liveOutput) pushRing(line string) {
	l.lines = append(l.lines, line)
	if n := len(l.lines); n > l.cap {
		l.lines = l.lines[:copy(l.lines, l.lines[n-l.cap:])]
	}
}

// redraw repaints the tail box: it moves the cursor back to the box top,
// reprints the recent lines (plus the in-progress partial line), and clears any
// rows left over from a previously taller box. Each line is truncated to the
// terminal width so nothing wraps and the row count the cursor math relies on
// stays exact.
func (l *liveOutput) redraw() {
	visible := append([]string(nil), l.lines...)
	if p := sanitizeLine(string(l.pending)); p != "" {
		visible = append(visible, p)
		if len(visible) > l.cap {
			visible = visible[len(visible)-l.cap:]
		}
	}
	if l.rendered > 0 {
		fmt.Fprintf(l.out, "\x1b[%dA\r", l.rendered)
	} else {
		fmt.Fprint(l.out, "\r")
	}
	for _, ln := range visible {
		fmt.Fprintf(l.out, "\x1b[K%s%s\n", l.indent, l.truncate(ln))
	}
	if len(visible) < l.rendered {
		fmt.Fprint(l.out, "\x1b[J") // erase the now-vacated rows below
	}
	l.rendered = len(visible)
}

// truncate cuts s to fit one terminal row after the indent, measuring by cell
// width (CJK counts as two) and appending an ellipsis when it overflows.
func (l *liveOutput) truncate(s string) string {
	avail := max(l.width-liveIndentWidth, 1)
	total := 0
	for _, r := range s {
		total += runeWidth(r)
	}
	if total <= avail {
		return s
	}
	budget := avail - 1 // reserve one cell for the ellipsis
	w := 0
	var b strings.Builder
	for _, r := range s {
		rw := runeWidth(r)
		if w+rw > budget {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	b.WriteString("…")
	return b.String()
}

func (l *liveOutput) reset() {
	l.started = false
	l.rendered = 0
	l.total = 0
	l.lines = l.lines[:0]
	l.pending = l.pending[:0]
}

// sanitizeLine turns one raw output line into plain single-line text safe to
// place in the window: it collapses carriage returns (progress animations keep
// only their final segment, mimicking in-place overwrite), strips ANSI escape
// sequences, drops other control bytes, and turns tabs into a space. Bytes ≥
// 0x80 pass through untouched, so multibyte (CJK) runes survive.
func sanitizeLine(s string) string {
	if i := strings.LastIndexByte(s, '\r'); i >= 0 {
		s = s[i+1:]
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b: // ESC: skip the whole escape sequence
			i = skipEscape(s, i)
		case c == '\t':
			b.WriteByte(' ')
			i++
		case c < 0x20 || c == 0x7f: // other control bytes
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// skipEscape returns the index just past the ANSI escape sequence beginning at
// s[i] (an ESC byte). It handles CSI (ESC [ … final 0x40–0x7e), OSC (ESC ] …
// terminated by BEL or ST), and short two-byte escapes.
func skipEscape(s string, i int) int {
	j := i + 1
	if j >= len(s) {
		return j
	}
	switch s[j] {
	case '[': // CSI
		j++
		for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
			j++
		}
		if j < len(s) {
			j++ // consume the final byte
		}
		return j
	case ']': // OSC
		j++
		for j < len(s) {
			if s[j] == 0x07 { // BEL
				return j + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' { // ST
				return j + 2
			}
			j++
		}
		return j
	default:
		// Lone or unrecognized ESC: drop just the ESC byte and keep scanning, so
		// a following normal character isn't swallowed.
		return j
	}
}
