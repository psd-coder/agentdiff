package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestReadOnlyRules(t *testing.T) {
	rules := readOnlyRules()
	if !slices.Contains(rules, "Bash(git show:*)") || !slices.Contains(rules, "Bash(rtk git show:*)") {
		t.Errorf("rules = %v", rules)
	}
	for _, r := range rules {
		if strings.Contains(r, "sed") || strings.Contains(r, "find") {
			t.Errorf("rule %q can write files", r)
		}
	}
}

func TestMain(m *testing.M) {
	loginEnv = os.Environ
	os.Exit(m.Run())
}

var claudeBase = append([]string{
	"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages",
	"--tools", "Read,Grep,Glob,Bash",
	"--permission-mode", "dontAsk",
	"--allowedTools",
}, readOnlyRules()...)

var codexBase = []string{"exec", "--json", "-s", "read-only", "--skip-git-repo-check"}

func with(base []string, extra ...string) []string {
	return append(append([]string{}, base...), extra...)
}

func TestArgs(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		req  Request
		want []string
	}{
		{"claude new", Config{Provider: Claude}, Request{Dir: "/r", Prompt: "p"}, claudeBase},
		{
			"claude model and system prompt",
			Config{Provider: Claude, Model: "haiku"},
			Request{SystemPrompt: "be brief"},
			with(claudeBase, "--model", "haiku", "--append-system-prompt", "be brief"),
		},
		{"claude effort", Config{Provider: Claude, Effort: "low"}, Request{}, with(claudeBase, "--effort", "low")},
		{"claude resume", Config{Provider: Claude}, Request{ResumeID: "s1"}, with(claudeBase, "--resume", "s1")},
		{
			"claude fork",
			Config{Provider: Claude},
			Request{ResumeID: "s1", Fork: true},
			with(claudeBase, "--resume", "s1", "--fork-session"),
		},
		{"claude fork without resume", Config{Provider: Claude}, Request{Fork: true}, claudeBase},
		{
			"claude extra args last",
			Config{Provider: Claude, Model: "opus", ExtraArgs: []string{"--add-dir", "/x"}},
			Request{ResumeID: "s1"},
			with(claudeBase, "--model", "opus", "--resume", "s1", "--add-dir", "/x"),
		},
		{"codex new", Config{Provider: Codex}, Request{Dir: "/r", Prompt: "p"}, with(codexBase, "-C", "/r", "-")},
		{"codex without dir", Config{Provider: Codex}, Request{}, with(codexBase, "-")},
		{
			"codex model",
			Config{Provider: Codex, Model: "gpt-5"},
			Request{Dir: "/r", SystemPrompt: "ignored in args"},
			with(codexBase, "-C", "/r", "-m", "gpt-5", "-"),
		},
		{"codex effort", Config{Provider: Codex, Effort: "low"}, Request{}, with(codexBase, "-c", `model_reasoning_effort="low"`, "-")},
		{"codex resume", Config{Provider: Codex}, Request{Dir: "/r", ResumeID: "t1"}, with(codexBase, "-C", "/r", "resume", "t1", "-")},
		{
			"codex fork",
			Config{Provider: Codex},
			Request{Dir: "/r", ResumeID: "t1", Fork: true},
			with(codexBase, "-C", "/r", "fork", "t1", "-"),
		},
		{"codex fork without resume", Config{Provider: Codex}, Request{Fork: true}, with(codexBase, "-")},
		{
			"codex extra args before subcommand",
			Config{Provider: Codex, ExtraArgs: []string{"-c", "model_reasoning_effort=low"}},
			Request{ResumeID: "t1"},
			with(codexBase, "-c", "model_reasoning_effort=low", "resume", "t1", "-"),
		},
		{"unknown provider", Config{Provider: "gemini"}, Request{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Args(tt.cfg, tt.req); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Args = %q\nwant   %q", got, tt.want)
			}
		})
	}
}

func TestAutoConfig(t *testing.T) {
	cfg := Config{Provider: Claude, Model: "opus", Effort: "high", AutoEffort: "low"}
	if got := cfg.Auto(); got.Model != "opus" || got.Effort != "low" {
		t.Errorf("auto with an effort only = %+v", got)
	}
	cfg.AutoModel, cfg.AutoEffort = "sonnet", ""
	if got := cfg.Auto(); got.Model != "sonnet" || got.Effort != "high" {
		t.Errorf("auto with a model only = %+v", got)
	}
}

