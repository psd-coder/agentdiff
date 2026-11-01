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

func (parser *codexParser) streamError() error {
	if parser.lastErr == "" {
		return nil
	}
	return errors.New(parser.lastErr)
}

func (parser *codexParser) parse(raw []byte) []Event {
	var line codexLine
	if json.Unmarshal(raw, &line) != nil {
		return nil
	}
	switch line.Type {
	case "thread.started":
		if parser.sessionID == "" && line.ThreadID != "" {
			parser.sessionID = line.ThreadID
			return []Event{{Kind: EventSession, SessionID: parser.sessionID}}
		}
	case "item.started":
		return parser.itemStarted(line.Item)
	case "item.completed":
		return parser.itemCompleted(line.Item)
	// codex also reports retry notices as error events, so only turn.failed ends the run.
	case "error":
		if line.Message != "" {
			parser.lastErr = line.Message
			return []Event{{Kind: EventActivity, Text: line.Message}}
		}
	case "turn.failed":
		msg := parser.lastErr
		if line.Error != nil && line.Error.Message != "" {
			msg = line.Error.Message
		}
		return []Event{{Kind: EventError, SessionID: parser.sessionID, Err: errors.New(firstNonEmpty(msg, "codex turn failed"))}}
	case "turn.completed":
		return []Event{{Kind: EventDone, Text: strings.Join(parser.texts, "\n\n"), SessionID: parser.sessionID}}
	}
	return nil
}

func (parser *codexParser) itemStarted(item *codexItem) []Event {
	if item == nil || item.Type != "command_execution" {
		return nil
	}
	if parser.started == nil {
		parser.started = map[string]bool{}
	}
	parser.started[item.ID] = true
	return []Event{commandActivity(item.Command)}
}

func (parser *codexParser) itemCompleted(item *codexItem) []Event {
	if item == nil {
		return nil
	}
	switch item.Type {
	case "agent_message":
		if item.Text == "" {
			return nil
		}
		text := item.Text
		if len(parser.texts) > 0 {
			text = "\n\n" + text
		}
		parser.texts = append(parser.texts, item.Text)
		return []Event{{Kind: EventText, Text: text}}
	case "command_execution":
		if !parser.started[item.ID] {
			return []Event{commandActivity(item.Command)}
		}
	}
	return nil
}

func commandActivity(raw json.RawMessage) Event {
	return Event{Kind: EventActivity, Text: "Running: " + oneLine(commandText(raw))}
}

func commandText(raw json.RawMessage) string {
	var command string
	if json.Unmarshal(raw, &command) == nil {
		return unwrapShell(command)
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
	match := shellWrapper.FindStringSubmatch(strings.TrimSpace(command))
	if match == nil {
		return command
	}
	arg := match[1]
	switch {
	case len(arg) >= 2 && arg[0] == '\'' && arg[len(arg)-1] == '\'':
		return strings.ReplaceAll(arg[1:len(arg)-1], `'\''`, `'`)
	case len(arg) >= 2 && arg[0] == '"' && arg[len(arg)-1] == '"':
		return unescapeDouble(arg[1 : len(arg)-1])
	}
	return arg
}

func unescapeDouble(text string) string {
	var unescaped strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' && i+1 < len(text) && strings.IndexByte("\"\\$`", text[i+1]) >= 0 {
			i++
		}
		unescaped.WriteByte(text[i])
	}
	return unescaped.String()
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
	if count := len(words); count >= 3 && words[count-1] == "-" && (words[count-3] == "resume" || words[count-3] == "fork") {
		return words[count-2]
	}
	return ""
}
