package main

import (
	"encoding/json"
	"fmt"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/psdcoder/agentdiff/internal/agent"
	"github.com/psdcoder/agentdiff/internal/gitx"
	"github.com/psdcoder/agentdiff/internal/review"
	"github.com/psdcoder/agentdiff/internal/store"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@e")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixtureRepo is a repository with a commit and uncommitted changes in
// two directories.
func fixtureRepo(t *testing.T) string {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "cache/cache.go", "package cache\n\nfunc Get(key string) string {\n\treturn key\n}\n")
	write(t, dir, "README.md", "# Demo\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "Initial commit")
	write(t, dir, "cache/cache.go", "package cache\n\nimport \"strings\"\n\nfunc Get(key string) string {\n\treturn strings.ToLower(key)\n}\n")
	write(t, dir, "api/handler.go", "package api\n\nfunc Handle() error {\n\treturn nil\n}\n")
	return dir
}

func newTestApp(t *testing.T) *app {
	t.Helper()
	a := newApp(store.Store{Dir: t.TempDir()})
	a.openRepo(fixtureRepo(t))
	if a.repo == nil {
		t.Fatalf("open: %s", a.repoErr)
	}
	return a
}

func snapshot(t *testing.T, tt *ui.Tester, name string) {
	dir := os.Getenv("AGENTDIFF_SNAPSHOTS")
	if dir == "" {
		return
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestOpensRepoWithChanges(t *testing.T) {
	a := newTestApp(t)
	if len(a.files) != 2 {
		t.Fatalf("files = %+v", a.files)
	}
	tt := ui.NewTester(a.view, 1300, 800)
	for _, want := range []string{"Overview", "Uncommitted", "Initial commit", "cache.go", "handler.go", "Explain changes with Claude Code"} {
		if !strings.Contains(strings.Join(tt.Texts(), "\n"), want) {
			t.Errorf("view lacks %q; texts %q", want, tt.Texts())
		}
	}
	snapshot(t, tt, "overview-empty")
}

func TestOverviewAndGroups(t *testing.T) {
	a := newTestApp(t)
	md := "## 1. High level\n- Keys are lower-cased in `cache/cache.go:6`.\n\n## 2. Data changes\nNone.\n\n```json\n{\"groups\":[{\"title\":\"Cache keys\",\"summary\":\"Lower-case keys.\",\"files\":[\"cache/cache.go\"]},{\"title\":\"API\",\"summary\":\"New handler.\",\"files\":[\"api/handler.go\"]}]}\n```"
	body, groups := review.ParseOverview(md, a.fileNames())
	a.rev.Overview = &review.Overview{Markdown: body, Groups: groups, Agent: agent.Claude, Generated: time.Now(), SessionID: "s"}
	tt := ui.NewTester(a.view, 1300, 900)
	tt.Frame()
	for _, want := range []string{"1. High level", "2. Data changes", "Cache keys", "Review order", "Lower-case keys."} {
		if !tt.HasText(want) {
			t.Errorf("view lacks %q", want)
		}
	}
	snapshot(t, tt, "overview")
	if err := tt.Click("Review group"); err != nil {
		t.Fatal(err)
	}
	if a.page != "group:0" {
		t.Errorf("page = %q", a.page)
	}
	tt.Advance(time.Second)
	snapshot(t, tt, "group")
	if l := a.diffs["cache/cache.go"].state.Layout; l != editor.DiffSplit {
		t.Errorf("group diff layout = %v, want split", l)
	}
	if err := tt.Click("Unified"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if l := a.diffs["cache/cache.go"].state.Layout; l != editor.DiffUnified || !a.cfg.Unified {
		t.Errorf("after Unified: layout %v, config %v", l, a.cfg.Unified)
	}
}

func TestCommentOnFileAndExport(t *testing.T) {
	a := newTestApp(t)
	a.page = "file:cache/cache.go"
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	if err := tt.Click("Comment on file"); err != nil {
		t.Fatal(err)
	}
	if a.thread == "" {
		t.Fatal("no thread opened")
	}
	tt.Type("Keep keys case-sensitive.")
	if err := tt.Click("Comment"); err != nil {
		t.Fatal(err)
	}
	th := a.rev.Thread(a.thread)
	if th == nil || len(th.Messages) != 1 || th.Messages[0].Text != "Keep keys case-sensitive." {
		t.Fatalf("thread = %+v", th)
	}
	snapshot(t, tt, "file-thread")
	got := a.feedback()
	if !strings.Contains(got, "## cache/cache.go (file-level)\nKeep keys case-sensitive.") {
		t.Errorf("feedback =\n%s", got)
	}
	a.page = "feedback"
	tt.Frame()
	if err := tt.Click("Copy feedback"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tt.Clipboard(), "Keep keys case-sensitive.") {
		t.Errorf("clipboard = %q", tt.Clipboard())
	}
	snapshot(t, tt, "feedback")
	reloaded := a.store.LoadReview(a.repo.Root, a.scope)
	if len(reloaded.Threads) != 1 {
		t.Errorf("saved threads = %d", len(reloaded.Threads))
	}
}

func TestLineThreadAnnotation(t *testing.T) {
	a := newTestApp(t)
	a.page = "file:cache/cache.go"
	a.lineThread("cache/cache.go", lineRange(5, 5))
	th := a.rev.Thread(a.thread)
	if th.Anchor.Quote != "\treturn strings.ToLower(key)" || th.Anchor.Label() != "cache/cache.go:6 (+)" {
		t.Errorf("anchor = %+v", th.Anchor)
	}
	a.comment(th, "Why lower-case?")
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	if !tt.HasText("Why lower-case?") {
		t.Error("annotation card missing")
	}
	snapshot(t, tt, "file-line-thread")
	a.closeThread()
	tt.Frame()
	if len(a.rev.Threads) != 1 {
		t.Error("closing a thread with messages dropped it")
	}
}

func TestEmptyThreadDroppedOnClose(t *testing.T) {
	a := newTestApp(t)
	a.newThread(review.Anchor{Kind: review.AnchorGeneral})
	a.closeThread()
	if len(a.rev.Threads) != 0 {
		t.Errorf("threads = %d, want 0", len(a.rev.Threads))
	}
}

func TestScopeSwitchToCommit(t *testing.T) {
	a := newTestApp(t)
	if len(a.commits) != 1 {
		t.Fatalf("commits = %+v", a.commits)
	}
	tt := ui.NewTester(a.view, 1300, 800)
	if err := tt.Click("Initial commit"); err != nil {
		t.Fatal(err)
	}
	if a.scope.Commit != a.commits[0].SHA || len(a.files) != 2 {
		t.Errorf("scope = %+v, files = %+v", a.scope, a.files)
	}
}

func TestSettings(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1300, 800)
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	snapshot(t, tt, "settings")
	title, _ := tt.Find("Shortcuts")
	if tt.HasText("This review") {
		t.Errorf("per-review tab: %q", tt.Texts())
	}
	a.settings.agent.provider = 1
	a.settings.agent.model = "gpt-5"
	a.settings.ahead = false
	tt.Frame()
	if got := a.store.LoadConfig(); got.Agent.Provider != agent.Codex || got.Agent.Model != "gpt-5" || got.DeeperAhead {
		t.Errorf("not saved as changed: %+v", got)
	}

	// The tabs start at the same place, and each is as tall as it needs.
	if err := tt.Click("Shortcuts"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	snapshot(t, tt, "settings-shortcuts")
	if tabs, _ := tt.Find("Shortcuts"); tabs.Y != title.Y {
		t.Errorf("the top moved with the tab: %v, then %v", title, tabs)
	}
	a.keysFilter = "refresh"
	tt.Frame()
	done, _ := tt.Find("Done")
	if reset, _ := tt.Find("Reset all shortcuts"); done.Y-reset.Y > 80 {
		t.Errorf("room under the short list: Reset all at %v, Done at %v", reset, done)
	}

	if err := tt.Click("Done"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settings != nil {
		t.Error("Done left the settings open")
	}
}

func lineRange(start, end int) editor.DiffLineRange {
	return editor.DiffLineRange{Side: editor.DiffNew, Start: start, End: end}
}

// TestReviewFollowsCommit writes an overview of uncommitted changes and
// commits them: the review then belongs to the commit.
func TestReviewFollowsCommit(t *testing.T) {
	a := newTestApp(t)
	dir := a.repo.Root
	answer := "```json\n" + `{"summary": "s", "steps": [{"title": "A", "text": "a"}], "groups": []}` + "\n```"
	a.cfg.Agent = agent.Config{Provider: agent.Claude, Binary: fakeClaude(t, "s1", answer)}
	a.cfg.DeeperAhead = false
	a.queue = make(chan func(), 64)
	a.generateOverview()
	pump(t, a, "the overview", func() bool { return a.overviewRunning() == nil })
	rev := a.rev
	if rev.Overview.PatchID == "" || rev.Overview.PatchID != a.patch {
		t.Fatalf("overview patch %q, scope patch %q", rev.Overview.PatchID, a.patch)
	}

	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "Agent work")
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(out))
	a.refresh()
	pump(t, a, "the reload", func() bool { return !a.loading && !a.switching })
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	if a.movedTo != sha || a.archived == nil || !a.rev.Empty() || !tt.HasText("Earlier review") || !tt.HasText("Open commit") {
		t.Fatalf("moved to %q, want %q; texts %q", a.movedTo, sha, tt.Texts())
	}

	if err := tt.Click("Open commit"); err != nil {
		t.Fatal(err)
	}
	pump(t, a, "the commit", func() bool { return !a.loading && !a.switching })
	if a.scope.Commit != sha || a.rev.Overview == nil || a.rev.Overview.PatchID != rev.Overview.PatchID || a.stale() {
		t.Fatalf("scope %+v, review %+v, stale %v", a.scope, a.rev.Overview, a.stale())
	}
	tt.Frame()
	if !tt.HasText("Moved here from earlier uncommitted changes, which had the same changes.") {
		t.Errorf("texts %q", tt.Texts())
	}
	a.setScope(gitx.Scope{Kind: gitx.ScopeUncommitted})
	pump(t, a, "the uncommitted changes", func() bool { return !a.loading && !a.switching })
	if !a.rev.Empty() || a.archived != nil {
		t.Errorf("the uncommitted scope kept the review: %+v, archived %+v", a.rev, a.archived)
	}
}

// TestReviewToBiggerCommit commits the reviewed changes together with
// another file: the review is offered to that commit.
func TestReviewToBiggerCommit(t *testing.T) {
	a := newTestApp(t)
	dir := a.repo.Root
	answer := "```json\n" + `{"summary": "s", "steps": [{"title": "A", "text": "a"}], "groups": []}` + "\n```"
	a.cfg.Agent = agent.Config{Provider: agent.Claude, Binary: fakeClaude(t, "s1", answer)}
	a.cfg.DeeperAhead = false
	a.queue = make(chan func(), 64)
	a.generateOverview()
	pump(t, a, "the overview", func() bool { return a.overviewRunning() == nil })

	write(t, dir, "extra.go", "package extra\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "Agent work and more")
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(out))
	a.refresh()
	pump(t, a, "the reload", func() bool { return !a.loading && !a.switching })
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	if a.archived == nil || a.movedTo != "" || a.probably != sha {
		t.Fatalf("archived %v, moved to %q, probably %q, want %q", a.archived != nil, a.movedTo, a.probably, sha)
	}
	if err := tt.Click("Move review to the commit"); err != nil {
		t.Fatal(err)
	}
	pump(t, a, "the commit", func() bool { return !a.loading && !a.switching })
	if a.scope.Commit != sha || a.rev.Overview == nil || !a.stale() {
		t.Fatalf("scope %+v, review %+v, stale %v", a.scope, a.rev.Overview, a.stale())
	}
	tt.Frame()
	if !tt.HasText("This overview was written for 2 files. Since then, 1 new file is not in it. Its code snippets can show the wrong lines. Regenerate the overview, or start over.") {
		t.Errorf("texts %q", tt.Texts())
	}
}

// TestArchiveUncommitted replaces the uncommitted changes with other
// ones: their review goes to the archive, and comes back with them.
func TestArchiveUncommitted(t *testing.T) {
	a := newTestApp(t)
	dir := a.repo.Root
	answer := "```json\n" + `{"summary": "Lower-case keys.", "steps": [{"title": "A", "text": "a"}], "groups": []}` + "\n```"
	a.cfg.Agent = agent.Config{Provider: agent.Claude, Binary: fakeClaude(t, "s1", answer)}
	a.cfg.DeeperAhead = false
	a.queue = make(chan func(), 64)
	a.generateOverview()
	pump(t, a, "the overview", func() bool { return a.overviewRunning() == nil })
	if len(a.rev.Overview.Files) != 2 {
		t.Fatalf("overview files = %v", a.rev.Overview.Files)
	}
	reload := func(what string) *ui.Tester {
		t.Helper()
		a.refresh()
		pump(t, a, what, func() bool { return !a.loading && !a.switching })
		tt := ui.NewTester(a.view, 1300, 800)
		tt.Frame()
		return tt
	}

	git(t, dir, "stash", "-u", "-q")
	write(t, dir, "output.json", "{}\n")
	a.overviews[a.rev] = &overviewRun{cancel: func() {}}
	reload("the busy reload")
	if a.rev.Overview == nil {
		t.Fatal("archived a review an agent writes into")
	}
	delete(a.overviews, a.rev)
	tt := reload("the other changes")
	if !a.rev.Empty() || a.archived == nil || !tt.HasText("Earlier review") || !tt.HasText("Lower-case keys.") {
		t.Fatalf("review %+v, archived %v; texts %q", a.rev, a.archived != nil, tt.Texts())
	}
	snapshot(t, tt, "archived")

	if err := os.Remove(filepath.Join(dir, "output.json")); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "stash", "pop", "-q")
	tt = reload("the changes back")
	if a.rev.Overview == nil || a.stale() || a.archived != nil || !tt.HasText("Moved here from earlier uncommitted changes") {
		t.Fatalf("review %+v, stale %v; texts %q", a.rev.Overview, a.stale(), tt.Texts())
	}

	git(t, dir, "stash", "-u", "-q")
	write(t, dir, "output.json", "{}\n")
	reload("the other changes again")
	if a.archived == nil {
		t.Fatal("not archived")
	}
	a.restoreArchived()
	tt = reload("the restored review")
	if a.rev.Overview == nil || a.archived != nil || !a.stale() || !tt.HasText("This overview was written for 2 files. Since then, 2 are no longer in the changes and 1 new file is not in it. Its code snippets can show the wrong lines. Regenerate the overview, or start over.") {
		t.Fatalf("review %+v, stale %v; texts %q", a.rev.Overview, a.stale(), tt.Texts())
	}

	write(t, dir, "cache/cache.go", "package cache\n")
	tt = reload("a file of the review")
	if !tt.HasText("This overview was written for 2 files. Since then, 1 changed, 1 is no longer in the changes and 1 new file is not in it. Its code snippets can show the wrong lines. Regenerate the overview, or start over.") {
		t.Errorf("texts %q", tt.Texts())
	}
}

func TestStaleOverviewAndStartOver(t *testing.T) {
	a := newTestApp(t)
	a.rev.Overview = &review.Overview{Markdown: "## A\nx", Groups: []review.Group{{Title: "Old", Files: []string{"gone.go"}}}, Agent: agent.Claude}
	a.comment(a.rev.AddThread(review.Anchor{Kind: review.AnchorGeneral}), "old note")
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	if !tt.HasText("This overview was written for 1 file. Since then, 1 is no longer in the changes and 2 new files are not in it. Its code snippets can show the wrong lines. Regenerate the overview, or start over.") {
		t.Errorf("stale warning missing: %q", tt.Texts())
	}
	a.startOver()
	tt.Frame()
	if a.rev.Overview != nil || len(a.rev.Threads) != 0 {
		t.Errorf("review after start over = %+v", a.rev)
	}
	if got := a.store.LoadReview(a.repo.Root, a.scope); len(got.Threads) != 0 {
		t.Error("saved review survived start over")
	}
}

// TestAgentLog runs a fake Claude that replays a recorded session, then
// reads the run's log in a new app, as after a restart.
func TestAgentLog(t *testing.T) {
	a := newTestApp(t)
	fixture, err := filepath.Abs("internal/agent/testdata/claude_tool.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "claude")
	write(t, filepath.Dir(bin), "claude", "#!/bin/sh\ncat > /dev/null\ncat "+fixture+"\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	a.cfg.Agent = agent.Config{Provider: agent.Claude, Binary: bin}
	a.generateOverview()
	waitForExit(t, a.store, a.repo.Root, a.scope)

	b := newApp(a.store)
	b.openRepo(a.repo.Root)
	tt := ui.NewTester(b.view, 1400, 900)
	if err := tt.Click("Agent log"); err != nil {
		t.Fatal(err)
	}
	var label string
	for _, s := range tt.Texts() {
		if strings.HasPrefix(s, "Overview · Claude Code · ") {
			label = s
		}
	}
	if label == "" {
		t.Fatalf("no overview run in %q", tt.Texts())
	}
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	for _, want := range []string{"Command", "Prompt", "Session started", "Tool call", "Tool result", "Result: success", "Exited with status 0"} {
		if !tt.HasText(want) {
			t.Errorf("log lacks %q", want)
		}
	}
	snapshot(t, tt, "log")
}

func waitForExit(t *testing.T, s store.Store, repo string, scope gitx.Scope) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runs := s.Runs(repo, scope); len(runs) == 1 {
			entries, _ := s.ReadRun(repo, scope, runs[0].ID)
			if n := len(entries); n > 0 && entries[n-1].Kind == agent.LogExit {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the run did not finish")
}

func TestScopeSwitchKeepsReview(t *testing.T) {
	a := newTestApp(t)
	rev := a.rev
	a.setScope(gitx.Scope{Kind: gitx.ScopeCommit, Commit: "HEAD"})
	a.setScope(gitx.Scope{Kind: gitx.ScopeUncommitted})
	if a.rev != rev {
		t.Error("switching back loaded a second copy of the review")
	}
}

func TestCompareBranches(t *testing.T) {
	a := newTestApp(t)
	dir := a.repo.Root
	git(t, dir, "stash", "-u", "-q")
	git(t, dir, "checkout", "-q", "-b", "agent")
	write(t, dir, "feature.go", "package feature\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "Add feature")
	git(t, dir, "checkout", "-q", "main")
	a.openRepo(dir)

	tt := ui.NewTester(a.view, 1300, 800)
	if err := tt.Click("Branch"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	snapshot(t, tt, "branches")
	if err := tt.Click("agent"); err != nil {
		t.Fatal(err)
	}
	if a.scope.Head != "agent" || a.scope.Base != "main" {
		t.Fatalf("scope = %+v", a.scope)
	}
	if len(a.files) != 1 || a.files[0].Path != "feature.go" {
		t.Errorf("files = %+v", a.files)
	}
	if len(a.commits) != 1 || a.commits[0].Subject != "Add feature" {
		t.Errorf("commits = %+v", a.commits)
	}
	tt.Frame()
	if !tt.HasText("Commits on agent") || !tt.HasText("All changes") || tt.HasText("Uncommitted changes") {
		t.Errorf("texts = %q", tt.Texts())
	}
	snapshot(t, tt, "branch")

	git(t, dir, "update-ref", "refs/remotes/origin/main", "main")
	if err := tt.Click("Compare with another base, or review a custom range"); err != nil {
		t.Fatal(err)
	}
	a.compareForm.branch, a.compareForm.base = "main", "origin/main"
	tt.Frame()
	if err := tt.Click("Show changes"); err != nil {
		t.Fatal(err)
	}
	if a.scope.Branch != "main" || a.scope.Base != "origin/main" {
		t.Fatalf("scope = %+v", a.scope)
	}
	tt.Frame()
	if !tt.HasText("vs origin/main") || !tt.HasText("Uncommitted changes") {
		t.Errorf("texts = %q", tt.Texts())
	}

	a.pickBranch("main")
	if a.scope.Kind != gitx.ScopeBranch || a.scope.Base != "origin/main" {
		t.Errorf("picked base is forgotten: scope = %+v", a.scope)
	}
	a.base, a.baseSet = "main", false
	a.pickBranch("main")
	if a.scope.Kind != gitx.ScopeUncommitted {
		t.Errorf("main against itself: scope = %+v", a.scope)
	}
	a.base = "agent"
	a.pickBranch("agent")
	if a.scope.Kind != gitx.ScopeCommit || len(a.commits) == 0 || a.scope.Commit != a.commits[0].SHA {
		t.Errorf("other branch against itself: scope = %+v", a.scope)
	}
}

func TestCustomRange(t *testing.T) {
	a := newTestApp(t)
	dir := a.repo.Root
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "Second")
	a.openRepo(dir)
	a.openCompare()
	a.compareForm.mode, a.compareForm.from, a.compareForm.to = compareRange, "HEAD~1", "HEAD"
	tt := ui.NewTester(a.view, 1300, 800)
	if err := tt.Click("Show changes"); err != nil {
		t.Fatal(err)
	}
	if a.scope.Kind != gitx.ScopeRange || len(a.files) != 2 {
		t.Fatalf("scope = %+v, files = %+v", a.scope, a.files)
	}
	if len(a.commits) != 1 || a.commits[0].Subject != "Second" {
		t.Errorf("commits = %+v", a.commits)
	}
	tt.Frame()
	if !tt.HasText("Commits in HEAD~1..HEAD") || tt.HasText("Uncommitted changes") {
		t.Errorf("texts = %q", tt.Texts())
	}
	a.pickBranch("main")
	if a.rng != nil || a.scope.Kind != gitx.ScopeUncommitted {
		t.Errorf("picking a branch kept the range: scope = %+v", a.scope)
	}
}

func TestRemoveProject(t *testing.T) {
	a := newTestApp(t)
	other := fixtureRepo(t)
	a.cfg.AddRecent(other)
	current := a.repo.Root
	a.openRepo(current)
	a.projectsOpen = true
	tt := ui.NewTester(a.view, 1300, 800)
	snapshot(t, tt, "projects")
	r, ok := tt.Find(filepath.Base(other))
	if !ok {
		t.Fatalf("no row for %s: %q", other, tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if err := tt.Click("Remove from the list"); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(a.cfg.Recent, other) || a.repo == nil {
		t.Fatalf("recent = %v, repo = %v", a.cfg.Recent, a.repo)
	}
	a.confirmRemoveProject(current)
	if len(a.cfg.Recent) != 0 || a.repo != nil {
		t.Fatalf("recent = %v, repo = %v", a.cfg.Recent, a.repo)
	}
	if !slices.Equal(a.store.LoadConfig().Recent, a.cfg.Recent) {
		t.Error("the list is not saved")
	}
	tt.Frame()
	if !tt.HasText("Open repository…") {
		t.Errorf("no welcome screen: %q", tt.Texts())
	}
}

func TestRunCost(t *testing.T) {
	a := newTestApp(t)
	parent := &logRun{info: store.RunInfo{ID: "p", Usage: &agent.Usage{Session: "p", SessionCost: 0.5, CostUSD: 0.5}}}
	a.runs[a.rev] = []*logRun{parent}
	for _, tc := range []struct {
		resumed string
		want    float64
	}{{"", 0.6}, {"p", 0.1}, {"gone", 0}} {
		run := &logRun{info: store.RunInfo{Usage: &agent.Usage{Session: "c", SessionCost: 0.6}}}
		if got := a.runCost(a.rev, run, tc.resumed); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("resumed %q: cost %v, want %v", tc.resumed, got, tc.want)
		}
	}
}

func TestReviewFollowsBranch(t *testing.T) {
	a := newTestApp(t)
	dir := a.repo.Root
	git(t, dir, "stash", "-u", "-q")
	git(t, dir, "checkout", "-q", "-b", "agent")
	write(t, dir, "feature.go", "package feature\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "Add feature")
	a.openRepo(dir)
	a.base = "main"
	s, ok := a.branchScope()
	if !ok {
		t.Fatal("no branch scope")
	}
	a.setScope(s)
	rev := a.rev

	git(t, dir, "checkout", "-q", "main")
	a.refresh()
	if a.branch != "main" || a.scope.Head != "agent" || a.rev != rev {
		t.Fatalf("after checkout of main: branch %q, scope %+v, same review %v", a.branch, a.scope, a.rev == rev)
	}
	if len(a.files) != 1 || a.files[0].Path != "feature.go" {
		t.Errorf("files = %+v", a.files)
	}

	a.setScope(gitx.Scope{Kind: gitx.ScopeUncommitted})
	a.compare("agent", "main")
	if a.rev != rev {
		t.Error("Compare opened another review of the same branch")
	}

	git(t, dir, "checkout", "-q", "agent")
	a.refresh()
	if a.scope.Branch != "agent" || a.scope.Head != "" || a.rev != rev || a.rev.Scope != a.scope {
		t.Fatalf("after checkout of agent: scope %+v, same review %v", a.scope, a.rev == rev)
	}
}

// fakeClaude is a claude CLI that answers every prompt with answer, in
// session id.
func fakeClaude(t *testing.T, id, answer string) string {
	t.Helper()
	dir := t.TempDir()
	init := `{"type":"system","subtype":"init","session_id":"` + id + `"}`
	step := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"a.go"}}],"usage":{"input_tokens":5,"cache_read_input_tokens":20000,"cache_creation_input_tokens":3000,"output_tokens":995}}}`
	result, err := json.Marshal(map[string]any{
		"type": "result", "subtype": "success", "result": answer, "session_id": id, "total_cost_usd": 0.25,
		"usage":      map[string]int{"input_tokens": 10, "output_tokens": 1000, "cache_read_input_tokens": 20000, "cache_creation_input_tokens": 5000},
		"modelUsage": map[string]any{"m": map[string]int{"inputTokens": 10, "outputTokens": 1000, "cacheReadInputTokens": 20000, "cacheCreationInputTokens": 5000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "out.jsonl", init+"\n"+step+"\n"+string(result)+"\n")
	write(t, dir, "claude", "#!/bin/sh\ncat > "+filepath.Join(dir, "prompt.txt")+"\ncat "+filepath.Join(dir, "out.jsonl")+"\n")
	if err := os.Chmod(filepath.Join(dir, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "claude")
}

// pump runs the app's queued updates on the test goroutine until ok.
func pump(t *testing.T, a *app, what string, ok func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !ok() {
		select {
		case fn := <-a.queue:
			fn()
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func TestWalkthrough(t *testing.T) {
	a := newTestApp(t)
	a.rev.Overview = &review.Overview{Agent: agent.Claude, SessionID: "root", Generated: time.Now(), Walk: &review.Walkthrough{
		Summary: "Cache keys are lower-cased now.",
		Steps: []review.Step{
			{Title: "Lower-case keys", Text: "`Get` lower-cases its key.", Refs: []review.Ref{
				{File: "cache/cache.go", Side: review.SideNew, Start: 5, End: 7, Why: "the new call"},
				{File: "README.md", Side: review.SideNew, Start: 1, End: 1, Why: "unchanged file"},
				{File: "cache/cache.go", Side: review.SideNew, Start: 90, End: 91},
			}},
			{Title: "New handler", Text: "A stub."},
		},
		Checklist: []review.CheckItem{{Question: "What data changes?", Answer: "None."}},
	}}
	deeper := "```json\n" + `{"summary": "Inside Get.", "steps": [{"title": "ToLower call", "text": "Uses strings.", "refs": [{"file": "cache/cache.go", "start": 6, "end": 6}]}]}` + "\n```"
	a.cfg.Agent = agent.Config{Provider: agent.Claude, Binary: fakeClaude(t, "child", deeper)}

	tt := ui.NewTester(a.view, 1300, 1000)
	tt.Advance(time.Second)
	for _, want := range []string{"Cache keys are lower-cased now.", "1. Lower-case keys", "cache/cache.go:5-7 (+)", "\treturn strings.ToLower(key)", "# Demo", "The file has 7 lines", "Checklist", "What data changes?"} {
		if !tt.HasText(strings.ReplaceAll(want, "\t", "    ")) {
			t.Errorf("view lacks %q", want)
		}
	}
	snapshot(t, tt, "walkthrough")

	a.queue = make(chan func(), 64)
	if err := tt.Click("Go deeper"); err != nil {
		t.Fatal(err)
	}
	w := a.rev.Walks[0]
	if w.Parent != "" || w.Step != 0 || a.page != "walk:"+w.ID {
		t.Fatalf("walk = %+v, page %q", w, a.page)
	}
	pump(t, a, "the deeper walkthrough", func() bool { return a.walkRuns[w.ID] == nil })
	if w.Err != "" || w.SessionID != "child" || w.Content == nil || w.Content.Steps[0].Title != "ToLower call" {
		t.Fatalf("walk = %+v", w)
	}
	prompt, err := os.ReadFile(filepath.Join(filepath.Dir(a.cfg.Agent.Binary), "prompt.txt"))
	if err != nil || !strings.Contains(string(prompt), "### Lower-case keys") {
		t.Errorf("prompt = %q, %v", prompt, err)
	}
	tt.Frame()
	for _, want := range []string{"Overview", "Inside Get.", "1. ToLower call", "cache/cache.go:6 (+)"} {
		if !tt.HasText(want) {
			t.Errorf("deeper page lacks %q", want)
		}
	}
	snapshot(t, tt, "walk")

	if err := tt.Click("cache/cache.go:6 (+)"); err != nil {
		t.Fatal(err)
	}
	if a.page != "file:cache/cache.go" {
		t.Errorf("page = %q", a.page)
	}
	if r, ok := a.diffs["cache/cache.go"].state.SelectedLines(); !ok || r.Start != 5 || r.End != 5 {
		t.Errorf("selection = %+v, %v", r, ok)
	}
}

func TestDeeperAhead(t *testing.T) {
	a := newTestApp(t)
	answer := "```json\n" + `{"summary": "s", "steps": [{"title": "A", "text": "a"}, {"title": "B", "text": "b"}, {"title": "C", "text": "c"}, {"title": "D", "text": "d"}], "groups": []}` + "\n```"
	a.cfg.Agent = agent.Config{Provider: agent.Claude, Binary: fakeClaude(t, "s1", answer), Effort: "high", AutoEffort: "low"}
	a.queue = make(chan func(), 256)
	a.generateOverview()
	pump(t, a, "the overview", func() bool { return a.overviewRunning() == nil })
	if len(a.rev.Walks) != 4 || len(a.walkRuns) != maxAhead || !a.queued(a.rev.Walks[3].ID) {
		t.Fatalf("%d walks, %d running", len(a.rev.Walks), len(a.walkRuns))
	}
	for i, w := range a.rev.Walks {
		if w.Step != i || w.Question != "" {
			t.Errorf("walk %d = %+v", i, w)
		}
	}
	pump(t, a, "the deeper walkthroughs", func() bool {
		for _, r := range a.runsOf(a.rev) {
			if r.running {
				return false
			}
		}
		return len(a.walkRuns) == 0 && len(a.ahead) == 0
	})
	for _, w := range a.rev.Walks {
		if w.Content == nil || w.SessionID != "s1" {
			t.Errorf("walk = %+v", w)
		}
	}

	runs := a.store.Runs(a.repo.Root, a.scope)
	if len(runs) != 5 {
		t.Fatalf("%d runs", len(runs))
	}
	// The fake answers every run in session s1 at a session cost of $0.25,
	// so a deeper run, which resumes s1, adds nothing to the cost.
	want := agent.Usage{Input: 10, Output: 1000, CacheRead: 20000, CacheWrite: 5000, Context: 24000, Session: "s1", SessionCost: 0.25}
	for _, r := range runs {
		w := want
		if r.Target == store.TargetOverview {
			w.CostUSD = 0.25
		}
		if r.Usage == nil || *r.Usage != w {
			t.Errorf("usage of %s = %+v", r.Title, r.Usage)
		}
		if r.Effort != "low" {
			t.Errorf("%s ran at %q effort, want the effort for automatic runs", r.Title, r.Effort)
		}
	}
	tt := ui.NewTester(a.view, 1400, 900)
	a.showLog("")
	tt.Frame()
	// The deeper runs fork the overview's session and add nothing to it.
	if !tt.HasText("24k tokens of context") {
		t.Errorf("log lacks the total; texts %q", tt.Texts())
	}
	if !tt.HasText("· 24k tokens") {
		t.Errorf("log lacks the context of a run; texts %q", tt.Texts())
	}
	snapshot(t, tt, "deeper-ahead")

	a.askQuestion("", -1, "Why?")
	pump(t, a, "the question", func() bool { return len(a.walkRuns) == 0 })
	if r := a.runsOf(a.rev)[0]; r.info.Effort != "high" {
		t.Errorf("a question ran at %q effort, want the main one", r.info.Effort)
	}
}

func TestBackFromSnippet(t *testing.T) {
	a := newTestApp(t)
	var steps []review.Step
	for i := range 8 {
		steps = append(steps, review.Step{Title: fmt.Sprintf("Step %d", i), Text: "Text.\n\nMore text.", Refs: []review.Ref{{File: "cache/cache.go", Side: review.SideNew, Start: 5, End: 7}}})
	}
	a.rev.Overview = &review.Overview{Agent: agent.Claude, Generated: time.Now(), Walk: &review.Walkthrough{Summary: "s", Steps: steps}}
	tt := ui.NewTester(a.view, 1300, 600)
	tt.Frame()
	a.scrollOf("overview").Y = 400
	tt.Frame()
	a.openRef(steps[3].Refs[0])
	if a.page != "file:cache/cache.go" {
		t.Fatalf("page = %q", a.page)
	}
	tt.Frame()
	if err := tt.Click("‹ Back to Overview"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.page != "overview" || len(a.history) != 0 {
		t.Errorf("page = %q, history %q", a.page, a.history)
	}
	if y := a.scrollOf("overview").Y; y != 400 {
		t.Errorf("overview scrolled to %v, want 400", y)
	}
}

func TestQuestionPanel(t *testing.T) {
	a := newTestApp(t)
	a.rev.Overview = &review.Overview{Agent: agent.Claude, SessionID: "root", Generated: time.Now(), Walk: &review.Walkthrough{
		Summary: "s", Steps: []review.Step{{Title: "Lower-case keys", Text: "t"}},
	}}
	reply := "```json\n" + `{"summary": "Callers pass mixed case.", "steps": [{"title": "Caller", "text": "x", "refs": [{"file": "cache/cache.go", "start": 6, "end": 6}]}]}` + "\n```"
	a.cfg.Agent = agent.Config{Provider: agent.Claude, Binary: fakeClaude(t, "q1", reply)}
	prompt := filepath.Join(filepath.Dir(a.cfg.Agent.Binary), "prompt.txt")
	tt := ui.NewTester(a.view, 1400, 900)
	tt.Frame()

	if err := tt.Click("Ask about this step"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("About: Lower-case keys") || a.page != "overview" {
		t.Fatalf("panel not open; page %q", a.page)
	}
	a.queue = make(chan func(), 64)
	a.paneDraft = "Why lower-case?"
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyEnter)
	w := a.rev.Walk(a.question)
	if w == nil || w.Question != "Why lower-case?" || w.Step != 0 || a.page != "overview" {
		t.Fatalf("question = %+v, page %q", w, a.page)
	}
	pump(t, a, "the answer", func() bool { return !a.walkBusy(w.ID) })
	tt.Frame()
	for _, want := range []string{"Why lower-case?", "Callers pass mixed case.", "1. Caller", "cache/cache.go:6 (+)"} {
		if !tt.HasText(want) {
			t.Errorf("panel lacks %q", want)
		}
	}

	a.askMore(a.rev, w, "And the tests?")
	pump(t, a, "the follow-up", func() bool { return !a.walkBusy(w.ID) })
	if len(w.More) != 1 || w.More[0].Content == nil || w.SessionID != "q1" {
		t.Fatalf("follow-up = %+v", w.More)
	}
	if b, _ := os.ReadFile(prompt); !strings.Contains(string(b), "follow-up question: And the tests?") || strings.Contains(string(b), "The change set under review") {
		t.Errorf("follow-up prompt = %q", b)
	}
	tt.Frame()
	snapshot(t, tt, "question")

	a.closeQuestion()
	a.goTo("threads")
	tt.Frame()
	if !tt.HasText("1 follow-ups") && !tt.HasText("Jan") {
		t.Logf("texts %q", tt.Texts())
	}
	if err := tt.Click("Why lower-case?"); err != nil {
		t.Fatal(err)
	}
	if a.question != w.ID {
		t.Errorf("question = %q", a.question)
	}
}

func TestKeyboardNavigation(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()

	a.stepFile(1)
	first := a.page
	a.stepFile(1)
	second := a.page
	a.stepFile(1)
	if first != "file:api/handler.go" || second != "file:cache/cache.go" || a.page != second {
		t.Errorf("files: %q, %q, then %q", first, second, a.page)
	}
	a.goBack()
	if a.page != first {
		t.Errorf("back = %q", a.page)
	}
	a.goForward()
	if a.page != second {
		t.Errorf("forward = %q", a.page)
	}
	a.goTo("feedback")
	if len(a.forward) != 0 {
		t.Error("a new page kept the forward history")
	}

	a.newThread(review.Anchor{Kind: review.AnchorFile, File: "cache/cache.go"})
	a.rev.Thread(a.thread).Messages = []review.Message{{Role: review.RoleUser, Text: "x"}}
	a.toggleLog()
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	if a.thread != "" || !a.logOpen {
		t.Errorf("Escape: thread %q, log %v", a.thread, a.logOpen)
	}
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	if a.logOpen {
		t.Error("Escape left the log open")
	}
	tt.Key(0, ui.KeyBack)
	if a.page != second {
		t.Errorf("mouse back = %q", a.page)
	}

	seen := map[string]string{}
	var walk func(items []*mygo.MenuItem)
	walk = func(items []*mygo.MenuItem) {
		for _, it := range items {
			if it.Accelerator != "" {
				if other, ok := seen[it.Accelerator]; ok {
					t.Errorf("%s is the shortcut of %q and %q", it.Accelerator, other, it.Label)
				}
				seen[it.Accelerator] = it.Label
			}
			walk(it.Submenu)
		}
	}
	walk(a.menu().Items())
	if seen["CmdOrCtrl+["] != "Back" || seen["CmdOrCtrl+P"] != "Switch Project…" || seen["CmdOrCtrl+B"] != "Switch Branch…" {
		t.Errorf("shortcuts = %v", seen)
	}
}

func TestPickersFromKeyboard(t *testing.T) {
	a := newTestApp(t)
	dir := a.repo.Root
	for _, b := range []string{"agent", "agent-two", "docs"} {
		git(t, dir, "branch", b)
	}
	other := fixtureRepo(t)
	a.cfg.AddRecent(other)
	a.openRepo(dir)
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()

	if err := tt.Click("Branch"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("agent")
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.branchesOpen || a.head != "agent-two" {
		t.Fatalf("open %v, head %q", a.branchesOpen, a.head)
	}
	if !tt.Focused("Branch") {
		t.Error("the focus did not go back to the branch button")
	}

	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if !a.branchesOpen || a.branchNav.hi != slices.Index(a.branches, "agent-two") {
		t.Fatalf("Down on the button: open %v, highlight %d", a.branchesOpen, a.branchNav.hi)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.branchesOpen {
		t.Fatal("Escape left the list open")
	}

	if err := tt.Click("Project"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.projectsOpen || !strings.HasSuffix(a.repo.Root, other) {
		t.Fatalf("open %v, repo %q, want %q", a.projectsOpen, a.repo.Root, other)
	}
}

// TestPickListScrollKeepsHighlight scrolls the branch list from the
// keyboard while the pointer rests on a row.
func TestPickListScrollKeepsHighlight(t *testing.T) {
	a := newTestApp(t)
	for i := range 30 {
		git(t, a.repo.Root, "branch", fmt.Sprintf("b%02d", i))
	}
	a.openRepo(a.repo.Root)
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	if err := tt.Click("Branch"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	r, ok := tt.Find("b25")
	if !ok || r.H == 0 {
		t.Fatalf("no row b25: %v", r)
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if got := a.branches[a.branchNav.hi]; got != "b25" {
		t.Fatalf("the pointer left the highlight on %q", got)
	}
	for range 20 {
		tt.Key(0, ui.KeyUp)
		tt.Frame()
	}
	if got := a.branches[a.branchNav.hi]; got != "b05" {
		t.Errorf("highlight on %q, want b05", got)
	}
}

func TestSpentContext(t *testing.T) {
	at := time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC)
	run := func(sec int, session, resumed string, context int) *logRun {
		return &logRun{info: store.RunInfo{Started: at.Add(time.Duration(sec) * time.Second), Resumed: resumed, Target: session,
			Usage: &agent.Usage{Session: session, Context: context}}}
	}
	overview := run(0, "ov", "", 42160)
	runs := []*logRun{
		run(60, "f3", "ov", 74040), run(41, "f2", "ov", 73164), run(40, "f1", "ov", 73401),
		run(90, "t1", "", 10000), run(120, "t1", "t1", 15000), run(150, "t1", "t1", 18000),
		overview,
	}
	all := func(*logRun) bool { return true }
	want := 42160 + (73401 - 42160) + (73164 - 42160) + (74040 - 42160) + 18000
	if got, ok := spentContext(runs, all); !ok || got != want {
		t.Errorf("spent %d, %v; want %d", got, ok, want)
	}
	if got, _ := spentContext(runs, func(r *logRun) bool { return r.info.Target == "f1" }); got != 73401-42160 {
		t.Errorf("one fork: %d", got)
	}
	orphan := []*logRun{run(10, "x", "gone", 5000)}
	if got, ok := spentContext(orphan, all); !ok || got != 5000 {
		t.Errorf("resumed a run out of the log: %d, %v", got, ok)
	}
	if _, ok := spentContext([]*logRun{{info: store.RunInfo{Usage: &agent.Usage{Input: 5}}}}, all); ok {
		t.Error("a total without any context size")
	}
}

func TestFlatFiles(t *testing.T) {
	a := newTestApp(t)
	a.rev.Overview = &review.Overview{Markdown: "x", Groups: []review.Group{
		{Title: "Cache keys", Files: []string{"cache/cache.go"}}, {Title: "API", Files: []string{"api/handler.go"}},
	}}
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	if !shows(tt, "Cache keys") || a.fileOrder()[0] != "cache/cache.go" {
		t.Fatalf("grouped: %q", tt.Texts())
	}
	if err := tt.Click("Flat"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	snapshot(t, tt, "flat-files")
	if shows(tt, "Cache keys") || !tt.HasText("api") || !a.store.LoadConfig().FlatFiles {
		t.Errorf("flat: %q", tt.Texts())
	}
	a.stepFile(1)
	if a.page != "file:api/handler.go" {
		t.Errorf("first file in flat order: %q", a.page)
	}
}

// shows tells whether the last frame shows exactly the text s.
func shows(tt *ui.Tester, s string) bool { return slices.Contains(tt.Texts(), s) }

// TestSelectLineRange selects lines by their numbers: a click, then a
// Shift+click.
func TestSelectLineRange(t *testing.T) {
	a := newTestApp(t)
	a.page = "file:api/handler.go"
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	if !tt.HasText("Click a line number, then Shift+click another to select a range") {
		t.Errorf("no hint: %q", tt.Texts())
	}
	sel := func() {
		// The numbers of the new side, in the split view of the file page.
		tt.ClickAt(822, 103)
		tt.Frame()
		tt.ClickAtWith(ui.Shift, 822, 143)
		tt.Frame()
		if r, ok := a.diffs["api/handler.go"].state.SelectedLines(); !ok || r.Start != 0 || r.End != 2 {
			t.Fatalf("selected %+v, %v", r, ok)
		}
	}
	check := func(how string) {
		t.Helper()
		th := a.rev.Thread(a.thread)
		if th == nil || th.Anchor.Start != 1 || th.Anchor.End != 3 {
			t.Errorf("thread from %s = %+v", how, th)
		}
		if _, ok := a.diffs["api/handler.go"].state.SelectedLines(); ok {
			t.Errorf("selection kept after %s", how)
		}
	}

	// The + on a selected line comments on the whole selection.
	sel()
	tt.Move(800, 123)
	tt.Frame()
	tt.ClickAt(800, 123)
	tt.Frame()
	check("+")

	a.thread = ""
	tt.Frame()
	sel()
	if err := tt.Click("Comment on lines 1–3"); err != nil {
		t.Fatal(err)
	}
	check("the header button")
}

// TestViewedFiles marks a whole group viewed, then hides the viewed
// files from the sidebar.
func TestViewedFiles(t *testing.T) {
	a := newTestApp(t)
	a.rev.Overview = &review.Overview{Markdown: "x", Groups: []review.Group{
		{Title: "Cache keys", Files: []string{"cache/cache.go"}}, {Title: "API", Files: []string{"api/handler.go"}},
	}}
	a.page = "group:0"
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	if err := tt.Click("Mark all as viewed"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.rev.Viewed["cache/cache.go"] || !tt.HasText("Mark all as not viewed") {
		t.Fatalf("viewed = %v", a.rev.Viewed)
	}
	if err := tt.Click("Hide viewed files"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	snapshot(t, tt, "hide-viewed")
	if shows(tt, "Cache keys") || !shows(tt, "API") || !a.store.LoadConfig().HideViewed {
		t.Errorf("hidden: %q", tt.Texts())
	}
	a.stepFile(1)
	if a.page != "file:api/handler.go" {
		t.Errorf("next file skips the hidden one: %q", a.page)
	}

	// On a group page, a file marked viewed leaves the sidebar.
	a.goTo("group:1")
	tt.Frame()
	if err := tt.Click("Viewed"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if shows(tt, "handler.go") || shows(tt, "API") {
		t.Errorf("group page: %q", tt.Texts())
	}
	a.rev.Viewed["api/handler.go"] = false

	// The open file leaves the sidebar too when it is marked viewed.
	a.goTo("file:api/handler.go")
	tt.Frame()
	if err := tt.Click("Viewed"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.rev.Viewed["api/handler.go"] || shows(tt, "API") || !tt.HasText("All files are viewed.") {
		t.Errorf("open file: %q", tt.Texts())
	}

	// Previous file steps from the hidden open file.
	a.rev.Viewed["cache/cache.go"] = false
	a.stepFile(-1)
	if a.page != "file:cache/cache.go" {
		t.Errorf("previous file: %q", a.page)
	}
	a.rev.Viewed["cache/cache.go"] = true
	tt.Frame()

	tt.Click("Hide viewed files")
	tt.Frame()
	if !shows(tt, "Cache keys") || !shows(tt, "API") {
		t.Errorf("shown again: %q", tt.Texts())
	}
}

// TestGroupItem opens a group from its title in the sidebar and shows
// and hides its files with the chevron.
func TestGroupItem(t *testing.T) {
	a := newTestApp(t)
	a.rev.Overview = &review.Overview{Markdown: "x", Groups: []review.Group{
		{Title: "Cache keys", Files: []string{"cache/cache.go"}}, {Title: "API", Files: []string{"api/handler.go"}},
	}}
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	snapshot(t, tt, "group-items")
	if tt.HasText("All files in group") || !shows(tt, "cache.go") {
		t.Fatalf("sidebar: %q", tt.Texts())
	}
	if err := tt.Click("Cache keys"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.page != "group:0" {
		t.Fatalf("page = %q", a.page)
	}
	if err := tt.Click("Hide files"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.page != "group:0" || shows(tt, "cache.go") || !shows(tt, "handler.go") {
		t.Errorf("hidden: %q, %q", a.page, tt.Texts())
	}
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if !shows(tt, "cache.go") {
		t.Errorf("Right did not show the files: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyLeft)
	tt.Frame()
	if shows(tt, "cache.go") {
		t.Errorf("Left did not hide the files: %q", tt.Texts())
	}
}

// TestPickerShortcuts opens the project and branch lists from the menu
// bar's shortcuts, with the keyboard focus in them.
func TestPickerShortcuts(t *testing.T) {
	a := newTestApp(t)
	git(t, a.repo.Root, "branch", "docs")
	a.openRepo(a.repo.Root)
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	click := func(label string) {
		var walk func(items []*mygo.MenuItem)
		walk = func(items []*mygo.MenuItem) {
			for _, it := range items {
				if it.Label == label {
					it.Click(it, nil)
				}
				walk(it.Submenu)
			}
		}
		walk(a.menu().Items())
		tt.Frame()
	}

	click("Switch Branch…")
	if !a.branchesOpen {
		t.Fatal("branch list closed")
	}
	tt.Type("docs")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.branchesOpen || a.head != "docs" {
		t.Errorf("open = %v, branch = %q", a.branchesOpen, a.head)
	}

	click("Switch Project…")
	if !a.projectsOpen {
		t.Fatal("project list closed")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.projectsOpen {
		t.Error("Escape left the project list open")
	}
}

// TestFocusAndShortcuts moves the focus to the sidebars from the menu
// bar and lists the shortcuts in a dialog.
func TestFocusAndShortcuts(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	click := func(label string) {
		var walk func(items []*mygo.MenuItem)
		walk = func(items []*mygo.MenuItem) {
			for _, it := range items {
				if it.Label == label {
					it.Click(it, nil)
				}
				walk(it.Submenu)
			}
		}
		walk(a.menu().Items())
		tt.Frame()
		tt.Frame()
	}

	// Focus Files opens the first file, in the list of files only.
	click("Focus Files")
	if !tt.Focused("Files") || a.page != "file:api/handler.go" {
		t.Errorf("files sidebar: focused %v, page %q", tt.Focused("Files"), a.page)
	}
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	if a.page != "group:0" {
		t.Errorf("Up in the files sidebar: %q", a.page)
	}
	// It keeps the file open.
	a.goTo("file:cache/cache.go")
	click("Focus Files")
	if a.page != "file:cache/cache.go" {
		t.Errorf("open file: %q", a.page)
	}
	click("Focus Changes")
	if !tt.Focused("Changes") {
		t.Error("changes sidebar has no focus")
	}

	click("Keyboard Shortcuts")
	snapshot(t, tt, "shortcuts")
	for _, s := range []string{"Keyboard shortcuts", "Go menu", "Next File", "⌥⌘↓", "⇧⌘B", "⌘P", "In the window"} {
		if !tt.HasText(s) {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}
	if tt.HasText("Edit menu") || tt.HasText("Copy") {
		t.Errorf("standard items listed: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.shortcutsOpen {
		t.Error("Escape left the dialog open")
	}
}

// TestCommandPalette runs commands from the palette: only the ones that
// apply on the page show, and the ones used last come first.
func TestCommandPalette(t *testing.T) {
	a := newTestApp(t)
	a.page = "file:api/handler.go"
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	open := func() {
		t.Helper()
		var walk func(items []*mygo.MenuItem)
		walk = func(items []*mygo.MenuItem) {
			for _, it := range items {
				if it.Label == "Command Palette…" {
					it.Click(it, nil)
				}
				walk(it.Submenu)
			}
		}
		walk(a.menu().Items())
		tt.Frame()
		tt.Frame()
		if !a.paletteOpen {
			t.Fatal("palette closed")
		}
	}
	titles := func() []string {
		var out []string
		for _, cm := range a.paletteCommands() {
			out = append(out, cm.title)
		}
		return out
	}

	open()
	snapshot(t, tt, "palette")
	if got := titles(); !slices.Contains(got, "Mark File as Viewed") || slices.Contains(got, "Mark File as Not Viewed") ||
		slices.Contains(got, "Close Thread") || slices.Contains(got, "Command Palette…") {
		t.Errorf("file page commands = %q", got)
	}
	tt.Type("as viewed")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.paletteOpen || !a.rev.Viewed["api/handler.go"] {
		t.Fatalf("open = %v, viewed = %v", a.paletteOpen, a.rev.Viewed)
	}
	if got := a.store.LoadConfig().RecentCommands; len(got) == 0 || got[0] != "mark-viewed" {
		t.Errorf("recent = %q", got)
	}

	// A click on a row runs it, and it comes first next time.
	open()
	tt.Type("agent log")
	tt.Frame()
	if err := tt.Click("Agent Log"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.paletteOpen || !a.logOpen {
		t.Fatalf("open = %v, log = %v", a.paletteOpen, a.logOpen)
	}
	open()
	if got := titles(); got[0] != "Agent Log" {
		t.Errorf("recent first: %q", got)
	}
	tt.Type("focus files")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	tt.Frame()
	if !tt.Focused("Files") {
		t.Error("Focus Files from the palette left the files sidebar without the focus")
	}
	// The menu bar's commands do not count as used from the palette.
	before := a.store.LoadConfig().RecentCommands
	for _, it := range a.menu().Items() {
		for _, sub := range it.Submenu {
			if sub.Label == "Next File" {
				sub.Click(sub, nil)
			}
		}
	}
	tt.Frame()
	if got := a.store.LoadConfig().RecentCommands; !slices.Equal(got, before) {
		t.Errorf("menu changed recent: %q, was %q", got, before)
	}
	open()
	// Escape closes the palette, and only it.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.paletteOpen || !a.logOpen {
		t.Errorf("Escape: open = %v, log = %v", a.paletteOpen, a.logOpen)
	}

	a.goTo("overview")
	tt.Frame()
	open()
	if got := titles(); slices.Contains(got, "Mark File as Viewed") || slices.Contains(got, "Comment on File") {
		t.Errorf("overview commands = %q", got)
	}
}

// TestWelcomeList opens a recent project from the welcome screen with the
// keyboard.
func TestWelcomeList(t *testing.T) {
	a := newTestApp(t)
	a.cfg.AddRecent(fixtureRepo(t))
	a.closeRepo()
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	tt.Frame()
	snapshot(t, tt, "welcome")
	if !tt.Focused("Projects") {
		t.Fatal("the list has no focus")
	}
	want := a.cfg.Recent[1]
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.repo == nil || a.repo.Root != want {
		t.Fatalf("opened %v, want %s", a.repo, want)
	}
}

// TestShortcutSettings records new shortcuts in the settings: a free one,
// one another command has, which moves, and a standard one, which stays.
func TestShortcutSettings(t *testing.T) {
	a := newTestApp(t)
	a.page = "file:api/handler.go"
	tt := ui.NewTester(a.view, 1300, 800)
	a.openSettings()
	a.settings.tab = shortcutsTab
	tt.Frame()
	menuKeys := func(id string) string {
		var keys string
		var walk func(items []*mygo.MenuItem)
		walk = func(items []*mygo.MenuItem) {
			for _, it := range items {
				if it.ID == id {
					keys = it.Accelerator
				}
				walk(it.Submenu)
			}
		}
		walk(a.menu().Items())
		return keys
	}
	record := func(filter, current string, mods ui.Modifiers, key ui.Key) {
		t.Helper()
		a.keysFilter = filter
		tt.Frame()
		if err := tt.Click(current); err != nil {
			t.Fatalf("%s: %v in %q", filter, err, tt.Texts())
		}
		tt.Frame()
		tt.Frame()
		if a.recording == "" {
			t.Fatalf("%s: not recording", filter)
		}
		tt.Key(mods, key)
		tt.Frame()
	}

	record("refresh", "⌘R", ui.Cmd|ui.Shift, ui.KeyY)
	snapshot(t, tt, "shortcut-settings")
	if !sameKeys(menuKeys("refresh"), "CmdOrCtrl+Shift+Y") || !sameKeys(a.store.LoadConfig().Keys["refresh"], "CmdOrCtrl+Shift+Y") {
		t.Errorf("refresh: menu %q, config %v", menuKeys("refresh"), a.store.LoadConfig().Keys)
	}

	// The keys of another command move.
	record("mark file as viewed", "None", ui.Cmd|ui.Shift, ui.KeyY)
	if a.keysOf(a.cmd("refresh")) != "" || !sameKeys(a.keysOf(a.cmd("mark-viewed")), "CmdOrCtrl+Shift+Y") ||
		!tt.HasText("⇧⌘Y moved from Refresh to Mark File as Viewed.") {
		t.Errorf("move: %v, %q", a.cfg.Keys, a.keysNote)
	}

	// A standard shortcut stays where it is.
	record("refresh", "None", ui.Cmd, ui.KeyC)
	if a.keysOf(a.cmd("refresh")) != "" || !strings.Contains(a.keysNote, "taken by Copy") {
		t.Errorf("taken: %v, %q", a.cfg.Keys, a.keysNote)
	}

	// The modifiers held show before the key comes.
	a.keysFilter = "refresh"
	tt.Frame()
	tt.Click("None")
	tt.Frame()
	tt.Frame()
	tt.HoldModifiers(ui.Cmd | ui.Shift)
	tt.Frame()
	if !tt.HasText("⇧⌘…") {
		t.Errorf("held keys: %q", tt.Texts())
	}
	tt.HoldModifiers(0)
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	// Escape cancels a recording and keeps the dialog.
	a.keysFilter = "refresh"
	tt.Frame()
	tt.Click("None")
	tt.Frame()
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.recording != "" || a.settings == nil {
		t.Errorf("Escape: recording %q, settings %v", a.recording, a.settings)
	}

	// A menu bar shortcut pressed while recording is the recorded one.
	a.startRecording("focus-files")
	for _, it := range a.menu().Items() {
		for _, sub := range it.Submenu {
			if sub.ID == "switch-branch" {
				sub.Click(sub, nil)
			}
		}
	}
	if !sameKeys(a.keysOf(a.cmd("focus-files")), "CmdOrCtrl+B") || a.keysOf(a.cmd("switch-branch")) != "" || a.pending != "" {
		t.Errorf("menu key: %v, pending %q", a.cfg.Keys, a.pending)
	}

	// A command outside the menu bar runs by its keys where it applies.
	a.settings.open = false
	tt.Frame()
	tt.Key(ui.Cmd|ui.Shift, ui.KeyY)
	tt.Frame()
	if !a.rev.Viewed["api/handler.go"] {
		t.Error("the shortcut did not mark the file viewed")
	}
	// Also while the focus is in a text field.
	a.setViewed([]string{"api/handler.go"}, false)
	a.setKeys("mark-viewed", "CmdOrCtrl+Shift+U")
	if err := tt.Click("Comment on file"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	tt.Type("hello")
	tt.Frame()
	tt.Key(ui.Cmd|ui.Shift, ui.KeyU)
	tt.Frame()
	if !a.rev.Viewed["api/handler.go"] || a.drafts[a.thread] != "hello" {
		t.Errorf("in a field: viewed %v, draft %q", a.rev.Viewed, a.drafts[a.thread])
	}

	a.resetKeys()
	if len(a.cfg.Keys) != 0 || menuKeys("refresh") != "CmdOrCtrl+R" {
		t.Errorf("reset: %v, %q", a.cfg.Keys, menuKeys("refresh"))
	}
}

// TestScopeShowsAtOnce keeps the shown changes while the next ones load,
// then shows them with the files of the code snippets already read.
func TestScopeShowsAtOnce(t *testing.T) {
	a := newTestApp(t)
	a.rev.Overview = &review.Overview{Walk: &review.Walkthrough{Summary: "s", Steps: []review.Step{
		{Title: "A", Text: "a", Refs: []review.Ref{{File: "api/handler.go", Start: 1, End: 2}}},
	}}}
	a.queue = make(chan func(), 64)
	a.reload()
	if !a.switching || a.loading || len(a.files) == 0 {
		t.Fatalf("switching %v, loading %v, files %d", a.switching, a.loading, len(a.files))
	}
	(<-a.queue)()
	if a.switching || len(a.files) == 0 {
		t.Fatalf("switching %v, files %d", a.switching, len(a.files))
	}
	if d := a.diffs["api/handler.go"]; d == nil || d.state == nil {
		t.Error("the file of the snippet is not read with the changes")
	}
	if d := a.diffs["cache/cache.go"]; d == nil || !d.loading {
		t.Error("the other file is read before the changes show")
	}

	// A new scope shows once its changes are read.
	old := a.scope
	commit := gitx.Scope{Kind: gitx.ScopeCommit, Commit: a.commits[0].SHA}
	a.setScope(commit)
	if a.scope != old || a.scopeID != scopeID(commit) || !a.switching {
		t.Fatalf("scope %+v, id %q, switching %v", a.scope, a.scopeID, a.switching)
	}
	pump(t, a, "the commit", func() bool { return !a.switching })
	if a.scope != commit || a.page != "overview" {
		t.Errorf("scope %+v, page %q", a.scope, a.page)
	}
}

// TestScopeLoadsInOrder opens a commit while a project is still read,
// and refreshes while the commit is.
func TestScopeLoadsInOrder(t *testing.T) {
	a := newTestApp(t)
	commit := gitx.Scope{Kind: gitx.ScopeCommit, Commit: a.commits[0].SHA}
	a.queue = make(chan func(), 64)
	a.openRepo(a.repo.Root)
	a.setScope(commit)
	pump(t, a, "the commit", func() bool { return !a.loading && !a.switching })
	if a.scope != commit || a.rev.Scope != commit {
		t.Fatalf("scope %+v, review of %+v", a.scope, a.rev.Scope)
	}

	uncommitted := gitx.Scope{Kind: gitx.ScopeUncommitted}
	a.setScope(uncommitted)
	a.refresh()
	pump(t, a, "the uncommitted changes", func() bool { return !a.loading && !a.switching })
	if a.scope != uncommitted || a.scopeID != scopeID(uncommitted) {
		t.Errorf("scope %+v, id %q", a.scope, a.scopeID)
	}
}

// TestSettingsRoom leaves as much room under a tall settings dialog as
// above it.
func TestSettingsRoom(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1300, 800)
	a.openSettings()
	a.settings.tab = shortcutsTab
	tt.Frame()
	snapshot(t, tt, "settings-room")
	// 15% of the window above the dialog, and Done about 26 points
	// inside it: the padding of the dialog and of the button.
	done, _ := tt.Find("Done")
	if below := 800 - (done.Y + done.H); below < 140 || below > 152 {
		t.Errorf("room under Done %v, want about 146", below)
	}
}
