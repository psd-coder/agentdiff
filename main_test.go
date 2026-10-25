package main

import (
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/psdcoder/agentdiff/internal/agent"
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
	if err := tt.Click(a.commits[0].Short + "  Initial commit"); err != nil {
		t.Fatal(err)
	}
	if a.scope.Commit != a.commits[0].SHA || len(a.files) != 2 {
		t.Errorf("scope = %+v, files = %+v", a.scope, a.files)
	}
}

func TestSettingsOverride(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1300, 800)
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	snapshot(t, tt, "settings")
	a.settings.override = true
	a.settings.local.provider = 1
	a.settings.local.model = "gpt-5"
	a.saveSettings()
	cfg, _ := a.cfg.Effective(a.rev.Settings)
	if cfg.Provider != agent.Codex || cfg.Model != "gpt-5" {
		t.Errorf("effective = %+v", cfg)
	}
	if a.cfg.Agent.Provider != agent.Claude {
		t.Errorf("global changed: %+v", a.cfg.Agent)
	}
}

func lineRange(start, end int) editor.DiffLineRange {
	return editor.DiffLineRange{Side: editor.DiffNew, Start: start, End: end}
}

func TestStaleOverviewAndStartOver(t *testing.T) {
	a := newTestApp(t)
	a.rev.Overview = &review.Overview{Markdown: "## A\nx", Groups: []review.Group{{Title: "Old", Files: []string{"gone.go"}}}, Agent: agent.Claude}
	a.comment(a.rev.AddThread(review.Anchor{Kind: review.AnchorGeneral}), "old note")
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	if !tt.HasText("The changed files differ from the ones this overview was written for. Regenerate it, or start over.") {
		t.Error("stale warning missing")
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
