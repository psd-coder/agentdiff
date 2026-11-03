package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const opencodeAgent = "agentdiff"

// opencodeArgs only uses options that OpenCode has kept across its
// releases. The read-only rules and the system prompt go in the config of
// the run, which opencodeEnv passes.
func opencodeArgs(cfg Config, req Request) []string {
	args := []string{"run", "--format", "json", "--agent", opencodeAgent, "--title", "AgentDiff"}
	if cfg.Model != "" {
		args = append(args, "-m", cfg.Model)
	}
	if cfg.Effort != "" {
		args = append(args, "--variant", cfg.Effort)
	}
	if req.ResumeID != "" {
		args = append(args, "--session", req.ResumeID)
		if req.Fork {
			args = append(args, "--fork")
		}
	}
	return append(args, cfg.ExtraArgs...)
}

func opencodePrompt(req Request) string { return req.Prompt }

// opencodeEnv gives OpenCode the config of the run. Without the user's
// setup, OpenCode reads its global config from an empty directory and
// skips the config and the AGENTS.md files of the repository, the Claude
// Code files and the skills of other agents. Its logins stay, because
// OpenCode keeps them in its data directory.
func opencodeEnv(cfg Config, req Request) []string {
	env := []string{"OPENCODE_CONFIG_CONTENT=" + opencodeConfig(req.SystemPrompt)}
	if !cfg.UserSetup {
		env = append(env,
			"OPENCODE_CONFIG_DIR="+filepath.Join(os.TempDir(), "agentdiff-opencode"),
			"OPENCODE_DISABLE_PROJECT_CONFIG=1",
			"OPENCODE_DISABLE_CLAUDE_CODE=1",
			"OPENCODE_DISABLE_EXTERNAL_SKILLS=1",
		)
	}
	return env
}

// opencodeConfig defines the agent of the runs. Its permissions refuse
// every tool except reading and the read-only commands. A redirect or
// git's --output would write a file, so commands with them are refused.
// The config also keeps OpenCode from sharing the session and from taking
// snapshots of the repository.
func opencodeConfig(systemPrompt string) string {
	bash := orderedRules{{"*", "deny"}}
	for _, command := range readOnlyCommands {
		bash = append(bash, rule{command, "allow"}, rule{command + " *", "allow"})
	}
	bash = append(bash, rule{"*>*", "deny"}, rule{"*--output*", "deny"})
	agent := map[string]any{
		"mode": "primary",
		"permission": orderedRules{
			{"*", "deny"}, {"read", "allow"}, {"glob", "allow"}, {"grep", "allow"}, {"list", "allow"},
			{"external_directory", "deny"}, {"bash", bash},
		},
	}
	if systemPrompt != "" {
		agent["prompt"] = systemPrompt
	}
	config := map[string]any{
		"share":      "disabled",
		"autoupdate": false,
		"snapshot":   false,
		"agent":      map[string]any{opencodeAgent: agent},
	}
	data, err := json.Marshal(config)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// orderedRules are permission rules in their order: the last rule that
// matches a call decides.
type orderedRules []rule

type rule struct {
	pattern string
	action  any
}

func (rules orderedRules) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, rule := range rules {
		if i > 0 {
			out.WriteByte(',')
		}
		key, err := json.Marshal(rule.pattern)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(rule.action)
		if err != nil {
			return nil, err
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// opencodeResumed takes the session after --session.
func opencodeResumed(words []string) string {
	if index := slices.Index(words, "--session"); index >= 0 && index+1 < len(words) {
		return words[index+1]
	}
	return ""
}

type opencodeLine struct {
	Type      string         `json:"type"`
	SessionID string         `json:"sessionID"`
	Part      *opencodePart  `json:"part"`
	Error     *opencodeError `json:"error"`
}

type opencodePart struct {
	Text   string          `json:"text"`
	Tool   string          `json:"tool"`
	Reason string          `json:"reason"`
	Cost   float64         `json:"cost"`
	Tokens *opencodeTokens `json:"tokens"`
	State  *struct {
		Status string         `json:"status"`
		Input  map[string]any `json:"input"`
		Output string         `json:"output"`
		Error  string         `json:"error"`
	} `json:"state"`
}

type opencodeTokens struct {
	Input     int `json:"input"`
	Output    int `json:"output"`
	Reasoning int `json:"reasoning"`
	Cache     struct {
		Read  int `json:"read"`
		Write int `json:"write"`
	} `json:"cache"`
}

type opencodeError struct {
	Name string `json:"name"`
	Data struct {
		Message string `json:"message"`
	} `json:"data"`
}

func (err *opencodeError) text() string {
	if err == nil {
		return "opencode reported an error"
	}
	return firstNonEmpty(err.Data.Message, err.Name, "opencode reported an error")
}

// opencodeDone says whether a step ends the answer: a step that called
// tools goes on with their results.
func opencodeDone(part *opencodePart) bool {
	return part != nil && part.Reason != "" && part.Reason != "tool-calls"
}

// opencodeParser reports errors only when the process exits: OpenCode can
// print a general error first and the cause after it.
type opencodeParser struct {
	dir       string
	sessionID string
	texts     []string
	errs      []string
}

func (parser *opencodeParser) streamError() error {
	if len(parser.errs) == 0 {
		return nil
	}
	return errors.New(strings.Join(parser.errs, "\n"))
}

func (parser *opencodeParser) parse(raw []byte) []Event {
	var line opencodeLine
	if json.Unmarshal(raw, &line) != nil {
		return nil
	}
	var events []Event
	if parser.sessionID == "" && line.SessionID != "" {
		parser.sessionID = line.SessionID
		events = append(events, Event{Kind: EventSession, SessionID: parser.sessionID})
	}
	switch line.Type {
	case "text":
		if line.Part != nil && line.Part.Text != "" {
			text := line.Part.Text
			if len(parser.texts) > 0 {
				text = "\n\n" + text
			}
			parser.texts = append(parser.texts, line.Part.Text)
			events = append(events, Event{Kind: EventText, Text: text})
		}
	case "tool_use":
		if line.Part != nil {
			events = append(events, Event{Kind: EventActivity, Text: opencodeActivity(line.Part, parser.dir)})
		}
	case "step_finish":
		if opencodeDone(line.Part) {
			events = append(events, Event{Kind: EventDone, Text: strings.Join(parser.texts, "\n\n"), SessionID: parser.sessionID})
		}
	case "error":
		parser.errs = append(parser.errs, line.Error.text())
	}
	return events
}

func opencodeActivity(part *opencodePart, dir string) string {
	var input map[string]any
	if part.State != nil {
		input = map[string]any{"command": part.State.Input["command"], "file_path": part.State.Input["filePath"], "pattern": part.State.Input["pattern"], "path": part.State.Input["path"]}
	}
	name := part.Tool
	if name != "" {
		name = strings.ToUpper(name[:1]) + name[1:]
	}
	return toolActivity(claudeBlock{Name: name, Input: input}, dir)
}
