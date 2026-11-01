package store

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
)

func TestConfigRoundTrip(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if got := s.LoadConfig(); got.Agent.Provider != agent.Claude || got.Prompts != nil {
		t.Fatalf("default config = %+v", got)
	}
	cfg := DefaultConfig()
	cfg.Agent = agent.Config{Provider: agent.Codex, Model: "gpt-5"}
	cfg.Prompts = review.Prompts{review.PromptSystem: "Be brief."}
	if err := s.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	got := s.LoadConfig()
	if got.Agent.Provider != agent.Codex || got.Agent.Model != "gpt-5" || got.Prompts[review.PromptSystem] != "Be brief." {
		t.Errorf("loaded config = %+v", got)
	}
}

func TestAutoEffortDefault(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := os.WriteFile(filepath.Join(s.Dir, "config.json"), []byte(`{"agent": {"provider": "claude"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := s.LoadConfig()
	if cfg.Agent.AutoEffort != "low" {
		t.Fatalf("an older config got auto effort %q", cfg.Agent.AutoEffort)
	}
	cfg.Agent.AutoEffort = ""
	if err := s.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if got := s.LoadConfig().Agent.AutoEffort; got != "" {
		t.Errorf("a cleared auto effort came back as %q", got)
	}
}

func TestLoadConfigCorrupt(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	path := filepath.Join(s.Dir, "config.json")
	if err := os.WriteFile(path, []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := s.LoadConfig(); got.Agent.Provider != agent.Claude {
		t.Errorf("corrupt config = %+v, want defaults", got)
	}
	if b, err := os.ReadFile(path + ".bad"); err != nil || string(b) != "{oops" {
		t.Errorf("config.json.bad = %q, %v", b, err)
	}
	if err := s.SaveConfig(DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path + ".bad"); string(b) != "{oops" {
		t.Errorf("saving the config changed config.json.bad to %q", b)
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
	c.RemoveRecent("c")
	if len(c.Recent) != maxRecent-1 || slices.Contains(c.Recent, "c") {
		t.Errorf("after remove: %v", c.Recent)
	}
}

func TestRecentKeepsTheOldSlice(t *testing.T) {
	c := Config{Recent: []string{"a", "b", "c"}}
	old := c.Recent
	c.RemoveRecent("a")
	c.AddRecent("c")
	if !slices.Equal(old, []string{"a", "b", "c"}) {
		t.Errorf("the old recent slice changed to %v", old)
	}
	if !slices.Equal(c.Recent, []string{"c", "b"}) {
		t.Errorf("recent = %v", c.Recent)
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

func TestLoadReviewCorrupt(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	scope := gitx.Scope{Kind: gitx.ScopeUncommitted}
	path := s.reviewPath("/repo", scope)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"threads": "a newer format"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := s.LoadReview("/repo", scope)
	if len(r.Threads) != 0 || r.Viewed == nil {
		t.Fatalf("unreadable review = %+v, want a new one", r)
	}
	if err := s.SaveReview(r); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path + ".bad"); err != nil || string(b) != `{"threads": "a newer format"}` {
		t.Errorf("review .bad file = %q, %v", b, err)
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

func writeRun(t *testing.T, s Store, scope gitx.Scope, info RunInfo, texts ...string) {
	t.Helper()
	w, err := s.CreateRun("/repo", scope, info)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range texts {
		if err := w.Write(agent.LogEntry{Kind: agent.LogStdout, Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRunsRoundTrip(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	scope := gitx.Scope{Kind: gitx.ScopeUncommitted}
	t0 := time.Date(2026, 10, 26, 14, 0, 0, 0, time.UTC)
	older := RunInfo{ID: NewRunID(t0), Target: TargetOverview, Title: "Overview", Agent: agent.Claude, Started: t0}
	newer := RunInfo{ID: NewRunID(t0.Add(time.Minute)), Target: "t1", Title: "a.go", Agent: agent.Codex, Started: t0.Add(time.Minute)}
	writeRun(t, s, scope, older, `{"type":"system"}`, "line two")
	writeRun(t, s, scope, newer)

	runs := s.Runs("/repo", scope)
	if len(runs) != 2 || runs[0].ID != newer.ID || runs[1].Target != TargetOverview || !runs[1].Started.Equal(t0) {
		t.Fatalf("runs = %+v", runs)
	}
	entries, err := s.ReadRun("/repo", scope, older.ID)
	if err != nil || len(entries) != 2 || entries[0].Text != `{"type":"system"}` || entries[1].Text != "line two" {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	if runs[0].Usage != nil {
		t.Errorf("usage before the run ended = %+v", runs[0].Usage)
	}
	u := agent.Usage{Input: 1, Output: 2, CacheRead: 3, CostUSD: 0.5}
	if err := s.SaveRunUsage("/repo", scope, newer.ID, u); err != nil {
		t.Fatal(err)
	}
	if got := s.Runs("/repo", scope); got[0].Usage == nil || *got[0].Usage != u || len(got) != 2 {
		t.Errorf("runs with usage = %+v", got)
	}
	if got := s.Runs("/repo", gitx.Scope{Kind: gitx.ScopeCommit, Commit: "abc"}); len(got) != 0 {
		t.Errorf("other scope runs = %+v", got)
	}
}

func TestReadRunSkipsCutLine(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	scope := gitx.Scope{Kind: gitx.ScopeUncommitted}
	info := RunInfo{ID: NewRunID(time.Now()), Started: time.Now()}
	writeRun(t, s, scope, info, "ok")
	f, err := os.OpenFile(s.RunPath("/repo", scope, info.ID), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"kind":"stdout","te`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := s.ReadRun("/repo", scope, info.ID)
	if err != nil || len(entries) != 1 {
		t.Errorf("entries = %+v, %v", entries, err)
	}
}

func TestRunsPrunedAndDeleted(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	scope := gitx.Scope{Kind: gitx.ScopeUncommitted}
	t0 := time.Date(2026, 10, 26, 14, 0, 0, 0, time.UTC)
	for i := range MaxRuns + 2 {
		at := t0.Add(time.Duration(i) * time.Second)
		writeRun(t, s, scope, RunInfo{ID: NewRunID(at), Started: at})
	}
	runs := s.Runs("/repo", scope)
	if len(runs) != MaxRuns || !runs[len(runs)-1].Started.Equal(t0.Add(2*time.Second)) {
		t.Fatalf("kept %d runs, oldest %v", len(runs), runs[len(runs)-1].Started)
	}
	if err := s.DeleteReview("/repo", scope); err != nil {
		t.Fatal(err)
	}
	if got := s.Runs("/repo", scope); len(got) != 0 {
		t.Errorf("runs after delete = %d", len(got))
	}
}

func TestSavedQuestionsMoveIntoTheOverviewPrompt(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   review.Prompts
	}{
		{"puts rewritten questions into the default overview prompt", `{"questions": "1. Is it fast?\n"}`, review.Prompts{review.PromptOverview: review.DefaultOverview("1. Is it fast?")}},
		{"drops the default questions", `{"questions": "1. What data changes: schemas, storage, formats?\n2. What UI changes, and how do I see them?\n3. What architecture and API changes?\n4. Which changes are not beautiful?\n5. Which assumptions and rules force the non-beautiful changes?"}`, nil},
		{"drops the questions of the first versions", `{"questions": "Explain the changes:\n\n1. Explain on high level.\n2. Explain data changes.\n3. Explain UI changes and how to see them.\n4. Explain architecture and API changes.\n5. Explain non-beautiful changes.\n6. Tell about assumptions and rules which force non-beautiful changes."}`, nil},
		{"keeps an overview prompt the user already rewrote", `{"questions": "1. Is it fast?", "prompts": {"overview": "Mine {{format}} {{files}}"}}`, review.Prompts{review.PromptOverview: "Mine {{format}} {{files}}"}},
		{"drops empty questions", `{"questions": " "}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Store{Dir: t.TempDir()}
			if err := os.WriteFile(filepath.Join(s.Dir, "config.json"), []byte(tt.config), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg := s.LoadConfig()
			if !reflect.DeepEqual(cfg.Prompts, tt.want) {
				t.Errorf("prompts = %q, want %q", cfg.Prompts, tt.want)
			}
			if err := s.SaveConfig(cfg); err != nil {
				t.Fatal(err)
			}
			saved, err := os.ReadFile(filepath.Join(s.Dir, "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(saved), `"questions"`) {
				t.Errorf("the saved config still holds the questions: %s", saved)
			}
		})
	}
}

func TestFindMoveCopyReview(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	uncommitted := gitx.Scope{Kind: gitx.ScopeUncommitted}
	commit := gitx.Scope{Kind: gitx.ScopeCommit, Commit: "abc"}
	amended := gitx.Scope{Kind: gitx.ScopeCommit, Commit: "def"}
	r := review.New("/repo", uncommitted)
	r.Overview = &review.Overview{Markdown: "x", PatchID: "p1"}
	if err := s.SaveReview(r); err != nil {
		t.Fatal(err)
	}
	w, err := s.CreateRun("/repo", uncommitted, RunInfo{ID: "run1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.FindReview("/repo", commit, "p2"); ok {
		t.Error("found a review for other changes")
	}
	if _, ok := s.FindReview("/repo", uncommitted, "p1"); ok {
		t.Error("found the review of the scope itself")
	}
	if src, ok := s.FindReview("/repo", commit, "p1"); !ok || src.Scope != uncommitted {
		t.Fatalf("found %+v, %v", src, ok)
	}
	if err := s.MoveReview("/repo", uncommitted, commit); err != nil {
		t.Fatal(err)
	}
	if s.LoadReview("/repo", uncommitted).Overview != nil || s.LoadReview("/repo", commit).Overview == nil {
		t.Error("the review did not move")
	}
	if err := s.CopyReview("/repo", commit, amended); err != nil {
		t.Fatal(err)
	}
	if s.LoadReview("/repo", commit).Overview == nil || len(s.Runs("/repo", amended)) != 1 {
		t.Error("the copy lost the review or its runs")
	}
	// The same changes again, as a cherry-pick: the review comes from a
	// commit, which keeps it, and the uncommitted changes stay alone.
	if src, ok := s.FindReview("/repo", uncommitted, "p1"); !ok || src.Scope.Kind != gitx.ScopeCommit {
		t.Errorf("found %+v, %v; a moved review must name its new scope", src, ok)
	}
}

func TestArchiveReview(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	uncommitted := gitx.Scope{Kind: gitx.ScopeUncommitted}
	if len(s.Archived("/repo")) != 0 {
		t.Error("an archive without reviews")
	}
	for i := range maxArchived + 2 {
		r := review.New("/repo", uncommitted)
		r.Overview = &review.Overview{Markdown: "x", PatchID: fmt.Sprint("p", i)}
		if err := s.SaveReview(r); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ArchiveReview("/repo", r.Overview.PatchID); err != nil {
			t.Fatal(err)
		}
		if err := s.PruneArchive("/repo"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if s.LoadReview("/repo", uncommitted).Overview != nil {
		t.Error("the review stayed")
	}
	archived := s.Archived("/repo")
	if n := len(archived); n != maxArchived {
		t.Fatalf("kept %d archived reviews, want %d", n, maxArchived)
	}
	if latest := archived[0]; latest.Overview.PatchID != fmt.Sprint("p", maxArchived+1) || latest.Scope.Kind != gitx.ScopeArchived {
		t.Errorf("latest = %+v", latest)
	}
	if src, ok := s.FindReview("/repo", uncommitted, "p5"); !ok || src.Scope.Kind != gitx.ScopeArchived {
		t.Errorf("found %+v, %v", src, ok)
	}
	if _, ok := s.FindReview("/repo", uncommitted, "p0"); ok {
		t.Error("the oldest archived review is still there")
	}
}

func TestArchivedReturnsTheReviewItRead(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	// The file of archive "a" holds the review of archive "b", whose own
	// file is missing, as when it is deleted while the archive is listed.
	r := review.New("/elsewhere", gitx.Scope{Kind: gitx.ScopeArchived, From: "b"})
	r.Overview = &review.Overview{Markdown: "x", PatchID: "p1"}
	r.Viewed = nil
	if err := writeJSON(s.reviewPath("/repo", gitx.Scope{Kind: gitx.ScopeArchived, From: "a"}), r); err != nil {
		t.Fatal(err)
	}
	archived := s.Archived("/repo")
	if len(archived) != 1 {
		t.Fatalf("archived = %+v", archived)
	}
	got := archived[0]
	if got.Overview == nil || got.Overview.PatchID != "p1" || got.Repo != "/repo" || got.Viewed == nil {
		t.Errorf("archived review = %+v", got)
	}
}

func TestRunResumedFromCommand(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	scope := gitx.Scope{Kind: gitx.ScopeUncommitted}
	w, err := s.CreateRun("/repo", scope, RunInfo{ID: "old", Agent: agent.Claude})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(agent.LogEntry{Kind: agent.LogCommand, Text: "claude -p --resume 2b02f0c1-3491-4bdd-9e05-ee7282cd2510 --fork-session"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	runs := s.Runs("/repo", scope)
	if len(runs) != 1 || runs[0].Resumed != "2b02f0c1-3491-4bdd-9e05-ee7282cd2510" {
		t.Errorf("runs = %+v", runs)
	}
}