func TestStdinText(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		req  Request
		want string
	}{
		{"claude keeps prompt", Config{Provider: Claude}, Request{Prompt: "q", SystemPrompt: "s"}, "q"},
		{"codex without system prompt", Config{Provider: Codex}, Request{Prompt: "q"}, "q"},
		{
			"codex wraps system prompt",
			Config{Provider: Codex},
			Request{Prompt: "q", SystemPrompt: "s"},
			"<instructions>\ns\n</instructions>\n\nq",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stdinText(tt.cfg, tt.req); got != tt.want {
				t.Errorf("stdinText = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTailBuffer(t *testing.T) {
	b := &tailBuffer{max: 5}
	for _, s := range []string{"ab", "cdef", "", "ghijklmn"} {
		if _, err := b.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	if got := b.String(); got != "jklmn" {
		t.Errorf("tail = %q", got)
	}
}

func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func collect(t *testing.T, ch <-chan Event, timeout time.Duration) []Event {
	t.Helper()
	var events []Event
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-deadline:
			t.Fatalf("channel not closed after %v, events so far: %+v", timeout, events)
		}
	}
}

func assertOneTerminal(t *testing.T, events []Event) Event {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("no events")
	}
	for _, ev := range events[:len(events)-1] {
		if ev.terminal() {
			t.Fatalf("terminal event before the last one: %+v", events)
		}
	}
	last := events[len(events)-1]
	if !last.terminal() {
		t.Fatalf("last event is not terminal: %+v", last)
	}
	return last
}

func TestRunFixtures(t *testing.T) {
	tests := []struct {
		provider Provider
		fixture  string
		session  string
		answer   string
	}{
		{Claude, "claude_tool.jsonl", "6a699d1b-afa7-40a4-b527-0cf3cd8745ca", "Scaffold MyGo native app"},
		{Codex, "codex_exec.jsonl", "01a1735a-bf7c-7a33-8851-e43fd5cba799", "hi there"},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			bin := writeScript(t, "/bin/cat "+fixturePath(t, tt.fixture))
			ch := Run(context.Background(), Config{Provider: tt.provider, Binary: bin}, Request{Dir: t.TempDir(), Prompt: "q"})
			events := collect(t, ch, 5*time.Second)
			last := assertOneTerminal(t, events)
			if last.Kind != EventDone || last.Text != tt.answer || last.SessionID != tt.session {
				t.Errorf("last = %+v", last)
			}
			if events[0].Kind != EventSession || events[0].SessionID != tt.session {
				t.Errorf("first = %+v", events[0])
			}
		})
	}
}

func TestRunPassesPromptOnStdin(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "stdin.txt")
	bin := writeScript(t, "/bin/cat > "+out+"\n/bin/cat "+fixturePath(t, "codex_exec.jsonl"))
	req := Request{Dir: dir, Prompt: "question", SystemPrompt: "rules"}
	last := assertOneTerminal(t, collect(t, Run(context.Background(), Config{Provider: Codex, Binary: bin}, req), 5*time.Second))
	if last.Kind != EventDone {
		t.Fatalf("last = %+v", last)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := stdinText(Config{Provider: Codex}, req); string(got) != want {
		t.Errorf("stdin = %q, want %q", got, want)
	}
}

func TestRunErrors(t *testing.T) {
	tests := []struct {
		name     string
		provider Provider
		script   string
		contains []string
	}{
		{"exit 1 with stderr", Claude, "echo 'invalid api key' >&2\nexit 1", []string{"exit status 1", "invalid api key"}},
		{"empty output", Claude, "exit 0", []string{"claude exited without a result"}},
		{"malformed output", Codex, "echo 'not json'\necho '{'", []string{"codex exited without a result"}},
		{
			"codex error event then exit",
			Codex,
			`echo '{"type":"error","message":"model not supported"}'` + "\nexit 1",
			[]string{"exit status 1", "model not supported"},
		},
		{"codex turn failed", Codex, `echo '{"type":"turn.failed","error":{"message":"quota"}}'`, []string{"quota"}},
		{"claude error result", Claude, `echo '{"type":"result","is_error":true,"result":"API Error"}'`, []string{"API Error"}},
		{
			"line over the size limit",
			Claude,
			fmt.Sprintf("head -c %d /dev/zero | tr '\\0' x", maxLineSize+1),
			[]string{"claude exited without a result", "token too long"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin := writeScript(t, tt.script)
			ch := Run(context.Background(), Config{Provider: tt.provider, Binary: bin}, Request{Dir: t.TempDir()})
			last := assertOneTerminal(t, collect(t, ch, 5*time.Second))
			if last.Kind != EventError {
				t.Fatalf("last = %+v", last)
			}
			for _, s := range tt.contains {
				if !strings.Contains(last.Err.Error(), s) {
					t.Errorf("error %q does not contain %q", last.Err, s)
				}
			}
		})
	}
}

