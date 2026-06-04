// The /model panel: view, switch, delete and add model profiles in-session.
// Data and actions live on the agent (local or the remote daemon); this is a
// thin client over control frames. On a raw terminal it renders an arrow-key
// panel reading the repl's shared key channel (which owns stdin); on a non-TTY
// session it degrades to a text list and switch-by-name.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"

	"github.com/areming/ops-agent/internal/transport"
)

// controlRoundTrip sends a control request and returns the decoded reply. The
// two repl modes differ only in where the reply is read from — the connection
// directly (cooked) or the shared frame channel (raw) — so the /model logic is
// written against this and works in both.
type controlRoundTrip func(cmd, arg string) (transport.ControlReplyPayload, error)

// connControl reads the reply straight from conn (cooked path owns the reader).
func connControl(conn *transport.Conn) controlRoundTrip {
	return func(cmd, arg string) (transport.ControlReplyPayload, error) {
		var zero transport.ControlReplyPayload
		req, err := transport.PayloadFrame(transport.TypeControlRequest, transport.ControlRequestPayload{Cmd: cmd, Arg: arg})
		if err != nil {
			return zero, err
		}
		if err := conn.WriteFrame(req); err != nil {
			return zero, err
		}
		f, err := conn.ReadFrame()
		if err != nil {
			return zero, err
		}
		if f.Type != transport.TypeControlReply {
			return zero, fmt.Errorf("expected control reply, got %s", f.Type)
		}
		var reply transport.ControlReplyPayload
		return reply, f.Decode(&reply)
	}
}

// framesControl reads the reply from the shared frame channel (raw path: a
// goroutine owns the connection reader).
func framesControl(conn *transport.Conn, frames <-chan frameOrErr) controlRoundTrip {
	return func(cmd, arg string) (transport.ControlReplyPayload, error) {
		var zero transport.ControlReplyPayload
		req, err := transport.PayloadFrame(transport.TypeControlRequest, transport.ControlRequestPayload{Cmd: cmd, Arg: arg})
		if err != nil {
			return zero, err
		}
		if err := conn.WriteFrame(req); err != nil {
			return zero, err
		}
		fe, ok := <-frames
		if !ok {
			return zero, io.EOF
		}
		if fe.err != nil {
			return zero, fe.err
		}
		if fe.frame.Type != transport.TypeControlReply {
			return zero, fmt.Errorf("expected control reply, got %s", fe.frame.Type)
		}
		var reply transport.ControlReplyPayload
		return reply, fe.frame.Decode(&reply)
	}
}

// fetchModels asks the agent for its saved profiles.
func fetchModels(rt controlRoundTrip) ([]transport.ModelProfile, error) {
	reply, err := rt(transport.CmdModelList, "")
	if err != nil {
		return nil, err
	}
	if reply.Err != "" {
		return nil, fmt.Errorf("%s", reply.Err)
	}
	var lr transport.ModelListReply
	if err := json.Unmarshal([]byte(reply.Text), &lr); err != nil {
		return nil, err
	}
	return lr.Profiles, nil
}

// printReply shows a control reply: a non-empty Err as an error marker,
// otherwise the text.
func printReply(out io.Writer, reply transport.ControlReplyPayload) {
	if reply.Err != "" {
		fmt.Fprintf(out, "%s %s\n", errLabel("✗"), reply.Err)
		return
	}
	if reply.Text != "" {
		fmt.Fprintln(out, reply.Text)
	}
}

// modelManageText is the non-interactive /model (cooked / non-TTY): switch by
// name when arg is given, otherwise print the saved profiles.
func modelManageText(rt controlRoundTrip, out io.Writer, arg string) error {
	if arg != "" {
		reply, err := rt(transport.CmdModelSwitch, arg)
		if err != nil {
			return err
		}
		printReply(out, reply)
		return nil
	}
	profs, err := fetchModels(rt)
	if err != nil {
		return err
	}
	if len(profs) == 0 {
		fmt.Fprintln(out, muted("（还没有模型配置；在交互终端里 /model 可新增）"))
		return nil
	}
	for _, p := range profs {
		mark := "  "
		if p.Active {
			mark = accent("* ")
		}
		fmt.Fprintf(out, "%s%s\n", mark, p.Label)
	}
	fmt.Fprintln(out, muted("切换：/model <名称>"))
	return nil
}

