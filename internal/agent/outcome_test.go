package agent

import (
	"math"
	"testing"
)

func withExit(items []LogItem, provider Provider, text string) []LogItem {
	return append(items, Summarize(provider, LogEntry{Kind: LogExit, Text: text})...)
}

func TestStatus(t *testing.T) {
	claudeErr := Summarize(Claude, LogEntry{Kind: LogStdout, Text: `{"type":"result","subtype":"error_max_turns","is_error":true}`})
	codexFailed := Summarize(Codex, LogEntry{Kind: LogStdout, Text: `{"type":"turn.failed","error":{"message":"quota"}}`})
	start := Summarize(Claude, LogEntry{Kind: LogCommand, Text: "claude -p"})
	tests := []struct {
		name  string
		items []LogItem
		want  RunStatus
	}{
		{"claude done", withExit(fixtureItems(t, Claude, "claude_tool.jsonl"), Claude, exitOK), RunDone},
		{"claude fork done", withExit(fixtureItems(t, Claude, "claude_fork.jsonl"), Claude, exitOK), RunDone},
		{"codex done", withExit(fixtureItems(t, Codex, "codex_exec.jsonl"), Codex, exitOK), RunDone},
		{"old log stopped after the result", withExit(fixtureItems(t, Claude, "claude_stream.jsonl"), Claude, exitStopped), RunDone},
		{"codex old log stopped after the result", withExit(fixtureItems(t, Codex, "codex_fork.jsonl"), Codex, exitStopped), RunDone},
		{"result then non-zero exit", withExit(fixtureItems(t, Codex, "codex_exec.jsonl"), Codex, "Exited with status 1"), RunDone},
		{"stopped before the result", withExit(start, Claude, exitStopped), RunStopped},
		{"non-zero exit", withExit(start, Claude, "Exited with status 3"), RunFailed},
		{"could not start", withExit(nil, Claude, "Could not start: no claude"), RunFailed},
		{"exit 0 without a result", withExit(start, Claude, exitOK), RunFailed},
		{"claude error result", withExit(claudeErr, Claude, exitOK), RunFailed},
		{"codex turn failed", withExit(codexFailed, Codex, "Exited with status 1"), RunFailed},
		{"still running", start, RunUnknown},
		{"empty", nil, RunUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Status(tt.items); got != tt.want {
				t.Errorf("status %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSumUsage(t *testing.T) {
	if got := SumUsage(fixtureItems(t, Codex, "codex_exec.jsonl")[:1]); got != nil {
		t.Errorf("usage without any report = %+v", got)
	}
	items := []LogItem{
		{Usage: &Usage{Context: 100}},
		{Title: "Tool call"},
		{Usage: &Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, Context: 200, Session: "s", SessionCost: 0.5}},
		{Usage: &Usage{Input: 10, Output: 20}},
	}
	want := Usage{Input: 11, Output: 22, CacheRead: 3, CacheWrite: 4, Context: 200, Session: "s", SessionCost: 0.5}
	if got := SumUsage(items); got == nil || *got != want {
		t.Errorf("usage = %+v, want %+v", got, want)
	}
}

func TestOwnCost(t *testing.T) {
	parent := &Usage{Session: "p", SessionCost: 0.5}
	tests := []struct {
		name    string
		usage   Usage
		resumed bool
		parent  *Usage
		want    float64
	}{
		{"new session", Usage{SessionCost: 0.6}, false, nil, 0.6},
		{"resumed", Usage{SessionCost: 0.6}, true, parent, 0.1},
		{"resumed run out of the log", Usage{SessionCost: 0.6}, true, nil, 0},
		{"session cost below the parent", Usage{SessionCost: 0.4}, true, parent, 0},
		{"codex reports no cost", Usage{}, true, &Usage{}, 0},
		{"cost known already", Usage{CostUSD: 0.2}, true, nil, 0.2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OwnCost(tt.usage, tt.resumed, tt.parent); math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("cost %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResumedSession(t *testing.T) {
	const session = "2b02f0c1-3491-4bdd-9e05-ee7282cd2510"
	tricky := Request{Dir: "/my repo", SystemPrompt: "don't resume 0d5c3c1e-7c3b-4a5e-9f00-0123456789ab --resume x fork y"}
	for _, p := range []Provider{Claude, Codex} {
		for _, tt := range []struct {
			name string
			id   string
			fork bool
		}{{"new", "", false}, {"new with fork", "", true}, {"resume", session, false}, {"fork", session, true}} {
			t.Run(string(p)+" "+tt.name, func(t *testing.T) {
				cfg := Config{Provider: p, Model: "m", Effort: "low", ExtraArgs: []string{"--x", "it's"}}
				req := tricky
				req.ResumeID, req.Fork = tt.id, tt.fork
				line := commandLine(req.Dir, "/bin/"+string(p), Args(cfg, req))
				if got := ResumedSession(p, line); got != tt.id {
					t.Errorf("session %q, want %q\n%s", got, tt.id, line)
				}
			})
		}
	}
}
