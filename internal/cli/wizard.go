// Guided model configuration shared by first-time local onboarding and the
// deploy wizard. It gives both flows one consistent, claude-style look: an
// arrow-key provider/model picker, base URLs pre-filled per provider, and an
// API-key prompt that echoes the paste masked (only head and tail shown). The
// interactive widgets enter raw mode themselves and fall back to numbered /
// plain prompts when stdin/stdout is not a terminal, so automation and
// SSH-without-tty still work.
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// errWizardCanceled is returned when the operator aborts a picker (Esc/Ctrl-C).
var errWizardCanceled = errors.New("已取消")

// providerInfo is one entry in the provider catalog: a human label, the
// underlying adapter the runtime speaks (openai-compatible / deepseek /
// anthropic), the default base URL to store, a default model, and the
// selectable model list. Third-party OpenAI-compatible providers (Kimi, Qwen,
// …) all use the "openai" adapter with their own base URL — the runtime needs
// no per-brand knowledge, only the adapter + base URL + model.
type providerInfo struct {
	ID           string
	Label        string
	Adapter      string // stored as config Provider: openai | deepseek | anthropic
	BaseURL      string // stored as config BaseURL; "" lets the adapter use its default
	DefaultModel string
	Models       []string // empty means "ask for a model name" (e.g. custom endpoint)
	Aliases      []string
}

// providerCatalog lists mainstream providers so a newcomer can pick a brand by
// name instead of knowing endpoints. Base URLs and model lists were current as
// of 2026-06; model lists go stale, so every picker also offers a "自定义"
// entry to type any model the provider accepts. The first three keep their
// historical order (1/2/3) for muscle memory.
var providerCatalog = []providerInfo{
	{
		ID: "deepseek", Label: "DeepSeek", Adapter: "deepseek",
		BaseURL: "https://api.deepseek.com", DefaultModel: "deepseek-chat",
		Models: []string{"deepseek-chat", "deepseek-reasoner"},
	},
	{
		ID: "openai", Label: "OpenAI", Adapter: "openai",
		BaseURL: "https://api.openai.com/v1", DefaultModel: "gpt-4o",
		Models:  []string{"gpt-5.5", "gpt-5", "gpt-4.1", "gpt-4o", "gpt-4o-mini", "o3-mini"},
		Aliases: []string{"openai-compatible"},
	},
	{
		ID: "anthropic", Label: "Anthropic", Adapter: "anthropic",
		BaseURL: "https://api.anthropic.com", DefaultModel: "claude-sonnet-4-6",
		Models:  []string{"claude-opus-4-8", "claude-sonnet-4-6", "claude-haiku-4-5-20251001"},
		Aliases: []string{"claude"},
	},
	{
		ID: "moonshot", Label: "Moonshot", Adapter: "openai",
		BaseURL: "https://api.moonshot.cn/v1", DefaultModel: "kimi-k2",
		Models:  []string{"kimi-k2.6", "kimi-k2-thinking", "kimi-k2.5", "kimi-k2", "moonshot-v1-128k", "moonshot-v1-32k", "moonshot-v1-8k"},
		Aliases: []string{"kimi"},
	},
	{
		ID: "qwen", Label: "Qwen", Adapter: "openai",
		BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", DefaultModel: "qwen-plus",
		Models:  []string{"qwen-max", "qwen-plus", "qwen-flash", "qwen3-max", "qwen-long"},
		Aliases: []string{"tongyi", "dashscope", "bailian"},
	},
	{
		ID: "zhipu", Label: "z.ai", Adapter: "openai",
		BaseURL: "https://open.bigmodel.cn/api/paas/v4", DefaultModel: "glm-4.6",
		Models:  []string{"glm-4.6", "glm-4.5", "glm-4-plus", "glm-4-air", "glm-4-flash"},
		Aliases: []string{"glm", "bigmodel"},
	},
	{
		ID: "doubao", Label: "Doubao", Adapter: "openai",
		BaseURL: "https://ark.cn-beijing.volces.com/api/v3", DefaultModel: "doubao-seed-1-6-250615",
		Models:  []string{"doubao-seed-1-6-250615", "doubao-1-5-pro-32k-250115", "doubao-1-5-lite-32k-250115"},
		Aliases: []string{"volcengine", "ark"},
	},
	{
		ID: "gemini", Label: "Gemini", Adapter: "openai",
		BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", DefaultModel: "gemini-2.5-flash",
		Models:  []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.0-flash", "gemini-3-flash-preview"},
		Aliases: []string{"google"},
	},
	{
		ID: "xai", Label: "xAI", Adapter: "openai",
		BaseURL: "https://api.x.ai/v1", DefaultModel: "grok-4.3",
		Models:  []string{"grok-4.3", "grok-4", "grok-3"},
		Aliases: []string{"grok"},
	},
	{
		ID: "groq", Label: "Groq", Adapter: "openai",
		BaseURL: "https://api.groq.com/openai/v1", DefaultModel: "llama-3.3-70b-versatile",
		Models: []string{"llama-3.3-70b-versatile", "llama-3.1-8b-instant", "deepseek-r1-distill-llama-70b"},
	},
	{
		ID: "mistral", Label: "Mistral", Adapter: "openai",
		BaseURL: "https://api.mistral.ai/v1", DefaultModel: "mistral-large-latest",
		Models: []string{"mistral-large-latest", "mistral-small-latest", "codestral-latest"},
	},
	{
		ID: "openrouter", Label: "OpenRouter", Adapter: "openai",
		BaseURL: "https://openrouter.ai/api/v1", DefaultModel: "anthropic/claude-sonnet-4-6",
		Models: []string{"anthropic/claude-sonnet-4-6", "openai/gpt-5", "deepseek/deepseek-chat", "google/gemini-2.5-pro"},
	},
	{
		ID: "siliconflow", Label: "SiliconFlow", Adapter: "openai",
		BaseURL: "https://api.siliconflow.cn/v1", DefaultModel: "deepseek-ai/DeepSeek-V3",
		Models: []string{"deepseek-ai/DeepSeek-V3", "deepseek-ai/DeepSeek-R1", "Qwen/Qwen2.5-72B-Instruct"},
	},
	{
		ID: "custom", Label: "Custom (OpenAI-compatible)", Adapter: "openai",
		BaseURL: "", DefaultModel: "",
		Models:  nil, // nil model list => ask for a model name
		Aliases: []string{"compatible", "other"},
	},
}