// runModelPanel drives the interactive /model panel on the raw path: it lists
// saved profiles, switches on Enter, deletes on 'd', and adds via the last row.
// It refreshes after each mutation and returns when the user picks/exits.
func runModelPanel(rt controlRoundTrip, keys <-chan keyEvent, out io.Writer) error {
	for {
		profs, err := fetchModels(rt)
		if err != nil {
			return err
		}
		opts := make([]menuOption, 0, len(profs)+1)
		for _, p := range profs {
			desc := p.Provider
			if p.Active {
				desc = p.Provider + "  " + accent("当前")
			}
			opts = append(opts, menuOption{label: p.Label, desc: desc})
		}
		opts = append(opts, menuOption{label: "+ 新增模型…", desc: "配置一个新的 provider / 模型"})

		idx, res := keysMenu(keys, out, "模型", opts, 0, true)
		switch res {
		case menuBack, menuCancel:
			return nil
		case menuDelete:
			if idx < len(profs) {
				reply, err := rt(transport.CmdModelDelete, profs[idx].ID)
				if err != nil {
					return err
				}
				printReply(out, reply)
			}
			continue
		case menuPicked:
			if idx == len(profs) { // the "+ 新增" row
				if err := addModelFlow(rt, keys, out); err != nil && err != errWizardCanceled {
					return err
				}
				continue
			}
			if profs[idx].Active {
				return nil // already active; just close
			}
			reply, err := rt(transport.CmdModelSwitch, profs[idx].ID)
			if err != nil {
				return err
			}
			printReply(out, reply)
			return nil
		}
	}
}

// addModelFlow runs the in-panel "new model" wizard over the key channel:
// provider → model → (custom base URL) → API key, then ships an add request to
// the agent, which seals the key and makes the profile active.
func addModelFlow(rt controlRoundTrip, keys <-chan keyEvent, out io.Writer) error {
	pidx, res := keysMenu(keys, out, "选择模型提供商", providerMenuOptions(), 0, false)
	if res != menuPicked {
		return errWizardCanceled
	}
	entry := providerCatalog[pidx]

	modelName, err := pickModel(keys, out, entry)
	if err != nil {
		return err
	}

	baseURL := entry.BaseURL
	if entry.ID == "custom" {
		u, ok := keysReadLine(keys, out, "base URL（OpenAI 兼容地址）", false)
		if !ok || u == "" {
			return errWizardCanceled
		}
		baseURL = u
	}

	key, ok := keysReadLine(keys, out, "粘贴 API key", true)
	if !ok || key == "" {
		return errWizardCanceled
	}

	req := transport.ModelAddRequest{
		Label:    entry.Label + " / " + modelName,
		Provider: entry.Adapter,
		Model:    modelName,
		BaseURL:  baseURL,
		Key:      key,
	}
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	reply, err := rt(transport.CmdModelAdd, string(b))
	if err != nil {
		return err
	}
	printReply(out, reply)
	return nil
}

// pickModel chooses a model from the provider's list (with a Custom escape
// hatch), or asks for the name directly when the provider has no preset list.
func pickModel(keys <-chan keyEvent, out io.Writer, entry providerInfo) (string, error) {
	if len(entry.Models) == 0 {
		m, ok := keysReadLine(keys, out, "模型名（手动输入）", false)
		if !ok || m == "" {
			return "", errWizardCanceled
		}
		return m, nil
	}
	opts := make([]menuOption, 0, len(entry.Models)+1)
	for _, m := range entry.Models {
		opts = append(opts, menuOption{label: m})
	}
	opts = append(opts, menuOption{label: "Custom", desc: "手动输入模型名"})
	initial := 0
	for i, m := range entry.Models {
		if m == entry.DefaultModel {
			initial = i
			break
		}
	}
	idx, res := keysMenu(keys, out, "选择模型", opts, initial, false)
	if res != menuPicked {
		return "", errWizardCanceled
	}
	if idx == len(entry.Models) { // the Custom row
		m, ok := keysReadLine(keys, out, "模型名（手动输入）", false)
		if !ok || m == "" {
			return "", errWizardCanceled
		}
		return m, nil
	}
	return entry.Models[idx], nil
}

