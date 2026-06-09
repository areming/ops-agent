package memory

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Command is one saved custom command: a `/name` the operator can trigger from
// the prompt. The Body is the command's definition — natural-language
// instructions, a shell snippet, or both — which the agent injects as a turn's
// input so the model knows what to do and within what bounds ("operation
// space"). Commands are defined as *.md files (see LoadCommands), mirroring how
// knowledge files are loaded.
type Command struct {
	Name        string // trigger, lowercased; matched against the typed /name
	Description string // one-line summary for /help and /commands
	Body        string // the instructions/script fed to the model on trigger
}

// LoadCommands reads every *.md file in dir and parses each into a Command.
// A missing dir yields no commands and no error, so custom commands stay
// optional. Files are returned sorted by name. A file with no frontmatter
// still loads: its name defaults to the filename and its body is the whole
// file, so a bare script file works as a command too.
func LoadCommands(dir string) ([]Command, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var cmds []Command
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		cmd := parseCommand(string(b))
		if cmd.Name == "" {
			cmd.Name = strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
		}
		if cmd.Body == "" {
			continue // nothing to run; skip
		}
		cmds = append(cmds, cmd)
	}
	return cmds, nil
}

// parseCommand splits an optional `---` frontmatter block (only name and
// description keys are read) from the body. A file without a leading `---`
// fence is treated as all body, so a plain script needs no frontmatter.
func parseCommand(raw string) Command {
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	rest, ok := strings.CutPrefix(s, "---\n")
	if !ok {
		return Command{Body: strings.TrimSpace(s)}
	}
	front, body, ok := strings.Cut(rest, "\n---")
	if !ok {
		// Unterminated fence: treat the whole thing as body rather than eating it.
		return Command{Body: strings.TrimSpace(s)}
	}
	// Drop the rest of the closing fence line (e.g. trailing chars after ---).
	if nl := strings.IndexByte(body, '\n'); nl >= 0 {
		body = body[nl+1:]
	} else {
		body = ""
	}

	var c Command
	for line := range strings.SplitSeq(front, "\n") {
		key, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name":
			c.Name = strings.ToLower(val)
		case "description", "desc":
			c.Description = val
		}
	}
	c.Body = strings.TrimSpace(body)
	return c
}

// FindCommand returns the command whose name matches (case-insensitively), if
// any.
func FindCommand(cmds []Command, name string) (Command, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, c := range cmds {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}