// lookupProvider resolves a 1-based menu number, an ID, or a known alias to a
// catalog entry. Used by the numbered fallback and defaultModel.
func lookupProvider(choice string) (providerInfo, bool) {
	s := strings.ToLower(strings.TrimSpace(choice))
	if s == "" {
		return providerInfo{}, false
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n >= 1 && n <= len(providerCatalog) {
			return providerCatalog[n-1], true
		}
		return providerInfo{}, false
	}
	for _, e := range providerCatalog {
		if s == e.ID || slices.Contains(e.Aliases, s) {
			return e, true
		}
	}
	return providerInfo{}, false
}

// defaultModel returns the suggested model for a provider, from the catalog.
// An unknown provider yields "".
func defaultModel(provider string) string {
	if e, ok := lookupProvider(provider); ok {
		return e.DefaultModel
	}
	return ""
}

// collectModel runs the shared provider→model→base-URL steps and returns the
// chosen entry (for its Label/Adapter), the model, and the base URL to store.
// The API key is collected separately so the deploy wizard can ask for it only
// after its SSH/sudo preflight passes.
func collectModel(r *bufio.Reader) (providerInfo, string, string, error) {
	provInitial := 0
	for {
		pIdx, res := selectMenu(r, "选择模型提供商", providerMenuOptions(), provInitial)
		if res != menuPicked { // back or cancel on the first step both leave the wizard
			return providerInfo{}, "", "", errWizardCanceled
		}
		provInitial = pIdx // remember it if the next step steps back here
		entry := providerCatalog[pIdx]

		model, mres := selectModel(r, entry)
		switch mres {
		case menuBack:
			continue // Esc on the model step → re-pick the provider
		case menuCancel:
			return providerInfo{}, "", "", errWizardCanceled
		}

		baseURL := entry.BaseURL
		if entry.ID == "custom" {
			var err error
			baseURL, err = prompt(r, "base URL（OpenAI 兼容地址）", "")
			if err != nil {
				return providerInfo{}, "", "", err
			}
			if baseURL == "" {
				return providerInfo{}, "", "", fmt.Errorf("自定义 provider 必须填 base URL，已取消")
			}
		}
		return entry, model, baseURL, nil
	}
}

// providerMenuOptions builds the provider catalog as menu rows, each labeled
// with its default base URL (or a hint for the custom entry).
func providerMenuOptions() []menuOption {
	opts := make([]menuOption, len(providerCatalog))
	for i, e := range providerCatalog {
		desc := e.BaseURL
		if desc == "" {
			desc = "本地模型 / 未收录的厂商，自己填 base URL"
		}
		opts[i] = menuOption{label: e.Label, desc: desc}
	}
	return opts
}

