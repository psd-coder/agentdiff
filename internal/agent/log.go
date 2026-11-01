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

func (l *logger) add(kind LogKind, text string) {
	if l.sink == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sink(LogEntry{Time: time.Now(), Kind: kind, Text: text})
}

// lineWriter hands each complete line written to it to emit.
type lineWriter struct {
	mu   sync.Mutex
	buf  []byte
	emit func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		w.emit(strings.TrimRight(string(w.buf[:i]), "\r"))
		w.buf = w.buf[i+1:]
	}
}

func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.emit(string(w.buf))
		w.buf = nil
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

func shellQuote(s string) string {
	if plainArg.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ResumedSession is the session that a command logged by commandLine
// resumed or forked, empty for a new session.
func ResumedSession(p Provider, command string) string {
	return specOf(p).resumed(shellWords(command))
}

// shellWords splits a command line that shellQuote wrote back into words.
func shellWords(line string) []string {
	var words []string
	var word strings.Builder
	inWord, quoted := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quoted && c == '\'':
			quoted = false
		case quoted:
			word.WriteByte(c)
		case c == '\'':
			quoted, inWord = true, true
		case c == '\\' && i+1 < len(line):
			i++
			word.WriteByte(line[i])
			inWord = true
		case c == ' ':
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
		default:
			word.WriteByte(c)
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

func (u Usage) Total() int { return u.Input + u.Output + u.CacheRead + u.CacheWrite }

func (u *Usage) Add(o Usage) {
	u.Input += o.Input
	u.Output += o.Output
	u.CacheRead += o.CacheRead
	u.CacheWrite += o.CacheWrite
	u.CostUSD += o.CostUSD
	if o.Context > 0 {
		u.Context = o.Context
	}
	if o.Session != "" {
		u.Session, u.SessionCost = o.Session, o.SessionCost
	}
}

func (u Usage) String() string {
	s := fmt.Sprintf("%s tokens processed: %s in, %s cache read, %s cache write, %s out",
		Count(u.Total()), Count(u.Input), Count(u.CacheRead), Count(u.CacheWrite), Count(u.Output))
	switch {
	case u.CostUSD > 0:
		s += fmt.Sprintf(", $%.4f at API prices", u.CostUSD)
	case u.SessionCost > 0:
		s += fmt.Sprintf(", $%.4f at API prices for the whole session", u.SessionCost)
	}
	if u.Context > 0 {
		s = fmt.Sprintf("%s tokens in context, %s", Count(u.Context), s)
	}
	return s
}

// Short is the context size, or the processed total when the CLI does
// not report the context.
func (u Usage) Short() string {
	if u.Context > 0 {
		return Count(u.Context) + " tokens"
	}
	return Count(u.Total()) + " tokens processed"
}

// Count writes a token count short: 950, 12.3k, 1.2M.
func Count(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

func Summarize(p Provider, e LogEntry) []LogItem {
	switch e.Kind {
	case LogCommand:
		return []LogItem{{Title: "Command", Detail: e.Text}}
	case LogStdin:
		return []LogItem{{Title: "Prompt", Detail: e.Text}}
	case LogStderr:
		if strings.TrimSpace(e.Text) == "" {
			return nil
		}
		return []LogItem{{Title: "Stderr", Detail: e.Text}}
	case LogExit:
		return []LogItem{{Title: e.Text, Failed: e.Text != exitOK, exit: exitStatus(e.Text)}}
	case LogStdout:
		if strings.TrimSpace(e.Text) == "" {
			return nil
		}
		if !json.Valid([]byte(e.Text)) {
			return []LogItem{{Title: "Output", Detail: e.Text}}
		}
		return specOf(p).logItems([]byte(e.Text))
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
	var l claudeLogLine
	if json.Unmarshal(raw, &l) != nil {
		return []LogItem{{Title: "Output", Detail: string(raw)}}
	}
	switch l.Type {
	case "stream_event":
		return nil
	case "system":
		return claudeSystem(l)
	case "rate_limit_event":
		if l.RateLimit == nil {
			return []LogItem{{Title: "Rate limit"}}
		}
		return []LogItem{{Title: "Rate limit: " + l.RateLimit.Status, Detail: l.RateLimit.Type, Failed: l.RateLimit.Status != "allowed"}}
	case "assistant", "user":
		if l.Message == nil {
			return nil
		}
		var items []LogItem
		for _, b := range l.Message.Content {
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
		if m := l.Message.Usage; m != nil && l.Type == "assistant" && l.Parent == nil && len(items) > 0 {
			items[0].Usage = &Usage{Context: m.Input + m.CacheRead + m.CacheWrite + m.Output}
		}
		return items
	case "result":
		failed := l.IsError || strings.HasPrefix(l.Subtype, "error")
		u := claudeUsage(l)
		item := LogItem{Title: "Result: " + firstNonEmpty(l.Subtype, "done"), Detail: claudeStats(l, u), Failed: failed, Usage: &u, result: failedStatus(failed)}
		if failed && l.Result != "" {
			item.Detail = l.Result + "\n" + item.Detail
		}
		return []LogItem{item}
	}
	return []LogItem{{Title: firstNonEmpty(l.Type, "Output")}}
}

func claudeSystem(l claudeLogLine) []LogItem {
	switch l.Subtype {
	case "status", "thinking_tokens":
		return nil
	case "init":
		return []LogItem{{
			Title:  "Session started",
			Detail: fmt.Sprintf("model %s, permission mode %s, %d tools, cwd %s", l.Model, l.Mode, len(l.Tools), l.CWD),
		}}
	case "hook_started":
		return []LogItem{{Title: "Hook started: " + l.Hook}}
	case "hook_response":
		detail := strings.TrimSpace(strings.Join([]string{l.Output, l.Stdout, l.Stderr}, "\n"))
		return []LogItem{{Title: "Hook " + l.Hook + ": " + l.Outcome, Detail: detail, Failed: l.Outcome != "success"}}
	case "informational":
		return []LogItem{{Title: "Notice", Detail: l.Content}}
	}
	return []LogItem{{Title: "System: " + l.Subtype}}
}

func claudeStats(l claudeLogLine, u Usage) string {
	return fmt.Sprintf("%d turns, %.1f s\n%s", l.Turns, float64(l.DurationMS)/1000, u)
}

// claudeUsage reads the tokens of the run from the top-level usage.
// modelUsage and the cost count the whole session, also the runs this one
// resumed, so modelUsage is only the fallback and the cost is the
// session's. The app subtracts the cost of the resumed run.
func claudeUsage(l claudeLogLine) Usage {
	u := Usage{Session: l.SessionID, SessionCost: l.Cost}
	if t := l.Usage; t != nil {
		u.Add(Usage{Input: t.Input, Output: t.Output, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite})
		return u
	}
	for _, m := range l.ModelUsage {
		u.Add(Usage{Input: m.Input, Output: m.Output, CacheRead: m.CacheRead, CacheWrite: m.CacheWrite})
	}
	return u
}

func toolResultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
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
	var l codexLogLine
	if json.Unmarshal(raw, &l) != nil {
		return []LogItem{{Title: "Output", Detail: string(raw)}}
	}
	switch l.Type {
	case "thread.started":
		return []LogItem{{Title: "Session started", Detail: l.ThreadID}}
	case "turn.started", "item.updated":
		return nil
	case "item.started":
		if l.Item != nil && l.Item.Type == "command_execution" {
			return []LogItem{{Title: "Tool call", Detail: commandText(l.Item.Command)}}
		}
		return nil
	case "item.completed":
		return codexCompleted(l)
	case "error":
		return []LogItem{{Title: "Notice", Detail: l.Message}}
	case "turn.failed":
		msg := ""
		if l.Error != nil {
			msg = l.Error.Message
		}
		return []LogItem{{Title: "Turn failed", Detail: msg, Failed: true, result: RunFailed}}
	case "turn.completed":
		item := LogItem{Title: "Turn completed", result: RunDone}
		if c := l.Usage; c != nil {
			u := Usage{Input: c.Input - c.Cached, CacheRead: c.Cached, Output: c.Output}
			item.Usage = &u
			item.Detail = fmt.Sprintf("%s, of them %s reasoning", u, Count(c.Reasoning))
		}
		return []LogItem{item}
	}
	return []LogItem{{Title: firstNonEmpty(l.Type, "Output")}}
}

func codexCompleted(l codexLogLine) []LogItem {
	it := l.Item
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
