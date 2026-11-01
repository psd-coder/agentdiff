package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

type LogKind string

const (
	LogCommand LogKind = "command"
	LogStdin   LogKind = "stdin"
	LogStdout  LogKind = "stdout"
	LogStderr  LogKind = "stderr"
	LogExit    LogKind = "exit"
)

type LogEntry struct {
	Time time.Time `json:"time"`
	Kind LogKind   `json:"kind"`
	Text string    `json:"text"`
}

const (
	exitOK      = "Exited with status 0"
	exitStopped = "Stopped"
)

type logger struct {
	mu   sync.Mutex
	sink func(LogEntry)
}

func newLogger(sink func(LogEntry)) *logger { return &logger{sink: sink} }

func (log *logger) add(kind LogKind, text string) {
	if log.sink == nil {
		return
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	log.sink(LogEntry{Time: time.Now(), Kind: kind, Text: text})
}

// lineWriter hands each complete line written to it to emit.
type lineWriter struct {
	mu   sync.Mutex
	buf  []byte
	emit func(string)
}

func (writer *lineWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.buf = append(writer.buf, data...)
	for {
		newline := bytes.IndexByte(writer.buf, '\n')
		if newline < 0 {
			return len(data), nil
		}
		writer.emit(strings.TrimRight(string(writer.buf[:newline]), "\r"))
		writer.buf = writer.buf[newline+1:]
	}
}

func (writer *lineWriter) flush() {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.buf) > 0 {
		writer.emit(string(writer.buf))
		writer.buf = nil
	}
}

// exitText is how the process ended. The CLI can still run its hooks after
// it sent the result, so a cancel after the result is a normal exit.
func exitText(ctx context.Context, err error, delivered bool) string {
	var exit *exec.ExitError
	switch {
	case delivered && ctx.Err() != nil:
		return exitOK
	case ctx.Err() != nil:
		return exitStopped
	case err == nil:
		return exitOK
	case errors.As(err, &exit):
		return fmt.Sprintf("Exited with status %d", exit.ExitCode())
	}
	return "Failed: " + err.Error()
}

var plainArg = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// commandLine is the run as a shell command a person can paste.
func commandLine(dir, bin string, args []string) string {
	parts := []string{shellQuote(bin)}
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	line := strings.Join(parts, " ")
	if dir != "" {
		line = "cd " + shellQuote(dir) + " && " + line
	}
	return line
}