// keysMenu renders an arrow-key menu reading the repl's key channel and writing
// to out (a crlfWriter, so plain \n suffices). It mirrors selectMenu's look and
// scrolling but for the raw-repl context, where stdin is owned by the key
// reader. allowDelete makes 'd' return menuDelete for the selected row.
func keysMenu(keys <-chan keyEvent, out io.Writer, title string, opts []menuOption, initial int, allowDelete bool) (int, menuResult) {
	hint := "↑/↓ 选择 · Enter 确认 · Esc 返回"
	if allowDelete {
		hint = "↑/↓ 选择 · Enter 切换 · d 删除 · Esc 退出"
	}
	fmt.Fprintf(out, "\n%s %s  %s\n", accent("◆"), bold(title), muted(hint))

	vh, scroll := len(opts), false
	maxVisible := 10
	if _, rows, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		if m := rows - 5; m < maxVisible {
			maxVisible = m
		}
	}
	if maxVisible < 3 {
		maxVisible = 3
	}
	if vh > maxVisible {
		vh, scroll = maxVisible, true
	}
	blockH := vh
	if scroll {
		blockH = vh + 2
	}

	sel := initial
	if sel < 0 || sel >= len(opts) {
		sel = 0
	}
	top := 0
	clampView := func() {
		if sel < top {
			top = sel
		}
		if sel >= top+vh {
			top = sel - vh + 1
		}
		if hi := len(opts) - vh; top > hi {
			top = hi
		}
		if top < 0 {
			top = 0
		}
	}
	clampView()

	render := func() {
		if scroll {
			if top > 0 {
				fmt.Fprintf(out, "\x1b[K  %s\n", muted("▲ 更多"))
			} else {
				fmt.Fprint(out, "\x1b[K\n")
			}
		}
		for i := top; i < top+vh; i++ {
			o := opts[i]
			marker, label := "  ", o.label
			if i == sel {
				marker, label = accent("❯")+" ", bold(o.label)
			}
			if o.desc != "" {
				fmt.Fprintf(out, "\x1b[K%s%s  %s\n", marker, label, muted(o.desc))
			} else {
				fmt.Fprintf(out, "\x1b[K%s%s\n", marker, label)
			}
		}
		if scroll {
			if rem := len(opts) - (top + vh); rem > 0 {
				fmt.Fprintf(out, "\x1b[K  %s\n", muted(fmt.Sprintf("▼ 更多 (%d)", rem)))
			} else {
				fmt.Fprint(out, "\x1b[K\n")
			}
		}
	}
	render()
	redraw := func() {
		clampView()
		fmt.Fprintf(out, "\x1b[%dA", blockH)
		render()
	}

	for ev := range keys {
		switch ev.kind {
		case keyCtrlC:
			return 0, menuCancel
		case keyEsc:
			return 0, menuBack
		case keyEnter:
			return sel, menuPicked
		case keyUp:
			if sel > 0 {
				sel--
			}
		case keyDown:
			if sel < len(opts)-1 {
				sel++
			}
		case keyRune:
			switch {
			case allowDelete && (ev.r == 'd' || ev.r == 'D'):
				return sel, menuDelete
			case ev.r == 'k' && sel > 0:
				sel--
			case ev.r == 'j' && sel < len(opts)-1:
				sel++
			default:
				continue
			}
		default:
			continue
		}
		redraw()
	}
	return 0, menuCancel
}

// keysReadLine reads a line over the key channel, echoing each rune (masked as
// '*' when masked is true). Backspace edits; Enter submits; Esc/Ctrl-C cancels
// (ok=false). It suits the raw repl, where stdin is owned by the key reader.
func keysReadLine(keys <-chan keyEvent, out io.Writer, label string, masked bool) (string, bool) {
	fmt.Fprintf(out, "\n%s %s%s", accent("◆"), bold(label), muted("："))
	var buf []rune
	for ev := range keys {
		switch ev.kind {
		case keyEnter:
			fmt.Fprint(out, "\n")
			return string(buf), true
		case keyEsc, keyCtrlC:
			fmt.Fprint(out, "\n")
			return "", false
		case keyBackspace:
			if n := len(buf); n > 0 {
				buf = buf[:n-1]
				eraseCells(out, 1)
			}
		case keyRune:
			buf = append(buf, ev.r)
			if masked {
				io.WriteString(out, "*")
			} else {
				io.WriteString(out, string(ev.r))
			}
		}
	}
	return "", false
}
