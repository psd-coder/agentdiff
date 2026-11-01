package agent

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
)

type claudeLine struct {
	Type            string         `json:"type"`
	Subtype         string         `json:"subtype"`
	SessionID       string         `json:"session_id"`
	ParentToolUseID *string        `json:"parent_tool_use_id"`
	APIMessageID    string         `json:"api_message_id"`
	Event           *claudeEvent   `json:"event"`
	Message         *claudeMessage `json:"message"`
	Result          string         `json:"result"`
	IsError         bool           `json:"is_error"`
}

type claudeEvent struct {
	Type         string         `json:"type"`
	Message      *claudeMessage `json:"message"`
	ContentBlock claudeBlock    `json:"content_block"`
	Delta        claudeBlock    `json:"delta"`
}

type claudeMessage struct {
	ID      string        `json:"id"`
	Content []claudeBlock `json:"content"`
}

type claudeBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

type claudeParser struct {
	dir       string
	sessionID string
	messageID string
	streamed  map[string]bool
	wroteText bool
	needSep   bool
}

func (p *claudeParser) streamError() error { return nil }

func (p *claudeParser) parse(raw []byte) []Event {
	var line claudeLine
	if json.Unmarshal(raw, &line) != nil {
		return nil
	}
	var events []Event
	if p.sessionID == "" && line.SessionID != "" {
		p.sessionID = line.SessionID
		events = append(events, Event{Kind: EventSession, SessionID: p.sessionID})
	}
	switch line.Type {
	case "stream_event":
		events = append(events, p.streamEvent(line)...)
	case "assistant":
		events = append(events, p.assistant(line)...)
	case "result":
		events = append(events, p.result(line))
	}
	return events
}

func (p *claudeParser) streamEvent(line claudeLine) []Event {
	if line.Event == nil || line.ParentToolUseID != nil {
		return nil
	}
	ev := line.Event
	switch ev.Type {
	case "message_start":
		if ev.Message != nil {
			p.messageID = ev.Message.ID
		}
	case "content_block_start":
		if ev.ContentBlock.Type == "text" {
			p.needSep = p.wroteText
		}
	case "content_block_delta":
		if ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
			p.markStreamed(firstNonEmpty(line.APIMessageID, p.messageID))
			return []Event{p.text(ev.Delta.Text)}
		}
	}
	return nil
}

// Text deltas already streamed this message, so its full copy is used only for tool calls.
// Without --include-partial-messages there are no deltas and the full text is the only source.
func (p *claudeParser) assistant(line claudeLine) []Event {
	if line.Message == nil {
		return nil
	}
	skipText := line.ParentToolUseID != nil || p.streamed[line.Message.ID]
	var events []Event
	for _, block := range line.Message.Content {
		switch {
		case block.Type == "tool_use":
			events = append(events, Event{Kind: EventActivity, Text: toolActivity(block, p.dir)})
		case block.Type == "text" && block.Text != "" && !skipText:
			p.needSep = p.wroteText
			events = append(events, p.text(block.Text))
		}
	}
	return events
}

func (p *claudeParser) result(line claudeLine) Event {
	if line.IsError || strings.HasPrefix(line.Subtype, "error") {
		return Event{Kind: EventError, SessionID: p.sessionID, Err: errors.New(firstNonEmpty(line.Result, line.Subtype, "claude reported an error"))}
	}
	return Event{Kind: EventDone, Text: line.Result, SessionID: p.sessionID}
}

func (p *claudeParser) text(s string) Event {
	if p.needSep {
		s = "\n\n" + s
		p.needSep = false
	}
	p.wroteText = true
	return Event{Kind: EventText, Text: s}
}

func (p *claudeParser) markStreamed(id string) {
	if p.streamed == nil {
		p.streamed = map[string]bool{}
	}
	p.streamed[id] = true
}

func toolActivity(block claudeBlock, dir string) string {
	str := func(key string) string {
		s, _ := block.Input[key].(string)
		return oneLine(s)
	}
	var detail string
	switch block.Name {
	case "Bash":
		detail = str("command")
	case "Read", "Edit", "Write", "NotebookEdit":
		detail = relPath(dir, str("file_path"))
	case "Grep", "Glob":
		detail = str("pattern")
		if path := str("path"); path != "" {
			detail += " in " + relPath(dir, path)
		}
	}
	if detail == "" {
		return block.Name
	}
	return block.Name + ": " + detail
}

func relPath(dir, path string) string {
	if dir == "" || !filepath.IsAbs(path) {
		return path
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return path
	}
	return rel
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

var readOnlyCommands = []string{
	"git diff", "git show", "git log", "git status", "git blame", "git ls-files", "git rev-parse",
	"grep", "rg", "ls", "cat", "head", "tail", "wc",
}

// readOnlyRules allows each command also with an rtk prefix: rtk's hook
// rewrites commands before Claude checks its rules.
func readOnlyRules() []string {
	rules := make([]string, 0, 2*len(readOnlyCommands))
	for _, c := range readOnlyCommands {
		rules = append(rules, "Bash("+c+":*)", "Bash(rtk "+c+":*)")
	}
	return rules
}

// claudeArgs keeps Claude read-only: in dontAsk mode a tool call that no
// allow rule matches is refused instead of asked about, so the allow list
// below, plus the user's own rules, is all that Bash may run. Safe mode
// leaves out the user's CLAUDE.md files, skills, plugins, hooks and MCP
// servers, and keeps the login and the permission rules.
func claudeArgs(cfg Config, req Request) []string {
	args := []string{
		"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages",
		"--tools", "Read,Grep,Glob,Bash",
		"--permission-mode", "dontAsk",
		"--allowedTools",
	}
	args = append(args, readOnlyRules()...)
	if !cfg.UserSetup {
		args = append(args, "--safe-mode")
	}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	if cfg.Effort != "" {
		args = append(args, "--effort", cfg.Effort)
	}
	if req.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", req.SystemPrompt)
	}
	if req.ResumeID != "" {
		args = append(args, "--resume", req.ResumeID)
		if req.Fork {
			args = append(args, "--fork-session")
		}
	}
	return append(args, cfg.ExtraArgs...)
}

func claudePrompt(req Request) string { return req.Prompt }

// claudeResumed takes the session after --resume.
func claudeResumed(words []string) string {
	if i := slices.Index(words, "--resume"); i >= 0 && i+1 < len(words) {
		return words[i+1]
	}
	return ""
}
