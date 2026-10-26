package agent

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
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
	entries := runLog(t, context.Background(), Config{Provider: Codex, Binary: bin}, Request{Dir: dir, Prompt: "question"})

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
		entries := runLog(t, context.Background(), Config{Provider: Claude, Binary: bin}, Request{})
		if last := entries[len(entries)-1]; last.Text != "Exited with status 3" {
			t.Errorf("last = %+v", last)
		}
	})
	t.Run("stopped", func(t *testing.T) {
		bin := writeScript(t, "sleep 10")
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(200*time.Millisecond, cancel)
		entries := runLog(t, ctx, Config{Provider: Claude, Binary: bin}, Request{})
		if last := entries[len(entries)-1]; last.Text != exitStopped {
			t.Errorf("last = %+v", last)
		}
	})
	t.Run("missing binary", func(t *testing.T) {
		entries := runLog(t, context.Background(), Config{Provider: Claude, Binary: "/nonexistent/claude"}, Request{})
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
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(nil, maxLineSize)
	var items []LogItem
	for scanner.Scan() {
		items = append(items, Summarize(p, LogEntry{Kind: LogStdout, Text: scanner.Text()})...)
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
	if d := byTitle["Result: success"].Detail; !strings.HasPrefix(d, "2 turns, 6.8 s, $0.0074, tokens: 4 in, 119 out") {
		t.Errorf("result = %q", d)
	}
	if d := byTitle["Session started"].Detail; !strings.HasPrefix(d, "model claude-haiku-5-5, permission mode ") {
		t.Errorf("session = %q", d)
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
	if items[5].Detail != "tokens: 61155 in, 34048 cached, 128 out, 16 reasoning" {
		t.Errorf("usage = %q", items[5].Detail)
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
			[]LogItem{{Title: "Result: error_max_turns", Detail: "too long\n9 turns, 0.0 s, $0.0000", Failed: true}},
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
			if got := Summarize(tt.p, tt.entry); !slices.Equal(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