func shellQuote(arg string) string {
	if plainArg.MatchString(arg) {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

// ResumedSession is the session that a command logged by commandLine
// resumed or forked, empty for a new session.
func ResumedSession(provider Provider, command string) string {
	return specOf(provider).resumed(shellWords(command))
}

// shellWords splits a command line that shellQuote wrote back into words.
func shellWords(line string) []string {
	var words []string
	var word strings.Builder
	inWord, quoted := false, false
	for i := 0; i < len(line); i++ {
		char := line[i]
		switch {
		case quoted && char == '\'':
			quoted = false
		case quoted:
			word.WriteByte(char)
		case char == '\'':
			quoted, inWord = true, true
		case char == '\\' && i+1 < len(line):
			i++
			word.WriteByte(line[i])
			inWord = true
		case char == ' ':
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
		default:
			word.WriteByte(char)
			inWord = true
		}
	}
	if inWord {
		words = append(words, word.String())
	}
	return words
}

// LogItem is one readable step of a run. A log entry gives none when it
// only repeats another one, as the streamed parts of a message do.
type LogItem struct {
	Title  string
	Detail string
	Failed bool
	// Usage is set on the item that reports what a run spent.
	Usage *Usage
	// result is how the agent ended its answer, on the item of its
	// result. exit is how the process ended, on the item of its exit.
	result RunStatus
	exit   RunStatus
}

// Usage is the tokens a run spent. Input counts the tokens read without
// the cache. CostUSD is what the CLI reports at API prices, 0 for Codex.
// The other counts add up every request of the run, and each request reads
// the whole conversation again. Context is the size of the last request,
// the number Claude Code shows; 0 when the CLI does not report it.
// Session is the Claude session the run ended in. SessionCost is what
// Claude reports for that session, so it includes the runs this one
// resumed.
type Usage struct {
	Input       int     `json:"input"`
	Output      int     `json:"output"`
	CacheRead   int     `json:"cacheRead"`
	CacheWrite  int     `json:"cacheWrite"`
	CostUSD     float64 `json:"costUsd,omitempty"`
	Context     int     `json:"context,omitempty"`
	Session     string  `json:"session,omitempty"`
	SessionCost float64 `json:"sessionCost,omitempty"`
}

func (usage Usage) Total() int {
	return usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
}

func (usage *Usage) Add(other Usage) {
	usage.Input += other.Input
	usage.Output += other.Output
	usage.CacheRead += other.CacheRead
	usage.CacheWrite += other.CacheWrite
	usage.CostUSD += other.CostUSD
	if other.Context > 0 {
		usage.Context = other.Context
	}
	if other.Session != "" {
		usage.Session, usage.SessionCost = other.Session, other.SessionCost
	}
}

func (usage Usage) String() string {
	text := fmt.Sprintf("%s tokens processed: %s in, %s cache read, %s cache write, %s out",
		Count(usage.Total()), Count(usage.Input), Count(usage.CacheRead), Count(usage.CacheWrite), Count(usage.Output))
	switch {
	case usage.CostUSD > 0:
		text += fmt.Sprintf(", $%.4f at API prices", usage.CostUSD)
	case usage.SessionCost > 0:
		text += fmt.Sprintf(", $%.4f at API prices for the whole session", usage.SessionCost)
	}
	if usage.Context > 0 {
		text = fmt.Sprintf("%s tokens in context, %s", Count(usage.Context), text)
	}
	return text
}

// Short is the context size, or the processed total when the CLI does
// not report the context.
func (usage Usage) Short() string {
	if usage.Context > 0 {
		return Count(usage.Context) + " tokens"
	}
	return Count(usage.Total()) + " tokens processed"
}

// Count writes a token count short: 950, 12.3k, 1.2M.
func Count(tokens int) string {
	switch {
	case tokens >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(tokens)/1e6)
	case tokens >= 10_000:
		return fmt.Sprintf("%dk", tokens/1000)
	case tokens >= 1000:
		return fmt.Sprintf("%.1fk", float64(tokens)/1000)
	}
	return fmt.Sprint(tokens)
}

func Summarize(provider Provider, entry LogEntry) []LogItem {
	switch entry.Kind {
	case LogCommand:
		return []LogItem{{Title: "Command", Detail: entry.Text}}
	case LogStdin:
		return []LogItem{{Title: "Prompt", Detail: entry.Text}}
	case LogStderr:
		if strings.TrimSpace(entry.Text) == "" {
			return nil
		}
		return []LogItem{{Title: "Stderr", Detail: entry.Text}}
	case LogExit:
		return []LogItem{{Title: entry.Text, Failed: entry.Text != exitOK, exit: exitStatus(entry.Text)}}
	case LogStdout:
		if strings.TrimSpace(entry.Text) == "" {
			return nil
		}
		if !json.Valid([]byte(entry.Text)) {
			return []LogItem{{Title: "Output", Detail: entry.Text}}
		}
		return specOf(provider).logItems([]byte(entry.Text))
	}
	return nil
}

type claudeLogLine struct {
	Type      string   `json:"type"`
	Subtype   string   `json:"subtype"`
	SessionID string   `json:"session_id"`
	Model     string   `json:"model"`
	CWD       string   `json:"cwd"`
	Mode      string   `json:"permissionMode"`
	Tools     []string `json:"tools"`
	Hook      string   `json:"hook_name"`
	Outcome   string   `json:"outcome"`
	Stdout    string   `json:"stdout"`
	Stderr    string   `json:"stderr"`
	Output    string   `json:"output"`
	Content   string   `json:"content"`
	Parent    *string  `json:"parent_tool_use_id"`
	Message   *struct {
		Content []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			Thinking string          `json:"thinking"`
			Name     string          `json:"name"`
			Input    map[string]any  `json:"input"`
			Content  json.RawMessage `json:"content"`
			IsError  bool            `json:"is_error"`
		} `json:"content"`
		Usage *claudeTokens `json:"usage"`
	} `json:"message"`
	RateLimit *struct {
		Status string `json:"status"`
		Type   string `json:"rateLimitType"`
	} `json:"rate_limit_info"`
	Result     string        `json:"result"`
	IsError    bool          `json:"is_error"`
	DurationMS int64         `json:"duration_ms"`
	Turns      int           `json:"num_turns"`
	Cost       float64       `json:"total_cost_usd"`
	Usage      *claudeTokens `json:"usage"`
	ModelUsage map[string]struct {
		Input      int `json:"inputTokens"`
		Output     int `json:"outputTokens"`
		CacheRead  int `json:"cacheReadInputTokens"`
		CacheWrite int `json:"cacheCreationInputTokens"`
	} `json:"modelUsage"`
}