func TestRunStderrTail(t *testing.T) {
	bin := writeScript(t, "i=0\nwhile [ $i -lt 500 ]; do echo \"line $i\" >&2; i=$((i+1)); done\necho END >&2\nexit 2")
	last := assertOneTerminal(t, collect(t, Run(context.Background(), Config{Provider: Claude, Binary: bin}, Request{}), 5*time.Second))
	msg := last.Err.Error()
	if !strings.Contains(msg, "END") || strings.Contains(msg, "line 1\n") || len(msg) > stderrTailSize+100 {
		t.Errorf("unexpected error message (%d bytes): %.200q", len(msg), msg)
	}
}

func TestRunDoneThenNonZeroExit(t *testing.T) {
	bin := writeScript(t, "/bin/cat "+fixturePath(t, "claude_stream.jsonl")+"\nexit 3")
	events := collect(t, Run(context.Background(), Config{Provider: Claude, Binary: bin}, Request{}), 5*time.Second)
	if last := assertOneTerminal(t, events); last.Kind != EventDone {
		t.Errorf("last = %+v", last)
	}
}

func TestRunCancel(t *testing.T) {
	bin := writeScript(t, `echo '{"type":"system","session_id":"s1"}'`+"\nsleep 30")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := Run(ctx, Config{Provider: Claude, Binary: bin}, Request{})
	first := <-ch
	if first.Kind != EventSession || first.SessionID != "s1" {
		t.Fatalf("first = %+v", first)
	}
	cancel()
	// Under waitDelay: a kill of sh alone would leave sleep holding stdout until waitDelay.
	last := assertOneTerminal(t, collect(t, ch, waitDelay-time.Second))
	if !errors.Is(last.Err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", last.Err)
	}
}

func TestRunCancelledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	bin := writeScript(t, "sleep 30")
	last := assertOneTerminal(t, collect(t, Run(ctx, Config{Provider: Codex, Binary: bin}, Request{}), 2*time.Second))
	if !errors.Is(last.Err, context.Canceled) {
		t.Errorf("err = %v", last.Err)
	}
}

func TestRunBinaryNotFound(t *testing.T) {
	tests := []Config{
		{Provider: Claude, Binary: "/nonexistent/claude"},
		{Provider: Codex, Binary: "agentdiff-no-such-binary"},
		{Provider: "gemini"},
	}
	for _, cfg := range tests {
		last := assertOneTerminal(t, collect(t, Run(context.Background(), cfg, Request{}), 2*time.Second))
		if last.Kind != EventError {
			t.Errorf("%+v: last = %+v", cfg, last)
		}
	}
}

func TestAvailable(t *testing.T) {
	bin := writeScript(t, `[ "$1" = "--version" ] && echo "2.1.0 (Claude Code)"`)
	path, version, err := Available(context.Background(), Config{Provider: Claude, Binary: bin})
	if err != nil || path != bin || version != "2.1.0 (Claude Code)" {
		t.Errorf("Available = %q, %q, %v", path, version, err)
	}
}

func TestAvailableErrors(t *testing.T) {
	failing := writeScript(t, "echo boom >&2\nexit 1")
	tests := []struct {
		name string
		cfg  Config
	}{
		{"not found", Config{Provider: Codex, Binary: "agentdiff-no-such-binary"}},
		{"not executable", Config{Provider: Codex, Binary: fixturePath(t, "codex_exec.jsonl")}},
		{"version fails", Config{Provider: Claude, Binary: failing}},
		{"unknown provider", Config{Provider: "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := Available(context.Background(), tt.cfg); err == nil {
				t.Error("want error")
			}
		})
	}
}

func TestAvailableTimeout(t *testing.T) {
	bin := writeScript(t, "sleep 30")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, _, err := Available(ctx, Config{Provider: Claude, Binary: bin}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("took %v", elapsed)
	}
}