// selectModel lets the operator pick from the provider's known models (plus a
// "自定义…" escape hatch to type any model name). A provider with no model list
// (custom endpoint) asks for the name directly.
func selectModel(r *bufio.Reader, entry providerInfo) (string, menuResult) {
	if len(entry.Models) == 0 {
		m, err := promptModelName(r) // custom endpoint: no preset list, type the name
		if err != nil {
			return "", menuCancel
		}
		return m, menuPicked
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
	idx, res := selectMenu(r, "选择模型", opts, initial)
	if res != menuPicked {
		return "", res
	}
	if idx == len(entry.Models) { // the "Custom" row
		m, err := promptModelName(r)
		if err != nil {
			return "", menuCancel
		}
		return m, menuPicked
	}
	return entry.Models[idx], menuPicked
}

// promptModelName reads a manually typed model name (for Custom). It carries no
// default — the point of Custom is a model not in the list (a local model, or a
// provider/version we don't catalog) — and re-asks until something is entered.
func promptModelName(r *bufio.Reader) (string, error) {
	for {
		m, err := prompt(r, "模型名（手动输入）", "")
		if err != nil {
			return "", err
		}
		if m != "" {
			return m, nil
		}
		fmt.Println("  模型名不能为空。")
	}
}

// menuOption is one selectable row: a label and an optional dim description.
type menuOption struct {
	label string
	desc  string
}

// menuResult is how a selectMenu interaction ended.
type menuResult int

const (
	menuPicked menuResult = iota // Enter on a row
	menuBack                     // Esc / ← : step back to the previous wizard step
	menuCancel                   // Ctrl-C / read error / non-TTY EOF : leave the wizard
	menuDelete                   // 'd' on a row (the /model panel only)
)

// selectMenu renders an arrow-key list and returns the chosen index. It mirrors
// the in-session confirm menu's look (❯ marker, bold selection, dim
// descriptions). A list taller than the viewport scrolls in place with ▲/▼
// "更多" indicators. Enter picks (menuPicked); Esc or ← steps back (menuBack);
// Ctrl-C or a read error cancels (menuCancel). Off a terminal (or if raw mode
// can't be entered) it falls back to a numbered prompt read from r.
func selectMenu(r *bufio.Reader, title string, opts []menuOption, initial int) (int, menuResult) {
	fd := int(os.Stdin.Fd())
	if !(term.IsTerminal(fd) && term.IsTerminal(int(os.Stdout.Fd()))) {
		return numberedSelect(r, title, opts, initial)
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return numberedSelect(r, title, opts, initial)
	}
	defer fmt.Print("\n")       // runs after Restore: clean line in cooked mode
	defer term.Restore(fd, old) //nolint:errcheck // best-effort restore

	fmt.Printf("\r\n%s %s  %s\r\n", accent("◆"), bold(title),
		muted("↑/↓ 选择 · Enter 确认 · Esc 返回 · ^C 退出"))

	// Viewport: show vh rows at a time, capped to the terminal height so a long
	// list scrolls instead of overflowing. A scrolling list adds ▲/▼ indicator
	// rows, keeping the redrawn block a constant height (blockH).
	vh, scroll := len(opts), false
	maxVisible := 10
	if _, rows, gerr := term.GetSize(int(os.Stdout.Fd())); gerr == nil {
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
				fmt.Printf("\r\x1b[K  %s\r\n", muted("▲ 更多"))
			} else {
				fmt.Print("\r\x1b[K\r\n")
			}
		}
		for i := top; i < top+vh; i++ {
			o := opts[i]
			marker, label := "  ", o.label
			if i == sel {
				marker, label = accent("❯")+" ", bold(o.label)
			}
			if o.desc != "" {
				fmt.Printf("\r\x1b[K%s%s  %s\r\n", marker, label, muted(o.desc))
			} else {
				fmt.Printf("\r\x1b[K%s%s\r\n", marker, label)
			}
		}
		if scroll {
			if rem := len(opts) - (top + vh); rem > 0 {
				fmt.Printf("\r\x1b[K  %s\r\n", muted(fmt.Sprintf("▼ 更多 (%d)", rem)))
			} else {
				fmt.Print("\r\x1b[K\r\n")
			}
		}
	}
	render()
	redraw := func() {
		clampView()
		fmt.Printf("\x1b[%dA", blockH) // back to the top of the block
		render()
	}

	// In raw mode an arrow/page key arrives as an ESC [ … sequence in one read.
	buf := make([]byte, 8)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			return 0, menuCancel
		}
		switch {
		case buf[0] == 3: // Ctrl-C → leave the wizard
			return 0, menuCancel
		case buf[0] == 27 && n == 1: // lone Esc → step back
			return 0, menuBack
		case buf[0] == '\r', buf[0] == '\n':
			return sel, menuPicked
		case n >= 3 && buf[0] == 27 && buf[1] == '[':
			switch {
			case buf[2] == 'A': // up
				if sel > 0 {
					sel--
				}
			case buf[2] == 'B': // down
				if sel < len(opts)-1 {
					sel++
				}
			case buf[2] == 'D': // ← : step back
				return 0, menuBack
			case n >= 4 && buf[2] == '5' && buf[3] == '~': // PageUp
				if sel -= vh; sel < 0 {
					sel = 0
				}
			case n >= 4 && buf[2] == '6' && buf[3] == '~': // PageDown
				if sel += vh; sel > len(opts)-1 {
					sel = len(opts) - 1
				}
			}
		case buf[0] == 'k':
			if sel > 0 {
				sel--
			}
		case buf[0] == 'j':
			if sel < len(opts)-1 {
				sel++
			}
		case buf[0] >= '1' && buf[0] <= '9': // jump to a numbered row (move, don't confirm)
			if i := int(buf[0] - '1'); i < len(opts) {
				sel = i
			}
		}
		redraw()
	}
}

