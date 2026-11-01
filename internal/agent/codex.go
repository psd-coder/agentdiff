package agent

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

type codexLine struct {
	Type     string     `json:"type"`
	ThreadID string     `json:"thread_id"`
	Message  string     `json:"message"`
	Error    *codexErr  `json:"error"`
	Item     *codexItem `json:"item"`
}

type codexErr struct {
	Message string `json:"message"`
}

type codexItem struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Command json.RawMessage `json:"command"`
}

type codexParser struct {
	sessionID string
	texts     []string
	started   map[string]bool
	lastErr   string
}

func (p *codexParser) streamError() error {
	if p.lastErr == "" {
		return nil
	}
	return errors.New(p.lastErr)
}

func (p *codexParser) parse(raw []byte) []Event {
	var line codexLine
	if json.Unmarshal(raw, &line) != nil {
		return nil
	}
	switch line.Type {
	case "thread.started":
		if p.sessionID == "" && line.ThreadID != "" {
			p.sessionID = line.ThreadID
			return []Event{{Kind: EventSession, SessionID: p.sessionID}}
		}
	case "item.started":
		return p.itemStarted(line.Item)
	case "item.completed":
		return p.itemCompleted(line.Item)
	// codex also reports retry notices as error events, so only turn.failed ends the run.
	case "error":
		if line.Message != "" {
			p.lastErr = line.Message
			return []Event{{Kind: EventActivity, Text: line.Message}}
		}
	case "turn.failed":
		msg := p.lastErr
		if line.Error != nil && line.Error.Message != "" {
			msg = line.Error.Message
		}
		return []Event{{Kind: EventError, SessionID: p.sessionID, Err: errors.New(firstNonEmpty(msg, "codex turn failed"))}}
	case "turn.completed":
		return []Event{{Kind: EventDone, Text: strings.Join(p.texts, "\n\n"), SessionID: p.sessionID}}
	}
	return nil
}

func (p *codexParser) itemStarted(item *codexItem) []Event {
	if item == nil || item.Type != "command_execution" {
		return nil
	}
	if p.started == nil {
		p.started = map[string]bool{}
	}
	p.started[item.ID] = true
	return []Event{commandActivity(item.Command)}
}

func (p *codexParser) itemCompleted(item *codexItem) []Event {
	if item == nil {
		return nil
	}
	switch item.Type {
	case "agent_message":
		if item.Text == "" {
			return nil
		}
		text := item.Text
		if len(p.texts) > 0 {
			text = "\n\n" + text
		}
		p.texts = append(p.texts, item.Text)
		return []Event{{Kind: EventText, Text: text}}
	case "command_execution":
		if !p.started[item.ID] {
			return []Event{commandActivity(item.Command)}
		}
	}
	return nil
}

func commandActivity(raw json.RawMessage) Event {
	return Event{Kind: EventActivity, Text: "Running: " + oneLine(commandText(raw))}
}

func commandText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return unwrapShell(s)
	}
	var argv []string
	if json.Unmarshal(raw, &argv) != nil {
		return ""
	}
	if len(argv) == 3 && shellName.MatchString(argv[0]) && loginFlag.MatchString(argv[1]) {
		return argv[2]
	}
	return strings.Join(argv, " ")
}

var (
	shellName    = regexp.MustCompile(`^(?:\S*/)?(?:ba|z)?sh$`)
	loginFlag    = regexp.MustCompile(`^-l?c$`)
	shellWrapper = regexp.MustCompile(`^(?:\S*/)?(?:ba|z)?sh\s+-l?c\s+(.+)$`)
)

func unwrapShell(command string) string {
	m := shellWrapper.FindStringSubmatch(strings.TrimSpace(command))
	if m == nil {
		return command
	}
	arg := m[1]
	switch {
	case len(arg) >= 2 && arg[0] == '\'' && arg[len(arg)-1] == '\'':
		return strings.ReplaceAll(arg[1:len(arg)-1], `'\''`, `'`)
	case len(arg) >= 2 && arg[0] == '"' && arg[len(arg)-1] == '"':
		return unescapeDouble(arg[1 : len(arg)-1])
	}
	return arg
}

func unescapeDouble(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\"\\$`", s[i+1]) >= 0 {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// codexArgs keeps Codex in its read-only sandbox. Without the user's
// setup, a run skips config.toml, hooks, plugins, apps and the AGENTS.md
// files of the repository. The CLI has no option to skip
// ~/.codex/AGENTS.md or the skills.
func codexArgs(cfg Config, req Request) []string {
	args := []string{"exec", "--json", "-s", "read-only", "--skip-git-repo-check"}
	if !cfg.UserSetup {
		args = append(args, "--ignore-user-config", "--disable", "hooks", "--disable", "plugins", "--disable", "apps", "-c", "project_doc_max_bytes=0")
	}
	if req.Dir != "" {
		args = append(args, "-C", req.Dir)
	}
	if cfg.Model != "" {
		args = append(args, "-m", cfg.Model)
	}
	if cfg.Effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+strconv.Quote(cfg.Effort))
	}
	args = append(args, cfg.ExtraArgs...)
	switch {
	case req.ResumeID != "" && req.Fork:
		args = append(args, "fork", req.ResumeID)
	case req.ResumeID != "":
		args = append(args, "resume", req.ResumeID)
	}
	return append(args, "-")
}

// codexPrompt puts the system prompt before the prompt, because the CLI
// takes no system prompt of its own.
func codexPrompt(req Request) string {
	if req.SystemPrompt == "" {
		return req.Prompt
	}
	return "<instructions>\n" + req.SystemPrompt + "\n</instructions>\n\n" + req.Prompt
}

// codexResumed takes the session after resume or fork near the end, before
// "-".
func codexResumed(words []string) string {
	if n := len(words); n >= 3 && words[n-1] == "-" && (words[n-3] == "resume" || words[n-3] == "fork") {
		return words[n-2]
	}
	return ""
}
