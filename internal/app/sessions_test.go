package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/review"
)

const sessionsAnswer = "```json\n" + `{"summary": "s", "steps": [{"title": "A", "text": "a"}, {"title": "B", "text": "b"}], "groups": []}` + "\n```"

// fakeCodex is a codex CLI that answers every prompt with answer, in
// thread id.
func fakeCodex(t *testing.T, id, answer string) string {
	t.Helper()
	dir := t.TempDir()
	msg, err := json.Marshal(map[string]any{"type": "item.completed", "item": map[string]string{"id": "item_0", "type": "agent_message", "text": answer}})
	if err != nil {
		t.Fatal(err)
	}
	out := `{"type":"thread.started","thread_id":"` + id + `"}` + "\n" + string(msg) + "\n" + `{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":5}}` + "\n"
	write(t, dir, "out.jsonl", out)
	write(t, dir, "codex", "#!/bin/sh\ncat > /dev/null\ncat "+filepath.Join(dir, "out.jsonl")+"\n")
	if err := os.Chmod(filepath.Join(dir, "codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "codex")
}

// useAgent makes the next runs go to provider, in a new session id.
func useAgent(t *testing.T, a *app, p agent.Provider, id string) {
	t.Helper()
	bin := fakeClaude(t, id, sessionsAnswer)
	if p == agent.Codex {
		bin = fakeCodex(t, id, sessionsAnswer)
	}
	a.cfg.Agent = agent.Config{Provider: p, Binary: bin}
}

func sessionsApp(t *testing.T) *app {
	t.Helper()
	a := newTestApp(t)
	a.cfg.DeeperAhead = false
	a.queue = make(chan func(), 256)
	return a
}

// idle waits until no run of the review is left.
func idle(t *testing.T, a *app, what string) {
	t.Helper()
	pump(t, a, what, func() bool {
		for _, r := range a.runsOf(a.rev) {
			if r.running {
				return false
			}
		}
		return true
	})
}

// lastResumed is the session the latest run resumed or forked, empty for a
// new session.
func lastResumed(a *app) string { return a.runsOf(a.rev)[0].info.Resumed }

// goDeeper starts the walkthrough of a step as Go deeper does.
func goDeeper(a *app, parent string, step int) {
	a.runWalk(a.rev, a.rev.AddWalk(parent, step, ""), false)
}

func withRootOverview(a *app) {
	a.rev.Overview = &review.Overview{Agent: agent.Claude, Sessions: review.Sessions{agent.Claude: "root"}, Generated: time.Now(), Walk: &review.Walkthrough{
		Summary: "s", Steps: []review.Step{{Title: "A", Text: "a"}},
	}}
}

// TestSessionsPerProvider runs an item with claude, then codex, then claude
// again. The codex run starts a new session, and the second claude run
// continues the first claude session.
func TestSessionsPerProvider(t *testing.T) {
	t.Run("overview", func(t *testing.T) {
		a := sessionsApp(t)
		useAgent(t, a, agent.Claude, "c1")
		a.generateOverview()
		idle(t, a, "the claude overview")
		useAgent(t, a, agent.Codex, "x1")
		a.generateOverview()
		idle(t, a, "the codex overview")
		if got := lastResumed(a); got != "" {
			t.Errorf("codex overview resumed %q", got)
		}
		useAgent(t, a, agent.Claude, "c2")
		goDeeper(a, "", 0)
		idle(t, a, "the deeper walkthrough")
		if got := lastResumed(a); got != "c1" {
			t.Errorf("claude walkthrough of the overview forked %q, want c1", got)
		}
	})

	t.Run("deeper walk", func(t *testing.T) {
		a := sessionsApp(t)
		withRootOverview(a)
		useAgent(t, a, agent.Claude, "c1")
		goDeeper(a, "", 0)
		idle(t, a, "the first walkthrough")
		parent := a.rev.Walks[0]
		useAgent(t, a, agent.Codex, "x1")
		goDeeper(a, parent.ID, 0)
		idle(t, a, "the codex walkthrough")
		if got := lastResumed(a); got != "" {
			t.Errorf("codex walkthrough forked %q", got)
		}
		useAgent(t, a, agent.Claude, "c2")
		goDeeper(a, parent.ID, 1)
		idle(t, a, "the claude walkthrough")
		if got := lastResumed(a); got != "c1" {
			t.Errorf("claude walkthrough forked %q, want c1", got)
		}
	})

	t.Run("follow-up question", func(t *testing.T) {
		a := sessionsApp(t)
		useAgent(t, a, agent.Claude, "c1")
		a.askQuestion("", -1, "Why?")
		idle(t, a, "the question")
		w := a.rev.Walks[0]
		useAgent(t, a, agent.Codex, "x1")
		a.askMore(a.rev, w, "And codex?")
		idle(t, a, "the codex follow-up")
		if got := lastResumed(a); got != "" {
			t.Errorf("codex follow-up resumed %q", got)
		}
		if w.Agent != agent.Claude {
			t.Errorf("written by %q after a codex follow-up, want claude, which wrote the first answer", w.Agent)
		}
		useAgent(t, a, agent.Claude, "c2")
		a.askMore(a.rev, w, "And claude?")
		idle(t, a, "the claude follow-up")
		if got := lastResumed(a); got != "c1" {
			t.Errorf("claude follow-up resumed %q, want c1", got)
		}
		useAgent(t, a, agent.Codex, "x2")
		a.askMore(a.rev, w, "And codex again?")
		idle(t, a, "the second codex follow-up")
		if got := lastResumed(a); got != "x1" {
			t.Errorf("second codex follow-up resumed %q, want x1", got)
		}
	})

	t.Run("thread", func(t *testing.T) {
		a := sessionsApp(t)
		withRootOverview(a)
		th := a.rev.AddThread(review.Anchor{Kind: review.AnchorGeneral})
		useAgent(t, a, agent.Claude, "c1")
		a.ask(th, "Why?")
		idle(t, a, "the first answer")
		if got := lastResumed(a); got != "root" {
			t.Errorf("first question forked %q, want the overview session", got)
		}
		useAgent(t, a, agent.Codex, "x1")
		a.ask(th, "And codex?")
		idle(t, a, "the codex answer")
		if got := lastResumed(a); got != "" {
			t.Errorf("codex question resumed %q", got)
		}
		useAgent(t, a, agent.Claude, "c2")
		a.ask(th, "And claude?")
		idle(t, a, "the claude answer")
		if got := lastResumed(a); got != "c1" {
			t.Errorf("claude question resumed %q, want c1", got)
		}
	})
}

// TestFailedStartKeepsSessions runs codex with a missing binary after a
// claude run. The failed run stores no session, so the next codex run
// starts a new session, and the overview keeps its claude label.
func TestFailedStartKeepsSessions(t *testing.T) {
	a := sessionsApp(t)
	useAgent(t, a, agent.Claude, "c1")
	a.generateOverview()
	th := a.rev.AddThread(review.Anchor{Kind: review.AnchorGeneral})
	a.ask(th, "Why?")
	a.askQuestion("", -1, "What?")
	idle(t, a, "the claude runs")
	w := a.rev.Walks[0]

	a.cfg.Agent = agent.Config{Provider: agent.Codex, Binary: filepath.Join(t.TempDir(), "missing")}
	a.generateOverview()
	a.ask(th, "And codex?")
	a.askMore(a.rev, w, "And codex?")
	idle(t, a, "the failed codex runs")
	ov := a.rev.Overview
	if ov.Err == "" || ov.Sessions[agent.Codex] != "" || th.Sessions[agent.Codex] != "" || w.Sessions[agent.Codex] != "" {
		t.Fatalf("after the failed runs: overview %+v, thread %v, walk %v", ov.Sessions, th.Sessions, w.Sessions)
	}
	if ov.Agent != agent.Claude || w.Agent != agent.Claude {
		t.Errorf("written by: overview %q, walk %q, want claude", ov.Agent, w.Agent)
	}
	tt := ui.NewTester(a.view, 1300, 900)
	tt.Frame()
	if label := "By Claude Code, " + ov.Generated.Format("Jan 2 15:04"); !tt.HasText(label) {
		t.Errorf("overview lacks %q", label)
	}

	useAgent(t, a, agent.Codex, "x1")
	a.ask(th, "And now?")
	idle(t, a, "the codex answer")
	if got := lastResumed(a); got != "" {
		t.Errorf("codex question resumed %q", got)
	}
	a.askMore(a.rev, w, "And now?")
	idle(t, a, "the codex follow-up")
	if got := lastResumed(a); got != "" {
		t.Errorf("codex follow-up resumed %q", got)
	}
}