// numberedSelect is the non-terminal fallback for selectMenu: it prints a
// numbered list and reads a choice from r. Empty input takes the default
// (initial); the loop re-asks on an out-of-range entry. There is no back
// navigation off a terminal, so it only returns menuPicked or menuCancel.
func numberedSelect(r *bufio.Reader, title string, opts []menuOption, initial int) (int, menuResult) {
	fmt.Printf("\n%s\n", title)
	for i, o := range opts {
		line := fmt.Sprintf("  [%d] %s", i+1, o.label)
		if o.desc != "" {
			line += "  " + o.desc
		}
		fmt.Println(line)
	}
	for {
		choice, err := prompt(r, fmt.Sprintf("选择 (1-%d)", len(opts)), strconv.Itoa(initial+1))
		if err != nil {
			return 0, menuCancel
		}
		if n, err := strconv.Atoi(strings.TrimSpace(choice)); err == nil && n >= 1 && n <= len(opts) {
			return n - 1, menuPicked
		}
		fmt.Println("  无效选择。")
	}
}

// promptAPIKey reads the model API key, echoing it masked (head and tail shown,
// middle as asterisks) so a paste is visibly confirmed without exposing the
// secret. Off a terminal it reads a plain line from r.
func promptAPIKey(r *bufio.Reader) (string, error) {
	return readMaskedSecret(r, "粘贴 API key")
}

// readMaskedSecret reads a secret in raw mode, redrawing it masked on each
// keystroke (so a paste shows up as head****tail). Backspace edits; Enter
// submits; Esc/Ctrl-C cancels. When stdin is not a terminal (or raw mode is
// unavailable) it falls back to a plain line read, which suits piped input.
func readMaskedSecret(r *bufio.Reader, label string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return prompt(r, label, "")
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return prompt(r, label, "")
	}
	defer term.Restore(fd, old) //nolint:errcheck // best-effort restore

	prefix := fmt.Sprintf("%s %s  %s", accent("◆"), bold(label), muted("（粘贴即可，已隐藏中间）"))
	redraw := func(sb []rune) {
		fmt.Printf("\r\x1b[K%s  %s", prefix, maskSecret(string(sb)))
	}
	redraw(nil)

	var sb []rune
	buf := make([]byte, 1024)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			break // EOF: submit what we have
		}
		if n == 1 && (buf[0] == 3 || buf[0] == 27) { // Ctrl-C or lone Esc → cancel
			fmt.Print("\r\n")
			return "", errWizardCanceled
		}
		done := false
		for _, ru := range string(buf[:n]) {
			switch {
			case ru == '\r' || ru == '\n':
				done = true
			case ru == 3: // Ctrl-C inside a chunk
				fmt.Print("\r\n")
				return "", errWizardCanceled
			case ru == 127 || ru == 8: // Backspace
				if l := len(sb); l > 0 {
					sb = sb[:l-1]
				}
			case ru < 32: // ignore other control bytes (incl. stray Esc)
			default:
				sb = append(sb, ru)
			}
			if done {
				break
			}
		}
		redraw(sb)
		if done {
			break
		}
	}
	fmt.Print("\r\n")
	return string(sb), nil
}

// maskSecret renders s with only its head and tail visible and the middle as
// asterisks (one per hidden rune, so the visible length still reflects a paste).
// Short values are fully masked since there is nothing safe to reveal.
func maskSecret(s string) string {
	r := []rune(s)
	n := len(r)
	if n == 0 {
		return ""
	}
	var head, tail int
	switch {
	case n <= 4:
		head, tail = 0, 0
	case n <= 10:
		head, tail = 2, 2
	default:
		head, tail = 4, 4
	}
	if head+tail >= n {
		head, tail = 0, 0
	}
	return string(r[:head]) + strings.Repeat("*", n-head-tail) + string(r[n-tail:])
}

// wizardTitle prints a styled heading for an onboarding flow, matching the
// accent/bold look used elsewhere in the client.
func wizardTitle(title, subtitle string) {
	fmt.Println()
	fmt.Printf(" %s %s\n", accent("✻"), bold(title))
	if subtitle != "" {
		fmt.Printf(" %s\n", muted(subtitle))
	}
}
