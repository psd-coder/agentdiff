package agent

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func runLog(t *testing.T, ctx context.Context, cfg Config, req Request) []LogEntry {
	t.Helper()
	var entries []LogEntry
	req.Log = func(e LogEntry) { entries = append(entries, e) }
	collect(t, Run(ctx, cfg, req), 5*time.Second)
	return entries
}

func logKinds(entries []LogEntry) []LogKind {
	out := make([]LogKind, len(entries))
	for i, e := range entries {
		out[i] = e.Kind
	}
	return out
}

func TestRunLog(t *testing.T) {
	fixture := fixturePath(t, "codex_exec.jsonl")
	bin := writeScript(t, "/bin/cat > /dev/null\necho 'warning: slow' >&2\n/bin/cat "+fixture+"\nprintf 'no newline' >&2")
	dir := t.TempDir()
	entries := runLog(t, t.Context(), Config{Provider: Codex, Binary: bin}, Request{Dir: dir, Prompt: "question"})

	lines, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	stdout := strings.Count(string(lines), "\n")
	var got []string
	for _, e := range entries {
		switch e.Kind {
		case LogStdout:
			stdout--
		case LogStderr:
			got = append(got, e.Text)
		case LogCommand, LogStdin, LogExit:
		}
	}
	if stdout != 0 {
		t.Errorf("stdout lines off by %d", stdout)
	}
	if !slices.Equal(got, []string{"warning: slow", "no newline"}) {
		t.Errorf("stderr = %q", got)
	}
	if entries[0].Kind != LogCommand || !strings.HasPrefix(entries[0].Text, "cd "+dir+" && "+bin+" exec --json") {
		t.Errorf("command = %+v", entries[0])
	}
	if entries[1].Kind != LogStdin || entries[1].Text != "question" {
		t.Errorf("stdin = %+v", entries[1])
	}
	if last := entries[len(entries)-1]; last.Kind != LogExit || last.Text != exitOK {
		t.Errorf("last = %+v", last)
	}
}

func TestRunLogExit(t *testing.T) {
	t.Run("status", func(t *testing.T) {
		bin := writeScript(t, "exit 3")
		entries := runLog(t, t.Context(), Config{Provider: Claude, Binary: bin}, Request{})
		if last := entries[len(entries)-1]; last.Text != "Exited with status 3" {
			t.Errorf("last = %+v", last)
		}
	})
	t.Run("stopped", func(t *testing.T) {
		bin := writeScript(t, "sleep 10")
		ctx, cancel := context.WithCancel(t.Context())
		time.AfterFunc(200*time.Millisecond, cancel)
		entries := runLog(t, ctx, Config{Provider: Claude, Binary: bin}, Request{})
		if last := entries[len(entries)-1]; last.Text != exitStopped {
			t.Errorf("last = %+v", last)
		}
	})
	t.Run("cancelled after the result", func(t *testing.T) {
		bin := writeScript(t, "/bin/cat "+fixturePath(t, "claude_stream.jsonl")+"\nsleep 30")
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var entries []LogEntry
		req := Request{Log: func(e LogEntry) { entries = append(entries, e) }}
		var events []Event
		for ev := range Run(ctx, Config{Provider: Claude, Binary: bin}, req) {
			events = append(events, ev)
			if ev.Kind == EventDone {
				cancel()
			}
		}
		if last := assertOneTerminal(t, events); last.Kind != EventDone {
			t.Fatalf("last event = %+v", last)
		}
		last := entries[len(entries)-1]
		if last.Kind != LogExit || last.Text != exitOK {
			t.Errorf("last = %+v", last)
		}
		if items := Summarize(Claude, last); len(items) != 1 || items[0].Failed {
			t.Errorf("items = %+v", items)
		}
	})
	t.Run("missing binary", func(t *testing.T) {
		entries := runLog(t, t.Context(), Config{Provider: Claude, Binary: "/nonexistent/claude"}, Request{})
		if !slices.Equal(logKinds(entries), []LogKind{LogExit}) || !strings.HasPrefix(entries[0].Text, "Could not start: ") {
			t.Errorf("entries = %+v", entries)
		}
	})
}

