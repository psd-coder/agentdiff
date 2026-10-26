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

func exitText(ctx context.Context, err error) string {
	var exit *exec.ExitError
	switch {
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

// LogItem is one readable step of a run. A log entry gives none when it
// only repeats another one, as the streamed parts of a message do.
type LogItem struct {
	Title  string
	Detail string
	Failed bool
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
		return []LogItem{{Title: e.Text, Failed: e.Text != exitOK}}
	case LogStdout:
		if strings.TrimSpace(e.Text) == "" {
			return nil
		}
		if !json.Valid([]byte(e.Text)) {
			return []LogItem{{Title: "Output", Detail: e.Text}}
		}
		if p == Codex {
			return codexItems([]byte(e.Text))
		}
		return claudeItems([]byte(e.Text))
	}
	return nil
}

type claudeLogLine struct {
	Type    string   `json:"type"`
	Subtype string   `json:"subtype"`
	Model   string   `json:"model"`
	CWD     string   `json:"cwd"`
	Mode    string   `json:"permissionMode"`
	Tools   []string `json:"tools"`
	Hook    string   `json:"hook_name"`
	Outcome string   `json:"outcome"`
	Stdout  string   `json:"stdout"`
	Stderr  string   `json:"stderr"`
	Output  string   `json:"output"`
	Content string   `json:"content"`
	Message *struct {
		Content []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			Thinking string          `json:"thinking"`
			Name     string          `json:"name"`
			Input    map[string]any  `json:"input"`
			Content  json.RawMessage `json:"content"`
			IsError  bool            `json:"is_error"`
		} `json:"content"`
	} `json:"message"`
	RateLimit *struct {
		Status string `json:"status"`
		Type   string `json:"rateLimitType"`
	} `json:"rate_limit_info"`
	Result     string  `json:"result"`
	IsError    bool    `json:"is_error"`
	DurationMS int64   `json:"duration_ms"`
	Turns      int     `json:"num_turns"`
	Cost       float64 `json:"total_cost_usd"`
	Usage      *struct {
		Input      int `json:"input_tokens"`
		Output     int `json:"output_tokens"`
		CacheRead  int `json:"cache_read_input_tokens"`
		CacheWrite int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
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
		return items
	case "result":
		failed := l.IsError || strings.HasPrefix(l.Subtype, "error")
		item := LogItem{Title: "Result: " + firstNonEmpty(l.Subtype, "done"), Detail: claudeStats(l), Failed: failed}
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

func claudeStats(l claudeLogLine) string {
	parts := []string{
		fmt.Sprintf("%d turns", l.Turns),
		fmt.Sprintf("%.1f s", float64(l.DurationMS)/1000),
		fmt.Sprintf("$%.4f", l.Cost),
	}
	if u := l.Usage; u != nil {
		parts = append(parts, fmt.Sprintf("tokens: %d in, %d out, %d cache read, %d cache write", u.Input, u.Output, u.CacheRead, u.CacheWrite))
	}
	return strings.Join(parts, ", ")
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
		return []LogItem{{Title: "Turn failed", Detail: msg, Failed: true}}
	case "turn.completed":
		item := LogItem{Title: "Turn completed"}
		if u := l.Usage; u != nil {
			item.Detail = fmt.Sprintf("tokens: %d in, %d cached, %d out, %d reasoning", u.Input, u.Cached, u.Output, u.Reasoning)
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