type claudeTokens struct {
	Input      int `json:"input_tokens"`
	Output     int `json:"output_tokens"`
	CacheRead  int `json:"cache_read_input_tokens"`
	CacheWrite int `json:"cache_creation_input_tokens"`
}

func claudeItems(raw []byte) []LogItem {
	var line claudeLogLine
	if json.Unmarshal(raw, &line) != nil {
		return []LogItem{{Title: "Output", Detail: string(raw)}}
	}
	switch line.Type {
	case "stream_event":
		return nil
	case "system":
		return claudeSystem(line)
	case "rate_limit_event":
		if line.RateLimit == nil {
			return []LogItem{{Title: "Rate limit"}}
		}
		return []LogItem{{Title: "Rate limit: " + line.RateLimit.Status, Detail: line.RateLimit.Type, Failed: line.RateLimit.Status != "allowed"}}
	case "assistant", "user":
		if line.Message == nil {
			return nil
		}
		var items []LogItem
		for _, b := range line.Message.Content {
			switch b.Type {
			case "text":
				items = append(items, LogItem{Title: "Assistant", Detail: b.Text})
			case "thinking":
				if b.Thinking != "" {
					items = append(items, LogItem{Title: "Thinking", Detail: b.Thinking})
				}
			case "tool_use":
				items = append(items, LogItem{Title: "Tool call", Detail: toolActivity(claudeBlock{Name: b.Name, Input: b.Input}, "")})
			case "tool_result":
				title := "Tool result"
				if b.IsError {
					title = "Tool error"
				}
				items = append(items, LogItem{Title: title, Detail: toolResultText(b.Content), Failed: b.IsError})
			}
		}
		if tokens := line.Message.Usage; tokens != nil && line.Type == "assistant" && line.Parent == nil && len(items) > 0 {
			items[0].Usage = &Usage{Context: tokens.Input + tokens.CacheRead + tokens.CacheWrite + tokens.Output}
		}
		return items
	case "result":
		failed := line.IsError || strings.HasPrefix(line.Subtype, "error")
		usage := claudeUsage(line)
		item := LogItem{Title: "Result: " + firstNonEmpty(line.Subtype, "done"), Detail: claudeStats(line, usage), Failed: failed, Usage: &usage, result: failedStatus(failed)}
		if failed && line.Result != "" {
			item.Detail = line.Result + "\n" + item.Detail
		}
		return []LogItem{item}
	}
	return []LogItem{{Title: firstNonEmpty(line.Type, "Output")}}
}

func claudeSystem(line claudeLogLine) []LogItem {
	switch line.Subtype {
	case "status", "thinking_tokens":
		return nil
	case "init":
		return []LogItem{{
			Title:  "Session started",
			Detail: fmt.Sprintf("model %s, permission mode %s, %d tools, cwd %s", line.Model, line.Mode, len(line.Tools), line.CWD),
		}}
	case "hook_started":
		return []LogItem{{Title: "Hook started: " + line.Hook}}
	case "hook_response":
		detail := strings.TrimSpace(strings.Join([]string{line.Output, line.Stdout, line.Stderr}, "\n"))
		return []LogItem{{Title: "Hook " + line.Hook + ": " + line.Outcome, Detail: detail, Failed: line.Outcome != "success"}}
	case "informational":
		return []LogItem{{Title: "Notice", Detail: line.Content}}
	}
	return []LogItem{{Title: "System: " + line.Subtype}}
}

