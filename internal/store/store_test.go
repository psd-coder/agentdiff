package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/psdcoder/agentdiff/internal/agent"
	"github.com/psdcoder/agentdiff/internal/gitx"
	"github.com/psdcoder/agentdiff/internal/review"
)

func TestConfigRoundTrip(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if got := s.LoadConfig(); got.Agent.Provider != agent.Claude || got.Questions != review.DefaultQuestions {
		t.Fatalf("default config = %+v", got)
	}
	cfg := DefaultConfig()
	cfg.Agent = agent.Config{Provider: agent.Codex, Model: "gpt-5"}
	cfg.Questions = "Q"
	if err := s.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	got := s.LoadConfig()
	if got.Agent.Provider != agent.Codex || got.Agent.Model != "gpt-5" || got.Questions != "Q" {
		t.Errorf("loaded config = %+v", got)
	}
}

func TestLoadConfigCorrupt(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := os.WriteFile(filepath.Join(s.Dir, "config.json"), []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := s.LoadConfig(); got.Agent.Provider != agent.Claude {
		t.Errorf("corrupt config = %+v, want defaults", got)
	}
}

func TestRecent(t *testing.T) {
	var c Config
	for i := range maxRecent + 3 {
		c.AddRecent(string(rune('a' + i)))
	}
	c.AddRecent("c")
	if len(c.Recent) != maxRecent || c.Recent[0] != "c" {
		t.Errorf("recent = %v", c.Recent)
	}
	seen := map[string]bool{}
	for _, r := range c.Recent {
		if seen[r] {
			t.Errorf("duplicate %q in %v", r, c.Recent)
		}
		seen[r] = true
	}
}

func TestEffective(t *testing.T) {
	c := Config{Agent: agent.Config{Provider: agent.Claude}, Questions: "global"}
	a, q := c.Effective(review.Settings{})
	if a.Provider != agent.Claude || q != "global" {
		t.Errorf("no override: %v %q", a, q)
	}
	own := "mine"
	a, q = c.Effective(review.Settings{Agent: &agent.Config{Provider: agent.Codex}, Questions: &own})
	if a.Provider != agent.Codex || q != "mine" {
		t.Errorf("override: %v %q", a, q)
	}
}

func TestReviewRoundTrip(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	scope := gitx.Scope{Kind: gitx.ScopeBranch, Base: "main"}
	r := s.LoadReview("/repo", scope)
	if len(r.Threads) != 0 || r.Viewed == nil {
		t.Fatalf("new review = %+v", r)
	}
	th := r.AddThread(review.Anchor{Kind: review.AnchorFile, File: "a.go"})
	th.Messages = []review.Message{{Role: review.RoleUser, Text: "hi"}}
	r.Viewed["a.go"] = true
	if err := s.SaveReview(r); err != nil {
		t.Fatal(err)
	}
	got := s.LoadReview("/repo", scope)
	if len(got.Threads) != 1 || got.Threads[0].Messages[0].Text != "hi" || !got.Viewed["a.go"] {
		t.Errorf("loaded review = %+v", got)
	}
	other := s.LoadReview("/other", scope)
	if len(other.Threads) != 0 {
		t.Error("reviews of different repos share a file")
	}
}

func TestDeleteReview(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	scope := gitx.Scope{Kind: gitx.ScopeUncommitted}
	r := s.LoadReview("/repo", scope)
	r.AddThread(review.Anchor{Kind: review.AnchorGeneral})
	if err := s.SaveReview(r); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteReview("/repo", scope); err != nil {
		t.Fatal(err)
	}
	if got := s.LoadReview("/repo", scope); len(got.Threads) != 0 {
		t.Errorf("threads after delete = %d", len(got.Threads))
	}
	if err := s.DeleteReview("/repo", scope); err != nil {
		t.Errorf("deleting a missing review: %v", err)
	}
}
