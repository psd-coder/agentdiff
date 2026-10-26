package main

import (
	"encoding/json"
	"fmt"
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
	if err := tt.Click("Compare branches…"); err != nil {
		t.Fatal(err)
	}
	a.compareForm.branch = "agent"
	tt.Frame()
	if err := tt.Click("Compare"); err != nil {
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
	if !tt.HasText("agent vs main") || !tt.HasText("Commits on agent") {
		t.Errorf("texts = %q", tt.Texts())
	}
}

// fakeClaude is a claude CLI that answers every prompt with answer, in
// session id.
func fakeClaude(t *testing.T, id, answer string) string {
	t.Helper()
	dir := t.TempDir()
	init := `{"type":"system","subtype":"init","session_id":"` + id + `"}`
	result, err := json.Marshal(map[string]any{
		"type": "result", "subtype": "success", "result": answer, "session_id": id, "total_cost_usd": 0.25,
		"modelUsage": map[string]any{"m": map[string]int{"inputTokens": 10, "outputTokens": 1000, "cacheReadInputTokens": 20000, "cacheCreationInputTokens": 5000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "out.jsonl", init+"\n"+string(result)+"\n")
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
	a.cfg.Agent = agent.Config{Provider: agent.Claude, Binary: fakeClaude(t, "s1", answer)}
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
	want := agent.Usage{Input: 10, Output: 1000, CacheRead: 20000, CacheWrite: 5000, CostUSD: 0.25}
	for _, r := range runs {
		if r.Usage == nil || *r.Usage != want {
			t.Errorf("usage of %s = %+v", r.Title, r.Usage)
		}
	}
	tt := ui.NewTester(a.view, 1400, 900)
	a.showLog("")
	tt.Frame()
	if !tt.HasText("130k tokens") {
		t.Errorf("log lacks the total; texts %q", tt.Texts())
	}
	snapshot(t, tt, "deeper-ahead")
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