func TestCommandLine(t *testing.T) {
	got := commandLine("/my repo", "/bin/claude", []string{"-p", "Bash(git diff:*)", "it's"})
	want := `cd '/my repo' && /bin/claude -p 'Bash(git diff:*)' 'it'\''s'`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func fixtureItems(t *testing.T, p Provider, name string) []LogItem {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(nil, maxLineSize)
	var items []LogItem
	for scanner.Scan() {
		items = append(items, Summarize(p, LogEntry{Kind: LogStdout, Text: scanner.Text()})...)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return items
}

func titles(items []LogItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Title
	}
	return out
}

func TestSummarizeClaude(t *testing.T) {
	items := fixtureItems(t, Claude, "claude_tool.jsonl")
	want := []string{
		"Hook started: SessionStart:startup", "Hook started: SessionStart:startup",
		"Hook SessionStart:startup: success", "Hook SessionStart:startup: success",
		"Session started", "Tool call", "Rate limit: allowed", "Tool result", "Assistant", "Notice", "Result: success",
	}
	if !slices.Equal(titles(items), want) {
		t.Fatalf("titles = %q", titles(items))
	}
	byTitle := map[string]LogItem{}
	for _, it := range items {
		byTitle[it.Title] = it
	}
	if d := byTitle["Tool call"].Detail; !strings.HasPrefix(d, "Bash: git -C ") || !strings.HasSuffix(d, " log -1 --oneline") {
		t.Errorf("tool call = %q", d)
	}
	if d := byTitle["Tool result"].Detail; !strings.Contains(d, "Scaffold MyGo native app") {
		t.Errorf("tool result = %q", d)
	}
	res := byTitle["Result: success"]
	if want := (Usage{Input: 4, Output: 119, CacheRead: 34947, CacheWrite: 35116, Session: "6a699d1b-afa7-40a4-b527-0cf3cd8745ca", SessionCost: 0.0074325699999999995}); res.Usage == nil || *res.Usage != want {
		t.Errorf("usage = %+v", res.Usage)
	}
	if want := "2 turns, 6.8 s\n70k tokens processed: 4 in, 34k cache read, 35k cache write, 119 out, $0.0074 at API prices for the whole session"; res.Detail != want {
		t.Errorf("result = %q", res.Detail)
	}
	if d := byTitle["Session started"].Detail; !strings.HasPrefix(d, "model claude-haiku-5-5, permission mode ") {
		t.Errorf("session = %q", d)
	}
	var run Usage
	for _, it := range items {
		if it.Usage != nil {
			run.Add(*it.Usage)
		}
	}
	if run.Context != 2+34947+169+8 || run.Total() != 70186 {
		t.Errorf("run usage = %+v", run)
	}
	if got := run.Short(); got != "35k tokens" {
		t.Errorf("short = %q", got)
	}
}

func TestClaudeSubagentContext(t *testing.T) {
	line := `{"type":"assistant","parent_tool_use_id":"toolu_1","message":{"content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":5,"output_tokens":1}}}`
	items := Summarize(Claude, LogEntry{Kind: LogStdout, Text: line})
	if len(items) != 1 || items[0].Usage != nil {
		t.Errorf("items = %+v", items)
	}
}

func TestUsageShort(t *testing.T) {
	u := Usage{Input: 1000, CacheRead: 20000}
	if got := u.Short(); got != "21k tokens processed" {
		t.Errorf("without context = %q", got)
	}
	u.Add(Usage{Context: 9000})
	u.Add(Usage{Output: 500})
	if got := u.Short(); got != "9.0k tokens" {
		t.Errorf("with context = %q", got)
	}
	if want := "9.0k tokens in context, 21k tokens processed: 1.0k in, 20k cache read, 0 cache write, 500 out"; u.String() != want {
		t.Errorf("string = %q", u.String())
	}
}

func TestSummarizeCodex(t *testing.T) {
	items := fixtureItems(t, Codex, "codex_fork.jsonl")
	want := []string{"Session started", "Assistant", "Tool call", "Tool result, exit 0", "Assistant", "Turn completed"}
	if !slices.Equal(titles(items), want) {
		t.Fatalf("titles = %q", titles(items))
	}
	if items[2].Detail != "rtk git log -1 --oneline" {
		t.Errorf("tool call = %q", items[2].Detail)
	}
	if want := (Usage{Input: 27107, CacheRead: 34048, Output: 128}); items[5].Usage == nil || *items[5].Usage != want {
		t.Errorf("usage = %+v", items[5].Usage)
	}
	if items[5].Detail != "61k tokens processed: 27k in, 34k cache read, 0 cache write, 128 out, of them 16 reasoning" {
		t.Errorf("usage detail = %q", items[5].Detail)
	}
}

func TestSummarizeEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		p     Provider
		entry LogEntry
		want  []LogItem
	}{
		{"exit ok", Claude, LogEntry{Kind: LogExit, Text: exitOK}, []LogItem{{Title: exitOK}}},
		{"exit stopped", Claude, LogEntry{Kind: LogExit, Text: exitStopped}, []LogItem{{Title: exitStopped, Failed: true}}},
		{"blank stderr", Claude, LogEntry{Kind: LogStderr, Text: "  "}, nil},
		{"plain stdout", Codex, LogEntry{Kind: LogStdout, Text: "Reading prompt from stdin..."}, []LogItem{{Title: "Output", Detail: "Reading prompt from stdin..."}}},
		{"stream event", Claude, LogEntry{Kind: LogStdout, Text: `{"type":"stream_event","event":{}}`}, nil},
		{
			"tool error with blocks", Claude,
			LogEntry{Kind: LogStdout, Text: `{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":[{"type":"text","text":"denied"}]}]}}`},
			[]LogItem{{Title: "Tool error", Detail: "denied", Failed: true}},
		},
		{
			"failed result", Claude,
			LogEntry{Kind: LogStdout, Text: `{"type":"result","subtype":"error_max_turns","is_error":true,"result":"too long","num_turns":9}`},
			[]LogItem{{Title: "Result: error_max_turns", Detail: "too long\n9 turns, 0.0 s\n0 tokens processed: 0 in, 0 cache read, 0 cache write, 0 out", Failed: true, Usage: &Usage{}}},
		},
		{
			"failed command", Codex,
			LogEntry{Kind: LogStdout, Text: `{"type":"item.completed","item":{"type":"command_execution","aggregated_output":"nope","exit_code":1,"status":"failed"}}`},
			[]LogItem{{Title: "Tool result, exit 1", Detail: "nope", Failed: true}},
		},
		{
			"turn failed", Codex,
			LogEntry{Kind: LogStdout, Text: `{"type":"turn.failed","error":{"message":"quota"}}`},
			[]LogItem{{Title: "Turn failed", Detail: "quota", Failed: true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Summarize(tt.p, tt.entry); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// A forked run reports the tokens of the whole session in modelUsage, the
// parent's included. Its own tokens are in usage.
func TestClaudeUsageFromFork(t *testing.T) {
	items := fixtureItems(t, Claude, "claude_fork.jsonl")
	last := items[len(items)-1]
	if want := (Usage{Input: 2, Output: 18, CacheRead: 31953, CacheWrite: 1744, Session: "4d856974-081d-4842-b5be-2c804b37991b", SessionCost: 0.0074112299999999996}); last.Usage == nil || *last.Usage != want {
		t.Errorf("usage = %+v", last.Usage)
	}
}

func TestCount(t *testing.T) {
	for n, want := range map[int]string{950: "950", 1234: "1.2k", 12345: "12k", 1_234_567: "1.2M"} {
		if got := Count(n); got != want {
			t.Errorf("Count(%d) = %q, want %q", n, got, want)
		}
	}
}
