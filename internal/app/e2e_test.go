package app

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
)

// TestEndToEnd asks a real agent for an overview of the last commit of
// this repository, then a question in a forked session. It uses the
// user's subscription, so it only runs with AGENTDIFF_E2E set to the
// provider: claude or codex.
func TestEndToEnd(t *testing.T) {
	provider := agent.Provider(os.Getenv("AGENTDIFF_E2E"))
	if provider == "" {
		t.Skip("set AGENTDIFF_E2E=claude or codex")
	}
	cfg := agent.Config{Provider: provider, Model: os.Getenv("AGENTDIFF_E2E_MODEL")}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	repo, err := gitx.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	log, err := repo.CommitLog(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	commits, err := log.Page(ctx, 0, 1)
	if err != nil || len(commits) == 0 {
		t.Fatalf("commits: %v", err)
	}
	scope := gitx.Scope{Kind: gitx.ScopeCommit, Commit: commits[0].SHA}
	snap, err := repo.Snapshot(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	files, err := snap.Changes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stat, err := snap.DiffStat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Path
	}
	rev := review.New(repo.Root, scope)
	ex := review.OverviewExchange(rev, review.ChangeSet{Diffstat: stat, Files: names}, cfg, review.DefaultQuestions)
	final, started := runAgent(t, ctx, cfg, ex.Request)
	ex.Started(started)
	ex.Answered(final, time.Now())
	ov := rev.Overview
	if ov.Walk == nil {
		t.Fatalf("no walkthrough in:\n%s", final)
	}
	logWalk(t, ov.Walk)
	for _, g := range ov.Groups {
		t.Logf("group %q (%s): %v", g.Title, g.Summary, g.Files)
	}
	if len(ov.Walk.Steps) < 2 || len(ov.Walk.Checklist) == 0 || len(ov.Groups) == 0 {
		t.Errorf("walkthrough has %d steps, %d checklist answers, %d groups", len(ov.Walk.Steps), len(ov.Walk.Checklist), len(ov.Groups))
	}
	checkRefs(t, ctx, repo, scope, ov.Walk)

	w := rev.AddWalk("", 0, "")
	deeper := review.DeeperExchange(rev, w, cfg, stat)
	session := ov.Sessions[provider]
	if deeper.Request.ResumeID != session || !deeper.Request.Fork {
		t.Errorf("deeper request resumes %q (fork %v), want a fork of %q", deeper.Request.ResumeID, deeper.Request.Fork, session)
	}
	answer, forked := runAgent(t, ctx, cfg, deeper.Request)
	if session == "" || forked == "" || forked == session {
		t.Errorf("fork session = %q, overview session = %q", forked, session)
	}
	deeper.Started(forked)
	deeper.Answered(answer, time.Now())
	if w.Content == nil {
		t.Fatalf("no deeper walkthrough in:\n%s", answer)
	}
	logWalk(t, w.Content)
	checkRefs(t, ctx, repo, scope, w.Content)
}

func logWalk(t *testing.T, w *review.Walkthrough) {
	t.Logf("summary: %s", w.Summary)
	for i, s := range w.Steps {
		t.Logf("%d. %s: %s", i+1, s.Title, s.Text)
		for _, r := range s.Refs {
			t.Logf("   %s %s", r.Label(), r.Why)
		}
	}
	for _, c := range w.Checklist {
		t.Logf("? %s %s", c.Question, c.Answer)
	}
}

// checkRefs reports references to lines that do not exist on their side.
func checkRefs(t *testing.T, ctx context.Context, repo *gitx.Repo, scope gitx.Scope, w *review.Walkthrough) {
	t.Helper()
	snap, err := repo.Snapshot(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := snap.Changes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range w.Steps {
		for _, r := range s.Refs {
			var oldText, newText string
			if i := slices.IndexFunc(changes, func(c gitx.FileChange) bool { return c.Path == r.File }); i >= 0 {
				oldText, newText, err = snap.Contents(ctx, changes[i])
			} else {
				newText, err = snap.File(ctx, r.File)
			}
			text := newText
			if r.Side == review.SideOld {
				text = oldText
			}
			if n := len(strings.Split(strings.TrimSuffix(text, "\n"), "\n")); err != nil || r.End > n {
				t.Errorf("%s points past the file (%d lines, %v)", r.Label(), n, err)
			}
		}
	}
}

func runAgent(t *testing.T, ctx context.Context, cfg agent.Config, req agent.Request) (string, string) {
	t.Helper()
	var session, final string
	var last agent.LogEntry
	req.Log = func(e agent.LogEntry) { last = e }
	defer func() {
		if last.Kind != agent.LogExit || last.Text != "Exited with status 0" {
			t.Errorf("last log entry = %+v", last)
		}
	}()
	for ev := range agent.Run(ctx, cfg, req) {
		switch ev.Kind {
		case agent.EventSession:
			session = ev.SessionID
		case agent.EventActivity:
			t.Logf("activity: %s", ev.Text)
		case agent.EventDone:
			final = ev.Text
		case agent.EventError:
			t.Fatalf("agent: %v", ev.Err)
		case agent.EventText:
		}
	}
	return final, session
}