func claudeStats(line claudeLogLine, usage Usage) string {
	return fmt.Sprintf("%d turns, %.1f s\n%s", line.Turns, float64(line.DurationMS)/1000, usage)
}

// claudeUsage reads the tokens of the run from the top-level usage.
// modelUsage and the cost count the whole session, also the runs this one
// resumed, so modelUsage is only the fallback and the cost is the
// session's. The app subtracts the cost of the resumed run.
func claudeUsage(line claudeLogLine) Usage {
	usage := Usage{Session: line.SessionID, SessionCost: line.Cost}
	if tokens := line.Usage; tokens != nil {
		usage.Add(Usage{Input: tokens.Input, Output: tokens.Output, CacheRead: tokens.CacheRead, CacheWrite: tokens.CacheWrite})
		return usage
	}
	for _, m := range line.ModelUsage {
		usage.Add(Usage{Input: m.Input, Output: m.Output, CacheRead: m.CacheRead, CacheWrite: m.CacheWrite})
	}
	return usage
}

func toolResultText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return string(raw)
	}
	var texts []string
	for _, b := range blocks {
		if b.Text != "" {
			texts = append(texts, b.Text)
		} else {
			texts = append(texts, "["+b.Type+"]")
		}
	}
	return strings.Join(texts, "\n")
}

type codexLogLine struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id"`
	Message  string `json:"message"`
	Error    *struct {
		Message string `json:"message"`
	} `json:"error"`
	Item *struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		Command  json.RawMessage `json:"command"`
		Output   string          `json:"aggregated_output"`
		ExitCode *int            `json:"exit_code"`
		Status   string          `json:"status"`
	} `json:"item"`
	Usage *struct {
		Input     int `json:"input_tokens"`
		Cached    int `json:"cached_input_tokens"`
		Output    int `json:"output_tokens"`
		Reasoning int `json:"reasoning_output_tokens"`
	} `json:"usage"`
}

func codexItems(raw []byte) []LogItem {
	var line codexLogLine
	if json.Unmarshal(raw, &line) != nil {
		return []LogItem{{Title: "Output", Detail: string(raw)}}
	}
	switch line.Type {
	case "thread.started":
		return []LogItem{{Title: "Session started", Detail: line.ThreadID}}
	case "turn.started", "item.updated":
		return nil
	case "item.started":
		if line.Item != nil && line.Item.Type == "command_execution" {
			return []LogItem{{Title: "Tool call", Detail: commandText(line.Item.Command)}}
		}
		return nil
	case "item.completed":
		return codexCompleted(line)
	case "error":
		return []LogItem{{Title: "Notice", Detail: line.Message}}
	case "turn.failed":
		msg := ""
		if line.Error != nil {
			msg = line.Error.Message
		}
		return []LogItem{{Title: "Turn failed", Detail: msg, Failed: true, result: RunFailed}}
	case "turn.completed":
		item := LogItem{Title: "Turn completed", result: RunDone}
		if tokens := line.Usage; tokens != nil {
			usage := Usage{Input: tokens.Input - tokens.Cached, CacheRead: tokens.Cached, Output: tokens.Output}
			item.Usage = &usage
			item.Detail = fmt.Sprintf("%s, of them %s reasoning", usage, Count(tokens.Reasoning))
		}
		return []LogItem{item}
	}
	return []LogItem{{Title: firstNonEmpty(line.Type, "Output")}}
}

func codexCompleted(line codexLogLine) []LogItem {
	it := line.Item
	if it == nil {
		return nil
	}
	switch it.Type {
	case "agent_message":
		return []LogItem{{Title: "Assistant", Detail: it.Text}}
	case "reasoning":
		return []LogItem{{Title: "Thinking", Detail: it.Text}}
	case "command_execution":
		title, failed := "Tool result", it.Status == "failed"
		if it.ExitCode != nil {
			title = fmt.Sprintf("Tool result, exit %d", *it.ExitCode)
			failed = failed || *it.ExitCode != 0
		}
		detail := it.Output
		if cmd := commandText(it.Command); cmd != "" {
			detail = "$ " + cmd + "\n" + detail
		}
		return []LogItem{{Title: title, Detail: detail, Failed: failed}}
	}
	return []LogItem{{Title: "Item: " + it.Type, Detail: it.Text}}
}
