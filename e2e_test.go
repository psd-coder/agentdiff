package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/psdcoder/agentdiff/internal/agent"
	"github.com/psdcoder/agentdiff/internal/gitx"
	"github.com/psdcoder/agentdiff/internal/review"
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
	commits, err := repo.Commits(ctx, "", 1)
	if err != nil || len(commits) == 0 {
		t.Fatalf("commits: %v", err)
	}
	scope := gitx.Scope{Kind: gitx.ScopeCommit, Commit: commits[0].SHA}
	files, err := repo.Changes(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	stat, err := repo.DiffStat(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Path
	}
	prompt := review.OverviewPrompt(scope.Describe(), stat, names, review.DefaultQuestions)
	final, session := runAgent(t, ctx, cfg, agent.Request{Dir: repo.Root, Prompt: prompt, SystemPrompt: review.SystemPrompt})
	body, groups := review.ParseOverview(final, names)
	t.Logf("overview:\n%s", body)
	for _, g := range groups {
		t.Logf("group %q (%s): %v", g.Title, g.Summary, g.Files)
	}
	if len(review.Sections(body)) < 3 || len(groups) == 0 {
		t.Errorf("overview has %d sections and %d groups", len(review.Sections(body)), len(groups))
	}
	th := &review.Thread{Anchor: review.Anchor{Kind: review.AnchorFile, File: names[0]}}
	th.Messages = []review.Message{{Role: review.RoleUser, Text: "In one sentence: what is the riskiest part of this file's change?"}}
	answer, forked := runAgent(t, ctx, cfg, agent.Request{Dir: repo.Root, Prompt: review.QuestionPrompt(th, th.Messages[0].Text, false), ResumeID: session, Fork: true})
	t.Logf("answer: %s", answer)
	if forked == "" || forked == session {
		t.Errorf("fork session = %q, overview session = %q", forked, session)
	}
}

func runAgent(t *testing.T, ctx context.Context, cfg agent.Config, req agent.Request) (string, string) {
	t.Helper()
	var session, final string
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
		}
	}
	return final, session
}
