package app

import (
	"bytes"
	"errors"
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

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
	"github.com/psd-coder/agentdiff/internal/store"
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
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
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
	app := newApp(store.Store{Dir: t.TempDir()})
	t.Cleanup(app.streams.Wait)
	app.openRepo(fixtureRepo(t))
	if app.repo == nil {
		t.Fatalf("open: %s", app.repoErr)
	}
	return app
}

func snapshot(t *testing.T, tt *ui.Tester, name string) {
	dir := os.Getenv("AGENTDIFF_SNAPSHOTS")
	if dir == "" {
		return
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, tt.Image()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".png"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOpensRepoWithChanges(t *testing.T) {
	app := newTestApp(t)
	if len(app.files) != 2 {
		t.Fatalf("files = %+v", app.files)
	}
	tt := ui.NewTester(app.view, 1300, 800)
	for _, want := range []string{"Overview", "Uncommitted", "Initial commit", "cache.go", "handler.go", "Explain changes with Claude Code"} {
		if !strings.Contains(strings.Join(tt.Texts(), "\n"), want) {
			t.Errorf("view lacks %q; texts %q", want, tt.Texts())
		}
	}
	snapshot(t, tt, "overview-empty")
}

func TestOverviewAndGroups(t *testing.T) {
	app := newTestApp(t)
	md := "## 1. High level\n- Keys are lower-cased in `cache/cache.go:6`.\n\n## 2. Data changes\nNone.\n\n```json\n{\"groups\":[{\"title\":\"Cache keys\",\"summary\":\"Lower-case keys.\",\"files\":[\"cache/cache.go\"]},{\"title\":\"API\",\"summary\":\"New handler.\",\"files\":[\"api/handler.go\"]}]}\n```"
	body, groups := review.ParseOverview(md, app.fileNames())
	app.rev.Overview = &review.Overview{Markdown: body, Groups: groups, Agent: agent.Claude, Generated: time.Now(), Sessions: review.Sessions{agent.Claude: "s"}}
	tt := ui.NewTester(app.view, 1300, 900)
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
	if app.nav.page != (page{kindGroup, "Cache keys"}) {
		t.Errorf("page = %q", app.nav.page.String())
	}
	tt.Advance(time.Second)
	snapshot(t, tt, "group")
	if layout := app.diffs["cache/cache.go"].state.Layout; layout != editor.DiffSplit {
		t.Errorf("group diff layout = %v, want split", layout)
	}
	if err := tt.Click("Unified"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if layout := app.diffs["cache/cache.go"].state.Layout; layout != editor.DiffUnified || !app.cfg.Unified {
		t.Errorf("after Unified: layout %v, config %v", layout, app.cfg.Unified)
	}
}

func TestCommentOnFileAndExport(t *testing.T) {
	app := newTestApp(t)
	app.nav.page = page{kindFile, "cache/cache.go"}
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	if err := tt.Click("Comment on file"); err != nil {
		t.Fatal(err)
	}
	if app.threadID() == "" {
		t.Fatal("no thread opened")
	}
	tt.Type("Keep keys case-sensitive.")
	if err := tt.Click("Comment"); err != nil {
		t.Fatal(err)
	}
	th := app.rev.Thread(app.threadID())
	if th == nil || len(th.Messages) != 1 || th.Messages[0].Text != "Keep keys case-sensitive." {
		t.Fatalf("thread = %+v", th)
	}
	snapshot(t, tt, "file-thread")
	got := app.feedback()
	if !strings.Contains(got, "## cache/cache.go (file-level)\nKeep keys case-sensitive.") {
		t.Errorf("feedback =\n%s", got)
	}
	app.nav.page = page{kind: kindFeedback}
	tt.Frame()
	if err := tt.Click("Copy feedback"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tt.Clipboard(), "Keep keys case-sensitive.") {
		t.Errorf("clipboard = %q", tt.Clipboard())
	}
	snapshot(t, tt, "feedback")
	reloaded := app.store.LoadReview(app.repo.Root, app.scope)
	if len(reloaded.Threads) != 1 {
		t.Errorf("saved threads = %d", len(reloaded.Threads))
	}
}

func TestLineThreadAnnotation(t *testing.T) {
	app := newTestApp(t)
	app.nav.page = page{kindFile, "cache/cache.go"}
	app.lineThread("cache/cache.go", lineRange(5, 5))
	th := app.rev.Thread(app.threadID())
	if th.Anchor.Quote != "\treturn strings.ToLower(key)" || th.Anchor.Label() != "cache/cache.go:6 (+)" {
		t.Errorf("anchor = %+v", th.Anchor)
	}
	app.comment(th, "Why lower-case?")
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	if !tt.HasText("Why lower-case?") {
		t.Error("annotation card missing")
	}
	snapshot(t, tt, "file-line-thread")
	app.closeThread()
	tt.Frame()
	if len(app.rev.Threads) != 1 {
		t.Error("closing a thread with messages dropped it")
	}
}

func TestEmptyThreadDroppedOnClose(t *testing.T) {
	app := newTestApp(t)
	app.newThread(review.Anchor{Kind: review.AnchorGeneral})
	app.closeThread()
	if len(app.rev.Threads) != 0 {
		t.Errorf("threads = %d, want 0", len(app.rev.Threads))
	}
}

func TestScopeSwitchToCommit(t *testing.T) {
	app := newTestApp(t)
	if len(app.commits.rows) != 1 {
		t.Fatalf("commits = %+v", app.commits.rows)
	}
	tt := ui.NewTester(app.view, 1300, 800)
	if err := tt.Click("Initial commit"); err != nil {
		t.Fatal(err)
	}
	if app.scope.Commit != app.commits.rows[0].SHA || len(app.files) != 2 {
		t.Errorf("scope = %+v, files = %+v", app.scope, app.files)
	}
}

func TestSettings(t *testing.T) {
	app := newTestApp(t)
	tt := ui.NewTester(app.view, 1300, 800)
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	snapshot(t, tt, "settings")
	title, _ := tt.Find("Shortcuts")
	if tt.HasText("This review") {
		t.Errorf("per-review tab: %q", tt.Texts())
	}
	app.settings.agent.provider = 1
	app.settings.agent.model = "gpt-5"
	app.settings.ahead = false
	tt.Frame()
	if got := app.store.LoadConfig(); got.Agent.Provider != agent.Codex || got.Agent.Model != "gpt-5" || got.DeeperAhead {
		t.Errorf("not saved as changed: %+v", got)
	}
	if got := newAgentForm(app.store.LoadConfig().Agent).provider; got != 1 {
		t.Errorf("settings opened again on provider %d, want Codex", got)
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
	app.keysFilter = "refresh"
	tt.Frame()
	done, _ := tt.Find("Done")
	if reset, _ := tt.Find("Reset all shortcuts"); done.Y-reset.Y > 80 {
		t.Errorf("room under the short list: Reset all at %v, Done at %v", reset, done)
	}

	if err := tt.Click("Appearance"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := app.store.LoadConfig().Theme; got != "dark" {
		t.Errorf("theme = %q, want dark", got)
	}
	if err := tt.Click("System"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := app.store.LoadConfig().Theme; got != "" {
		t.Errorf("theme = %q, want the system one", got)
	}

	if err := tt.Click("Done"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.settings != nil {
		t.Error("Done left the settings open")
	}
}

func TestThemeIndex(t *testing.T) {
	for saved, want := range map[string]int{"": 0, "light": 1, "dark": 2, "sepia": 0} {
		if got := themeIndex(saved); got != want {
			t.Errorf("themeIndex(%q) = %d, want %d", saved, got, want)
		}
	}
}

func lineRange(start, end int) editor.DiffLineRange {
	return editor.DiffLineRange{Side: editor.DiffNew, Start: start, End: end}
}

// TestReviewFollowsCommit writes an overview of uncommitted changes and
// commits them: the review then belongs to the commit.
func TestReviewFollowsCommit(t *testing.T) {
	app := newTestApp(t)
	dir := app.repo.Root
	answer := "```json\n" + `{"summary": "s", "steps": [{"title": "A", "text": "a"}], "groups": []}` + "\n```"
	newFakeAgent(t, app).answer("s1", answer)
	app.cfg.DeeperAhead = false
	app.queue = make(chan func(), 64)
	app.generateOverview()
	pump(t, app, "the overview", func() bool { return !app.busy(store.TargetOverview) })

	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "Agent work")
	sha := revSHA(t, dir, "HEAD")
	app.refresh()
	tt := ui.NewTester(app.view, 1300, 800)
	waitText(t, app, tt, "now commit "+shortSHA(sha))

	if err := tt.Click("s"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Open commit"); err != nil {
		t.Fatal(err)
	}
	waitText(t, app, tt, "Moved here from earlier uncommitted changes, which had the same changes.")
	if !tt.HasText(shortSHA(sha)+" Agent work") || tt.HasText("This overview was written for") {
		t.Errorf("the commit does not show the review as current: %q", tt.Texts())
	}

	if err := tt.Click("Uncommitted changes"); err != nil {
		t.Fatal(err)
	}
	waitText(t, app, tt, "There is nothing to review in uncommitted changes.")
	if tt.HasText("Earlier reviews") {
		t.Errorf("the uncommitted changes still list the review: %q", tt.Texts())
	}
}

// TestReviewToBiggerCommit commits the reviewed changes together with
// another file: the review is offered to that commit.
func TestReviewToBiggerCommit(t *testing.T) {
	app := newTestApp(t)
	dir := app.repo.Root
	answer := "```json\n" + `{"summary": "s", "steps": [{"title": "A", "text": "a"}], "groups": []}` + "\n```"
	newFakeAgent(t, app).answer("s1", answer)
	app.cfg.DeeperAhead = false
	app.queue = make(chan func(), 64)
	app.generateOverview()
	pump(t, app, "the overview", func() bool { return !app.busy(store.TargetOverview) })

	write(t, dir, "extra.go", "package extra\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "Agent work and more")
	sha := revSHA(t, dir, "HEAD")
	app.refresh()
	tt := ui.NewTester(app.view, 1300, 800)
	waitText(t, app, tt, "probably in commit "+shortSHA(sha))

	if err := tt.Click("s"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Move review to the commit"); err != nil {
		t.Fatal(err)
	}
	waitText(t, app, tt, "This overview was written for 2 files. Since then, 1 new file is not in it. Its code snippets can show the wrong lines. Regenerate the overview, or start over.")
	if !tt.HasText(shortSHA(sha) + " Agent work and more") {
		t.Errorf("the commit is not open: %q", tt.Texts())
	}
}

// TestArchiveUncommitted replaces the uncommitted changes with other
// ones: their review stays while an agent writes into it, then goes to
// the list of earlier reviews, and the Restore button brings it back
// with a banner that counts how its files changed.
func TestArchiveUncommitted(t *testing.T) {
	app := newTestApp(t)
	dir := app.repo.Root
	answer := "```json\n" + `{"summary": "Lower-case keys.", "steps": [{"title": "A", "text": "a"}], "groups": []}` + "\n```"
	fake := newFakeAgent(t, app)
	fake.answer("s1", answer)
	app.cfg.DeeperAhead = false
	app.queue = make(chan func(), 64)
	app.generateOverview()
	pump(t, app, "the overview", func() bool { return !app.busy(store.TargetOverview) })
	tt := ui.NewTester(app.view, 1300, 800)
	reload := func(what string) {
		t.Helper()
		app.refresh()
		waitLoad(t, app, what)
		drain(app)
		tt.Frame()
	}

	git(t, dir, "stash", "-u", "-q")
	write(t, dir, "output.json", "{}\n")
	fake.hold()
	app.generateOverview()
	reload("the busy reload")
	if tt.HasText("Earlier reviews") {
		t.Fatal("archived a review an agent writes into")
	}
	app.runner.stop(app.rev, store.TargetOverview)
	pump(t, app, "the stopped overview", func() bool { return !app.logsOf(app.rev)[0].running })
	reload("the other changes")
	if !tt.HasText("Earlier reviews") || !tt.HasText("Lower-case keys.") || !tt.HasText("Get a walkthrough of these changes") {
		t.Fatalf("texts %q", tt.Texts())
	}
	snapshot(t, tt, "archived")

	if err := tt.Click("Lower-case keys."); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Restore"); err != nil {
		t.Fatal(err)
	}
	reload("the restored review")
	if tt.HasText("Earlier reviews") || !tt.HasText("This overview was written for 2 files. Since then, 2 are no longer in the changes and 1 new file is not in it. Its code snippets can show the wrong lines. Regenerate the overview, or start over.") {
		t.Fatalf("texts %q", tt.Texts())
	}

	write(t, dir, "cache/cache.go", "package cache\n")
	reload("a file of the review")
	if !tt.HasText("This overview was written for 2 files. Since then, 1 changed, 1 is no longer in the changes and 1 new file is not in it. Its code snippets can show the wrong lines. Regenerate the overview, or start over.") {
		t.Errorf("texts %q", tt.Texts())
	}
}

func TestArchiveAndAdoptShowsWhereTheReviewCameFrom(t *testing.T) {
	app := newTestApp(t)
	dir := app.repo.Root
	fake := newFakeAgent(t, app)
	app.cfg.DeeperAhead = false
	app.queue = make(chan func(), 64)
	tt := ui.NewTester(app.view, 1300, 800)
	overview := func(summary string) {
		t.Helper()
		fake.answer("s1", "```json\n"+`{"summary": "`+summary+`", "steps": [{"title": "A", "text": "a"}], "groups": []}`+"\n```")
		app.generateOverview()
		pump(t, app, "the overview", func() bool { return !app.busy(store.TargetOverview) })
	}
	reload := func(what string) {
		t.Helper()
		app.refresh()
		waitLoad(t, app, what)
		drain(app)
		tt.Frame()
	}

	overview("First changes.")
	git(t, dir, "stash", "-u", "-q")
	write(t, dir, "output.json", "{}\n")
	reload("the other changes")
	overview("Other changes.")

	if err := os.Remove(filepath.Join(dir, "output.json")); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "stash", "pop", "-q")
	reload("the first changes back")
	if !tt.HasText("First changes.") || !tt.HasText("Moved here from earlier uncommitted changes, which had the same changes.") || !tt.HasText("Other changes.") {
		t.Fatalf("texts %q", tt.Texts())
	}

	app.startOver()
	tt.Frame()
	if tt.HasText("Moved here from earlier uncommitted changes, which had the same changes.") {
		t.Errorf("the notice stayed after start over: %q", tt.Texts())
	}
}

func TestStaleOverviewAndStartOver(t *testing.T) {
	app := newTestApp(t)
	app.rev.Overview = &review.Overview{Markdown: "## A\nx", Groups: []review.Group{{Title: "Old", Files: []string{"gone.go"}}}, Agent: agent.Claude}
	app.comment(app.rev.AddThread(review.Anchor{Kind: review.AnchorGeneral}), "old note")
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	if !tt.HasText("This overview was written for 1 file. Since then, 1 is no longer in the changes and 2 new files are not in it. Its code snippets can show the wrong lines. Regenerate the overview, or start over.") {
		t.Errorf("stale warning missing: %q", tt.Texts())
	}
	app.startOver()
	tt.Frame()
	if app.rev.Overview != nil || len(app.rev.Threads) != 0 {
		t.Errorf("review after start over = %+v", app.rev)
	}
	if got := app.store.LoadReview(app.repo.Root, app.scope); len(got.Threads) != 0 {
		t.Error("saved review survived start over")
	}
}

// TestFailedSaveShowsNotice saves while the store cannot be written: the
// window says so until a later save works.
func TestFailedSaveShowsNotice(t *testing.T) {
	app := newTestApp(t)
	good := app.store
	notADir := filepath.Join(t.TempDir(), "file")
	write(t, filepath.Dir(notADir), "file", "")
	app.store = store.Store{Dir: notADir}
	app.setViewed([]string{"cache/cache.go"}, true)
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	if !tt.HasText("Could not save your changes. AgentDiff tries again on the next change.") || !tt.HasText("not a directory") {
		t.Fatalf("save error not shown: %q", tt.Texts())
	}
	snapshot(t, tt, "save-error")

	app.store = good
	app.setViewed([]string{"cache/cache.go"}, false)
	tt.Frame()
	if tt.HasText("Could not save your changes") {
		t.Errorf("save error shown after a save that worked: %q", tt.Texts())
	}
}

// TestBadBaseShowsCommitsError compares with a base that does not exist:
// the sidebar shows why there are no commits, until the base is valid.
func TestBadBaseShowsCommitsError(t *testing.T) {
	app := newTestApp(t)
	app.compare("main", "nope")
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	if !tt.HasText(`Could not list the commits: gitx: unknown revision "nope"`) || tt.HasText("No commits") {
		t.Fatalf("commits error not shown: %q", tt.Texts())
	}

	app.compare("main", "main")
	tt.Frame()
	if tt.HasText("Could not list the commits") || !tt.HasText("Initial commit") {
		t.Errorf("after a valid base: %q", tt.Texts())
	}
}

// TestStartOverKeepsReviewWhenDeleteFails starts over while the saved
// review cannot be deleted: the review stays, in the window and on disk.
func TestStartOverKeepsReviewWhenDeleteFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root deletes files in a read-only directory")
	}
	app := newTestApp(t)
	app.comment(app.rev.AddThread(review.Anchor{Kind: review.AnchorGeneral}), "keep me")
	dirs, err := filepath.Glob(filepath.Join(app.store.Dir, "reviews", "*"))
	if err != nil || len(dirs) != 1 {
		t.Fatalf("review dirs %q: %v", dirs, err)
	}
	if err := os.Chmod(dirs[0], 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dirs[0], 0o755); err != nil {
			t.Error(err)
		}
	})

	app.startOver()
	if len(app.rev.Threads) != 1 {
		t.Errorf("threads after a failed start over = %d", len(app.rev.Threads))
	}
	if got := app.store.LoadReview(app.repo.Root, app.scope); len(got.Threads) != 1 {
		t.Errorf("saved threads after a failed start over = %d", len(got.Threads))
	}
}

// TestStartOverDuringRuns starts over while a thread and a question run.
// Their late answers do not bring the deleted review back.
func TestStartOverDuringRuns(t *testing.T) {
	app := newTestApp(t)
	root, scope := app.repo.Root, app.scope
	app.queue = make(chan func(), 64)
	fake := newFakeAgent(t, app)
	fake.answer("t1", "Because.")
	app.ask(app.rev, app.rev.AddThread(review.Anchor{Kind: review.AnchorGeneral}), "Why?")
	thread := app.logs[app.rev][0]
	fake.hold()
	app.askQuestion("", -1, "What?")
	runs := slices.Clone(app.logs[app.rev])
	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, _ := app.store.ReadRun(root, scope, thread.info.ID)
		if count := len(entries); count > 0 && entries[count-1].Kind == agent.LogExit {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the thread run did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}

	app.startOver()
	pump(t, app, "the end of both runs", func() bool {
		return !slices.ContainsFunc(runs, func(r *logRun) bool { return r.running })
	})
	if got := app.store.LoadReview(root, scope); len(got.Threads) != 0 || len(got.Walks) != 0 {
		t.Errorf("deleted review came back: %d threads, %d walks", len(got.Threads), len(got.Walks))
	}
	if _, err := os.Stat(filepath.Dir(app.store.RunPath(root, scope, thread.info.ID))); !os.IsNotExist(err) {
		t.Errorf("run logs of the deleted review came back: %v", err)
	}
}

// TestThreadAsksOnceWhileAnswering comments in a thread while the agent
// answers it. The thread cannot ask again until the answer ends, and Stop
// still stops the answer.
func TestThreadAsksOnceWhileAnswering(t *testing.T) {
	app := newTestApp(t)
	app.queue = make(chan func(), 64)
	fake := newFakeAgent(t, app)
	fake.answer("t1", "Because.")
	fake.hold()
	th := app.rev.AddThread(review.Anchor{Kind: review.AnchorGeneral})
	app.openThread(th.ID)
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	tt.Type("Why?")
	tt.Key(ui.Cmd, ui.KeyEnter)
	tt.Frame()
	if count := len(app.logs[app.rev]); count != 1 {
		t.Fatalf("runs after the first question = %d", count)
	}
	if !tt.HasText("Thinking…") {
		t.Error("the thread does not show the answer being written")
	}

	tt.Type("Keep it short.")
	if err := tt.Click("Comment"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	hint, ok := tt.Find("Cmd+Enter asks the agent")
	if !ok {
		t.Fatal("the composer is not open")
	}
	// The text area sits right above the hint, and Comment took its focus.
	tt.ClickAt(hint.X+20, hint.Y-30)
	tt.Type("And the tests?")
	tt.Key(ui.Cmd, ui.KeyEnter)
	tt.Frame()
	if err := tt.Click("Ask agent"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if count := len(fake.received()); count != 1 {
		t.Fatalf("requests after asking again = %d", count)
	}
	if draft := app.drafts[th.ID]; !strings.Contains(draft, "And the tests?") {
		t.Errorf("draft after asking again = %q", draft)
	}
	if !app.runner.reviewBusy(app.rev) {
		t.Error("the review is not busy while the thread runs")
	}

	if !app.applies(app.cmd("stop-thread"), th.ID) {
		t.Error("stop-thread does not apply after the comment")
	}
	if err := tt.Click("Stop"); err != nil {
		t.Fatal(err)
	}
	pump(t, app, "the stopped answer", func() bool { return !app.busy(th.ID) })
	if app.applies(app.cmd("stop-thread"), th.ID) {
		t.Error("stop-thread applies after the answer ended")
	}
	want := []review.Message{
		{Role: review.RoleUser, Text: "Why?"},
		{Role: review.RoleAgent, Err: "Stopped"},
		{Role: review.RoleUser, Text: "Keep it short."},
	}
	saved := app.store.LoadReview(app.repo.Root, app.scope).Thread(th.ID)
	for name, got := range map[string]*review.Thread{"shown": th, "saved": saved} {
		if got == nil || len(got.Messages) != len(want) {
			t.Fatalf("%s messages = %+v", name, got)
		}
		for i, m := range got.Messages {
			if m.Role != want[i].Role || m.Text != want[i].Text || m.Err != want[i].Err {
				t.Errorf("%s message %d = %+v, want %+v", name, i, m, want[i])
			}
		}
	}
}

// TestDeleteWalkStopsItsRuns deletes a walk: the runs of the walk and the
// walks under it stop, and the runs of other walks go on.
func TestDeleteWalkStopsItsRuns(t *testing.T) {
	app := newTestApp(t)
	app.queue = make(chan func(), 64)
	fake := newFakeAgent(t, app)
	fake.answer("w1", "Because.")
	fake.hold()
	app.setScope(gitx.Scope{Kind: gitx.ScopeCommit, Commit: "HEAD"})
	waitLoad(t, app, "the commit")
	otherRev := app.rev
	other := otherRev.AddWalk("", -1, "Other review?")
	app.runWalk(otherRev, other)
	app.setScope(gitx.Scope{Kind: gitx.ScopeUncommitted})
	waitLoad(t, app, "the uncommitted changes")
	parent := app.rev.AddWalk("", -1, "Parent?")
	child := app.rev.AddWalk(parent.ID, -1, "Child?")
	sibling := app.rev.AddWalk("", -1, "Sibling?")
	for _, w := range []*review.Walk{parent, child, sibling} {
		app.runWalk(app.rev, w)
	}
	app.deleteWalk(parent)
	pump(t, app, "the stopped runs", func() bool { return !app.busy(parent.ID) && !app.busy(child.ID) })
	otherBusy := app.runner.run(otherRev, other.ID) != nil
	if parent.Err != "Stopped" || child.Err != "Stopped" || !app.busy(sibling.ID) || !otherBusy {
		t.Errorf("parent %q, child %q, sibling busy %v, walk of another review busy %v", parent.Err, child.Err, app.busy(sibling.ID), otherBusy)
	}
}

// TestThreadOfReviewOffScreen asks in a thread of a review that is not
// on screen. The question goes to that review's repository and scope, and
// the answer goes into its thread.
func TestThreadOfReviewOffScreen(t *testing.T) {
	app := newTestApp(t)
	app.queue = make(chan func(), 64)
	fake := newFakeAgent(t, app)
	fake.answer("t1", "Because.")
	other := review.New(t.TempDir(), gitx.Scope{Kind: gitx.ScopeCommit, Commit: "abc123"})
	th := other.AddThread(review.Anchor{Kind: review.AnchorGeneral})
	app.ask(other, th, "Why?")
	pump(t, app, "the answer", func() bool { return app.runner.run(other, th.ID) == nil })
	req := fake.received()[0]
	if req.Dir != other.Repo {
		t.Errorf("dir %q, want %q", req.Dir, other.Repo)
	}
	if !strings.Contains(req.Prompt, "commit abc123") || strings.Contains(req.Prompt, "uncommitted") || !strings.Contains(req.Prompt, "Diffstat:\n```\n\n```") {
		t.Errorf("prompt = %q", req.Prompt)
	}
	if count := len(th.Messages); count != 2 || th.Messages[1].Text != "Because." {
		t.Errorf("messages = %+v", th.Messages)
	}
}

// TestAgentLog runs a fake Claude that replays a recorded session, then
// reads the run's log in a new app, as after a restart.
func TestAgentLog(t *testing.T) {
	app := newTestApp(t)
	fixture, err := filepath.Abs("../agent/testdata/claude_tool.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "claude")
	write(t, filepath.Dir(bin), "claude", "#!/bin/sh\ncat > /dev/null\ncat "+fixture+"\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	app.cfg.Agent = agent.Config{Provider: agent.Claude, Binary: bin}
	app.generateOverview()
	waitForExit(t, app.store, app.repo.Root, app.scope)

	reopened := newApp(app.store)
	reopened.openRepo(app.repo.Root)
	tt := ui.NewTester(reopened.view, 1400, 900)
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

func waitForExit(t *testing.T, storage store.Store, repo string, scope gitx.Scope) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runs := storage.Runs(repo, scope); len(runs) == 1 {
			entries, _ := storage.ReadRun(repo, scope, runs[0].ID)
			if count := len(entries); count > 0 && entries[count-1].Kind == agent.LogExit {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the run did not finish")
}

func TestScopeSwitchKeepsReview(t *testing.T) {
	app := newTestApp(t)
	rev := app.rev
	app.setScope(gitx.Scope{Kind: gitx.ScopeCommit, Commit: "HEAD"})
	app.setScope(gitx.Scope{Kind: gitx.ScopeUncommitted})
	if app.rev != rev {
		t.Error("switching back loaded a second copy of the review")
	}
}

func TestGroupsFollowChanges(t *testing.T) {
	app := newTestApp(t)
	answer := "```json\n" + `{"summary": "s", "steps": [{"title": "A", "text": "a"}], "groups": [{"title": "Cache keys", "files": ["cache/cache.go"]}]}` + "\n```"
	newFakeAgent(t, app).answer("s1", answer)
	app.cfg.DeeperAhead = false
	app.queue = make(chan func(), 64)
	want := func(step string, titles ...string) {
		t.Helper()
		var got []string
		for _, g := range app.groups() {
			got = append(got, g.Title)
		}
		if !slices.Equal(got, titles) {
			t.Fatalf("%s: groups %q, want %q", step, got, titles)
		}
	}
	loaded := func(what string) {
		t.Helper()
		waitLoad(t, app, what)
		drain(app)
	}
	want("first load", "api", "cache")

	write(t, app.repo.Root, "docs/new.md", "# New\n")
	app.refresh()
	loaded("the reload")
	want("reload", "api", "cache", "docs")

	app.rev.Overview = &review.Overview{Markdown: "x", Groups: []review.Group{{Title: "Docs first", Files: []string{"docs/new.md"}}}}
	want("overview written", "Docs first", "Other changes")

	app.regenerate("")
	pump(t, app, "the overview", func() bool { return !app.busy(store.TargetOverview) })
	want("regenerate", "Cache keys", "Other changes")

	app.startOver()
	want("start over", "api", "cache", "docs")

	app.setScope(gitx.Scope{Kind: gitx.ScopeCommit, Commit: "HEAD"})
	loaded("the commit")
	want("commit scope", "Repository root", "cache")
	app.setScope(gitx.Scope{Kind: gitx.ScopeUncommitted})
	loaded("the uncommitted changes")
	want("uncommitted scope", "api", "cache", "docs")
}

func TestCompareBranches(t *testing.T) {
	app := newTestApp(t)
	dir := app.repo.Root
	git(t, dir, "stash", "-u", "-q")
	git(t, dir, "checkout", "-q", "-b", "agent")
	write(t, dir, "feature.go", "package feature\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "Add feature")
	git(t, dir, "checkout", "-q", "main")
	app.openRepo(dir)

	tt := ui.NewTester(app.view, 1300, 800)
	if err := tt.Click("Branch"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	snapshot(t, tt, "branches")
	if err := tt.Click("agent"); err != nil {
		t.Fatal(err)
	}
	if app.scope.Head != "agent" || app.scope.Base != "main" {
		t.Fatalf("scope = %+v", app.scope)
	}
	if len(app.files) != 1 || app.files[0].Path != "feature.go" {
		t.Errorf("files = %+v", app.files)
	}
	if len(app.commits.rows) != 1 || app.commits.rows[0].Subject != "Add feature" {
		t.Errorf("commits = %+v", app.commits.rows)
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
	app.compareForm.branch, app.compareForm.base = "main", "origin/main"
	tt.Frame()
	if err := tt.Click("Show changes"); err != nil {
		t.Fatal(err)
	}
	if app.scope.Branch != "main" || app.scope.Base != "origin/main" {
		t.Fatalf("scope = %+v", app.scope)
	}
	tt.Frame()
	if !tt.HasText("vs origin/main") || !tt.HasText("Uncommitted changes") {
		t.Errorf("texts = %q", tt.Texts())
	}

	app.pickBranch("main")
	if app.scope.Kind != gitx.ScopeBranch || app.scope.Base != "origin/main" {
		t.Errorf("picked base is forgotten: scope = %+v", app.scope)
	}
	app.cmpr.base, app.cmpr.baseSet = "main", false
	app.pickBranch("main")
	if app.scope.Kind != gitx.ScopeUncommitted {
		t.Errorf("main against itself: scope = %+v", app.scope)
	}
	app.cmpr.base = "agent"
	app.pickBranch("agent")
	if app.scope.Kind != gitx.ScopeCommit || len(app.commits.rows) == 0 || app.scope.Commit != app.commits.rows[0].SHA {
		t.Errorf("other branch against itself: scope = %+v", app.scope)
	}
}

func TestCustomRange(t *testing.T) {
	app := newTestApp(t)
	dir := app.repo.Root
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "Second")
	app.openRepo(dir)
	app.openCompare()
	app.compareForm.mode, app.compareForm.from, app.compareForm.to = compareRange, "HEAD~1", "HEAD"
	tt := ui.NewTester(app.view, 1300, 800)
	if err := tt.Click("Show changes"); err != nil {
		t.Fatal(err)
	}
	if app.scope.Kind != gitx.ScopeRange || len(app.files) != 2 {
		t.Fatalf("scope = %+v, files = %+v", app.scope, app.files)
	}
	if len(app.commits.rows) != 1 || app.commits.rows[0].Subject != "Second" {
		t.Errorf("commits = %+v", app.commits.rows)
	}
	tt.Frame()
	if !tt.HasText("Commits in HEAD~1..HEAD") || tt.HasText("Uncommitted changes") {
		t.Errorf("texts = %q", tt.Texts())
	}
	app.pickBranch("main")
	if app.cmpr.rng != nil || app.scope.Kind != gitx.ScopeUncommitted {
		t.Errorf("picking a branch kept the range: scope = %+v", app.scope)
	}
}

func TestRemoveProject(t *testing.T) {
	app := newTestApp(t)
	other := fixtureRepo(t)
	app.cfg.AddRecent(other)
	current := app.repo.Root
	app.openRepo(current)
	app.projectsOpen = true
	tt := ui.NewTester(app.view, 1300, 800)
	snapshot(t, tt, "projects")
	row, ok := tt.Find(filepath.Base(other))
	if !ok {
		t.Fatalf("no row for %s: %q", other, tt.Texts())
	}
	tt.Move(row.X+row.W/2, row.Y+row.H/2)
	tt.Frame()
	if err := tt.Click("Remove from the list"); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(app.cfg.Recent, other) || app.repo == nil {
		t.Fatalf("recent = %v, repo = %v", app.cfg.Recent, app.repo)
	}
	app.confirmRemoveProject(current)
	if len(app.cfg.Recent) != 0 || app.repo != nil {
		t.Fatalf("recent = %v, repo = %v", app.cfg.Recent, app.repo)
	}
	if !slices.Equal(app.store.LoadConfig().Recent, app.cfg.Recent) {
		t.Error("the list is not saved")
	}
	tt.Frame()
	if !tt.HasText("Open repository…") {
		t.Errorf("no welcome screen: %q", tt.Texts())
	}
}

func TestRunCost(t *testing.T) {
	at := time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC)
	ended := func(sec int, session string, cost float64) *logRun {
		return &logRun{info: store.RunInfo{Started: at.Add(time.Duration(sec) * time.Second), Usage: &agent.Usage{Session: session, SessionCost: cost}}}
	}
	parent := ended(0, "p", 0.5)
	later := ended(20, "p", 0.55)
	for _, tc := range []struct {
		resumed string
		want    float64
	}{{"", 0.6}, {"p", 0.1}, {"gone", 0}} {
		run := ended(10, "c", 0.6)
		run.info.Resumed = tc.resumed
		if got := runCost([]*logRun{later, run, parent}, run); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("resumed %q: cost %v, want %v", tc.resumed, got, tc.want)
		}
	}
}

func TestLoadedRunStatusAndCost(t *testing.T) {
	app := newTestApp(t)
	at := time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC)
	result := func(session string, cost float64) string {
		return mustJSON(map[string]any{"type": "result", "subtype": "success", "session_id": session, "total_cost_usd": cost, "usage": map[string]int{"output_tokens": 10}})
	}
	saveRun := func(sec int, entries ...agent.LogEntry) string {
		started := at.Add(time.Duration(sec) * time.Second)
		info := store.RunInfo{ID: store.NewRunID(started), Title: fmt.Sprint("Run ", sec), Agent: agent.Claude, Started: started}
		writer, err := app.store.CreateRun(app.rev.Repo, app.rev.Scope, info)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if err := writer.Write(e); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return info.ID
	}
	entry := func(kind agent.LogKind, text string) agent.LogEntry { return agent.LogEntry{Kind: kind, Text: text} }
	parentID := saveRun(0, entry(agent.LogCommand, "claude -p"), entry(agent.LogStdout, result("p", 0.5)), entry(agent.LogExit, "Exited with status 0"))
	// Older logs say Stopped when the app cancelled a run after its result. Such a run ended normally.
	childID := saveRun(10, entry(agent.LogCommand, "claude -p --resume p"), entry(agent.LogStdout, result("c", 0.6)), entry(agent.LogExit, "Stopped"))
	stoppedID := saveRun(20, entry(agent.LogCommand, "claude -p"), entry(agent.LogExit, "Stopped"))

	byID := map[string]*logRun{}
	for _, r := range app.logsOf(app.rev) {
		byID[r.info.ID] = r
	}
	parent, child, stopped := byID[parentID], byID[childID], byID[stoppedID]
	app.loadRun(app.rev, child)
	if child.info.Usage == nil || child.info.Usage.CostUSD != 0 {
		t.Fatalf("cost before the resumed run is read: %+v", child.info.Usage)
	}
	app.loadRun(app.rev, parent)
	if usage := child.info.Usage; math.Abs(usage.CostUSD-0.1) > 1e-9 || !strings.Contains(usage.String(), "$0.1000 at API prices") {
		t.Errorf("child usage = %+v, %q", usage, usage.String())
	}
	if usage := parent.info.Usage; usage.CostUSD != 0.5 {
		t.Errorf("parent cost = %v", usage.CostUSD)
	}
	app.loadRun(app.rev, stopped)
	for r, want := range map[*logRun]string{parent: "", child: "", stopped: "stopped"} {
		if got := r.status(); got != want {
			t.Errorf("%s: status %q, want %q", r.info.Title, got, want)
		}
	}
}

func TestReviewFollowsBranch(t *testing.T) {
	app := newTestApp(t)
	dir := app.repo.Root
	git(t, dir, "stash", "-u", "-q")
	git(t, dir, "checkout", "-q", "-b", "agent")
	write(t, dir, "feature.go", "package feature\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "Add feature")
	app.openRepo(dir)
	app.cmpr.base = "main"
	branchScope, ok := app.cmpr.branchScope()
	if !ok {
		t.Fatal("no branch scope")
	}
	app.setScope(branchScope)
	rev := app.rev

	git(t, dir, "checkout", "-q", "main")
	app.refresh()
	if app.cmpr.branch != "main" || app.scope.Head != "agent" || app.rev != rev {
		t.Fatalf("after checkout of main: branch %q, scope %+v, same review %v", app.cmpr.branch, app.scope, app.rev == rev)
	}
	if len(app.files) != 1 || app.files[0].Path != "feature.go" {
		t.Errorf("files = %+v", app.files)
	}

	app.setScope(gitx.Scope{Kind: gitx.ScopeUncommitted})
	app.compare("agent", "main")
	if app.rev != rev {
		t.Error("Compare opened another review of the same branch")
	}

	git(t, dir, "checkout", "-q", "agent")
	app.refresh()
	if app.scope.Branch != "agent" || app.scope.Head != "" || app.rev != rev || app.rev.Scope != app.scope {
		t.Fatalf("after checkout of agent: scope %+v, same review %v", app.scope, app.rev == rev)
	}
}

func TestRefreshListsNewCommits(t *testing.T) {
	app := newTestApp(t)
	dir := app.repo.Root
	before := len(app.commits.rows)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "Commit the work")
	app.refresh()
	if len(app.commits.rows) != before+1 || app.commits.rows[0].Subject != "Commit the work" {
		t.Fatalf("commits after refresh = %+v", app.commits.rows)
	}
}

// waitText pumps the app until the window shows a text containing text.
func waitText(t *testing.T, app *app, tt *ui.Tester, text string) {
	t.Helper()
	pump(t, app, fmt.Sprintf("the text %q", text), func() bool {
		tt.Frame()
		return tt.HasText(text)
	})
}

// revSHA is the commit that rev names in the repository at dir.
func revSHA(t *testing.T, dir, rev string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", rev).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// pump runs the app's queued updates on the test goroutine until ok.
// waitLoad runs the queued updates until the latest load of a scope
// landed.
func waitLoad(t *testing.T, app *app, what string) {
	t.Helper()
	pump(t, app, what, app.loader.idle)
}

func pump(t *testing.T, app *app, what string, ok func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !ok() {
		select {
		case fn := <-app.queue:
			fn()
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func TestWalkthrough(t *testing.T) {
	app := newTestApp(t)
	app.rev.Overview = &review.Overview{Agent: agent.Claude, Sessions: review.Sessions{agent.Claude: "root"}, Generated: time.Now(), Walk: &review.Walkthrough{
		Summary: "Cache keys are lower-cased now.",
		Steps: []review.Step{
			{Title: "Lower-case keys", Text: "`Get` lower-cases its key.", Refs: []review.Ref{
				{File: "cache/cache.go", Side: review.SideNew, Start: 5, End: 7, Why: "the new call"},
				{File: "README.md", Side: review.SideNew, Start: 1, End: 1, Why: "unchanged file"},
				{File: "cache/cache.go", Side: review.SideNew, Start: 90, End: 91},
			}},
			{Title: "New handler", Text: "A stub."},
		},
		Sections: []review.Section{{Title: "Checklist", Items: []review.Step{{Title: "What data changes?", Text: "None."}}}},
	}}
	deeper := "```json\n" + `{"summary": "Inside Get.", "steps": [{"title": "ToLower call", "text": "Uses strings.", "refs": [{"file": "cache/cache.go", "start": 6, "end": 6}]}]}` + "\n```"
	fake := newFakeAgent(t, app)
	fake.answer("child", deeper)

	tt := ui.NewTester(app.view, 1300, 1000)
	tt.Advance(time.Second)
	for _, want := range []string{"Cache keys are lower-cased now.", "1. Lower-case keys", "cache/cache.go:5-7 (+)", "\treturn strings.ToLower(key)", "# Demo", "The file has 7 lines", "Checklist", "What data changes?"} {
		if !tt.HasText(strings.ReplaceAll(want, "\t", "    ")) {
			t.Errorf("view lacks %q", want)
		}
	}
	snapshot(t, tt, "walkthrough")

	app.queue = make(chan func(), 64)
	if err := tt.Click("Go deeper"); err != nil {
		t.Fatal(err)
	}
	walk := app.rev.Walks[0]
	if walk.Parent != "" || walk.Step != 0 || app.nav.page != (page{kindWalk, walk.ID}) {
		t.Fatalf("walk = %+v, page %q", walk, app.nav.page.String())
	}
	pump(t, app, "the deeper walkthrough", func() bool { return app.runner.run(app.rev, walk.ID) == nil })
	if walk.Err != "" || walk.Sessions[agent.Claude] != "child" || walk.Content == nil || walk.Content.Steps[0].Title != "ToLower call" {
		t.Fatalf("walk = %+v", walk)
	}
	if req := fake.last(); !strings.Contains(req.Prompt, "### Lower-case keys") || req.ResumeID != "root" || !req.Fork {
		t.Errorf("request = %+v", req)
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
	if app.nav.page != (page{kindFile, "cache/cache.go"}) {
		t.Errorf("page = %q", app.nav.page.String())
	}
	if selection, ok := app.diffs["cache/cache.go"].state.SelectedLines(); !ok || selection.Start != 5 || selection.End != 5 {
		t.Errorf("selection = %+v, %v", selection, ok)
	}
}

func TestDeeperAhead(t *testing.T) {
	app := newTestApp(t)
	answer := "```json\n" + `{"summary": "s", "steps": [{"title": "A", "text": "a"}, {"title": "B", "text": "b"}, {"title": "C", "text": "c"}, {"title": "D", "text": "d"}], "groups": []}` + "\n```"
	newFakeAgent(t, app).answer("s1", answer)
	app.cfg.Agent = agent.Config{Provider: agent.Claude, Effort: "high", AutoEffort: "low"}
	app.queue = make(chan func(), 256)
	app.generateOverview()
	pump(t, app, "the overview", func() bool { return !app.busy(store.TargetOverview) })
	if len(app.rev.Walks) != 4 || app.runner.run(app.rev, app.rev.Walks[2].ID) == nil || !app.runner.queued(app.rev, app.rev.Walks[3].ID) {
		t.Fatalf("%d walks, the third runs %v, the fourth waits %v", len(app.rev.Walks), app.runner.run(app.rev, app.rev.Walks[2].ID) != nil, app.runner.queued(app.rev, app.rev.Walks[3].ID))
	}
	for i, w := range app.rev.Walks {
		if w.Step != i || w.Question != "" {
			t.Errorf("walk %d = %+v", i, w)
		}
	}
	pump(t, app, "the deeper walkthroughs", func() bool {
		for _, r := range app.logsOf(app.rev) {
			if r.running {
				return false
			}
		}
		return !app.runner.reviewBusy(app.rev)
	})
	for _, w := range app.rev.Walks {
		if w.Content == nil || w.Sessions[agent.Claude] != "s1" {
			t.Errorf("walk = %+v", w)
		}
	}

	runs := app.store.Runs(app.repo.Root, app.scope)
	if len(runs) != 5 {
		t.Fatalf("%d runs", len(runs))
	}
	// The fake answers every run in session s1 at a session cost of $0.25,
	// so a deeper run, which resumes s1, adds nothing to the cost.
	want := agent.Usage{Input: 10, Output: 1000, CacheRead: 20000, CacheWrite: 5000, Context: 24000, Session: "s1", SessionCost: 0.25}
	for _, r := range runs {
		expected := want
		if r.Target == store.TargetOverview {
			expected.CostUSD = 0.25
		}
		if r.Usage == nil || *r.Usage != expected {
			t.Errorf("usage of %s = %+v", r.Title, r.Usage)
		}
		if r.Effort != "low" {
			t.Errorf("%s ran at %q effort, want the effort for automatic runs", r.Title, r.Effort)
		}
	}
	tt := ui.NewTester(app.view, 1400, 900)
	app.showLog("")
	tt.Frame()
	// The deeper runs fork the overview's session and add nothing to it.
	if !tt.HasText("24k tokens of context") {
		t.Errorf("log lacks the total; texts %q", tt.Texts())
	}
	if !tt.HasText("· 24k tokens") {
		t.Errorf("log lacks the context of a run; texts %q", tt.Texts())
	}
	snapshot(t, tt, "deeper-ahead")

	app.askQuestion("", -1, "Why?")
	pump(t, app, "the question", func() bool { return !app.runner.reviewBusy(app.rev) })
	if run := app.logsOf(app.rev)[0]; run.info.Effort != "high" {
		t.Errorf("a question ran at %q effort, want the main one", run.info.Effort)
	}
}

// TestDeleteOpenWalkLeavesNoHistory deletes the open walk: its parent
// opens, and Back leads past the parent to the page before it.
func TestWalkRowShowsWhetherTheLatestAnswerFailed(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  string
		more []review.Turn
		mark string
	}{
		{name: "a failed answer", err: "boom", mark: "failed"},
		{name: "a failed follow-up after a good answer", more: []review.Turn{{Question: "More?", Err: "boom"}}, mark: "failed"},
		{name: "a good follow-up after a failed answer", err: "boom", more: []review.Turn{{Question: "More?", Markdown: "Fine."}}},
		{name: "a stopped answer", err: review.Stopped, mark: "stopped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp(t)
			app.rev.Overview = &review.Overview{Agent: agent.Claude, Generated: time.Now(), Walk: &review.Walkthrough{
				Summary: "s", Steps: []review.Step{{Title: "Lower-case keys", Text: "t"}},
			}}
			app.rev.Walks = append(app.rev.Walks, &review.Walk{ID: "q", Step: 0, Question: "Why?", Err: tc.err, More: tc.more})
			tt := ui.NewTester(app.view, 1300, 800)
			tt.Frame()
			if !tt.HasText("Why?") {
				t.Fatalf("texts %q", tt.Texts())
			}
			for _, mark := range []string{"failed", "stopped"} {
				if got := tt.HasText(mark); got != (mark == tc.mark) {
					t.Errorf("shows %q = %v, want %v", mark, got, mark == tc.mark)
				}
			}
			snapshot(t, tt, "walk-row-"+strings.ReplaceAll(tc.name, " ", "-"))
		})
	}
}

// TestThreadRowShowsWhetherTheLatestAnswerFailed lists a thread on the
// page of questions and comments. Its row tells a failed answer from a
// stopped one.
func TestThreadRowShowsWhetherTheLatestAnswerFailed(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  string
		mark string
	}{
		{name: "a failed answer", err: "boom", mark: "failed"},
		{name: "a stopped answer", err: review.Stopped, mark: "stopped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp(t)
			th := app.rev.AddThread(review.Anchor{Kind: review.AnchorGeneral})
			th.Messages = []review.Message{{Role: review.RoleUser, Text: "Why?"}, {Role: review.RoleAgent, Err: tc.err}}
			app.nav.goTo(page{kind: kindThreads})
			tt := ui.NewTester(app.view, 1300, 800)
			tt.Frame()
			if !tt.HasText("Why?") {
				t.Fatalf("texts %q", tt.Texts())
			}
			for _, mark := range []string{"failed", "stopped"} {
				if got := tt.HasText(mark); got != (mark == tc.mark) {
					t.Errorf("shows %q = %v, want %v", mark, got, mark == tc.mark)
				}
			}
			snapshot(t, tt, "thread-row-"+tc.mark)
		})
	}
}

// TestUnseenAnswerMarksItsRows asks in a thread that is not open. When
// the answer comes, the thread and its scope stay unseen until the user
// opens the thread.
func TestUnseenAnswerMarksItsRows(t *testing.T) {
	app := newTestApp(t)
	app.queue = make(chan func(), 64)
	fake := newFakeAgent(t, app)
	fake.answer("t1", "Because.")
	th := app.rev.AddThread(review.Anchor{Kind: review.AnchorGeneral})
	app.ask(app.rev, th, "Why?")
	pump(t, app, "the answer", func() bool { return !app.busy(th.ID) })
	if got := app.state(th.ID); got != (targetState{status: targetAnswered, unseen: true}) {
		t.Fatalf("state after the answer = %+v", got)
	}
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	snapshot(t, tt, "unseen-thread")
	if !app.targets.unseenIn(app.rev) {
		t.Error("the scope of the thread is not unseen")
	}
	app.openThread(th.ID)
	tt.Frame()
	if app.state(th.ID).unseen || app.targets.unseenIn(app.rev) {
		t.Error("the open thread stays unseen")
	}
}

func TestDeleteOpenWalkLeavesNoHistory(t *testing.T) {
	app := newTestApp(t)
	parent := app.rev.AddWalk("", 0, "")
	child := app.rev.AddWalk(parent.ID, 0, "")
	app.nav.goTo(page{kindFile, "cache/cache.go"})
	app.nav.goTo(page{kindWalk, parent.ID})
	app.nav.goTo(page{kindWalk, child.ID})
	app.deleteWalk(child)
	if app.nav.page != (page{kindWalk, parent.ID}) || slices.Contains(app.nav.history, page{kindWalk, child.ID}) {
		t.Fatalf("page %+v, history %+v", app.nav.page, app.nav.history)
	}
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	if err := tt.Click("‹ Back to cache.go"); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	if app.nav.page != (page{kindFile, "cache/cache.go"}) {
		t.Errorf("back opened %+v", app.nav.page)
	}
}

// TestDeleteWalkForgetsItsPagesInHistory deletes a walk while a walk under
// it is in the history: both leave the history, and the back bar names
// the page Back opens.
func TestDeleteWalkForgetsItsPagesInHistory(t *testing.T) {
	app := newTestApp(t)
	parent := app.rev.AddWalk("", 0, "")
	child := app.rev.AddWalk(parent.ID, 0, "")
	app.nav.goTo(page{kindFile, "cache/cache.go"})
	app.nav.goTo(page{kindWalk, parent.ID})
	app.nav.goTo(page{kindWalk, child.ID})
	app.nav.goTo(page{kindWalk, parent.ID})
	app.deleteWalk(parent)
	if app.nav.page != overview || !slices.Equal(app.nav.history, []page{overview, {kindFile, "cache/cache.go"}}) {
		t.Fatalf("page %+v, history %+v", app.nav.page, app.nav.history)
	}
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	if !tt.HasText("‹ Back to cache.go") {
		t.Errorf("back bar: %q", tt.Texts())
	}
}

func TestBackFromSnippet(t *testing.T) {
	app := newTestApp(t)
	var steps []review.Step
	for i := range 8 {
		steps = append(steps, review.Step{Title: fmt.Sprintf("Step %d", i), Text: "Text.\n\nMore text.", Refs: []review.Ref{{File: "cache/cache.go", Side: review.SideNew, Start: 5, End: 7}}})
	}
	app.rev.Overview = &review.Overview{Agent: agent.Claude, Generated: time.Now(), Walk: &review.Walkthrough{Summary: "s", Steps: steps}}
	tt := ui.NewTester(app.view, 1300, 600)
	tt.Frame()
	app.scrollOf("overview").Y = 400
	tt.Frame()
	app.openRef(steps[3].Refs[0])
	if app.nav.page != (page{kindFile, "cache/cache.go"}) {
		t.Fatalf("page = %q", app.nav.page.String())
	}
	tt.Frame()
	if err := tt.Click("‹ Back to Overview"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.nav.page != overview {
		t.Errorf("page = %q", app.nav.page.String())
	}
	if offset := app.scrollOf("overview").Y; offset != 400 {
		t.Errorf("overview scrolled to %v, want 400", offset)
	}
}

func TestQuestionPanel(t *testing.T) {
	app := newTestApp(t)
	app.rev.Overview = &review.Overview{Agent: agent.Claude, Sessions: review.Sessions{agent.Claude: "root"}, Generated: time.Now(), Walk: &review.Walkthrough{
		Summary: "s", Steps: []review.Step{{Title: "Lower-case keys", Text: "t"}},
	}}
	reply := "```json\n" + `{"summary": "Callers pass mixed case.", "steps": [{"title": "Caller", "text": "x", "refs": [{"file": "cache/cache.go", "start": 6, "end": 6}]}]}` + "\n```"
	fake := newFakeAgent(t, app)
	fake.answer("q1", reply)
	tt := ui.NewTester(app.view, 1400, 900)
	tt.Frame()

	if err := tt.Click("Ask about this step"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("About: Lower-case keys") || app.nav.page != overview {
		t.Fatalf("panel not open; page %q", app.nav.page.String())
	}
	app.queue = make(chan func(), 64)
	tt.Type("Why lower-case?")
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyEnter)
	walk := app.rev.Walk(app.questionID())
	if walk == nil || walk.Question != "Why lower-case?" || walk.Step != 0 || app.nav.page != overview {
		t.Fatalf("question = %+v, page %q", walk, app.nav.page.String())
	}
	pump(t, app, "the answer", func() bool { return !app.busy(walk.ID) })
	tt.Frame()
	for _, want := range []string{"Why lower-case?", "Callers pass mixed case.", "1. Caller", "cache/cache.go:6 (+)"} {
		if !tt.HasText(want) {
			t.Errorf("panel lacks %q", want)
		}
	}

	app.askMore(app.rev, walk, "And the tests?")
	pump(t, app, "the follow-up", func() bool { return !app.busy(walk.ID) })
	if len(walk.More) != 1 || walk.More[0].Content == nil || walk.Sessions[agent.Claude] != "q1" {
		t.Fatalf("follow-up = %+v", walk.More)
	}
	if req := fake.last(); !strings.Contains(req.Prompt, "follow-up question: And the tests?") || strings.Contains(req.Prompt, "The change set under review") || req.ResumeID != "q1" || req.Fork {
		t.Errorf("follow-up request = %+v", req)
	}
	tt.Frame()
	snapshot(t, tt, "question")

	app.closeQuestion()
	app.nav.goTo(page{kind: kindThreads})
	tt.Frame()
	if !tt.HasText("1 follow-ups") && !tt.HasText("Jan") {
		t.Logf("texts %q", tt.Texts())
	}
	if err := tt.Click("Why lower-case?"); err != nil {
		t.Fatal(err)
	}
	if app.questionID() != walk.ID {
		t.Errorf("question = %q", app.questionID())
	}
}

// TestLeavingReviewClosesAskPanel leaves a review while its Ask panel is
// open: the panel closes, and a question sent after that goes nowhere.
func TestLeavingReviewClosesAskPanel(t *testing.T) {
	for _, tc := range []struct {
		name  string
		leave func(app *app)
	}{
		{"scope switch", func(a *app) {
			a.setScope(gitx.Scope{Kind: gitx.ScopeCommit, Commit: "HEAD"})
			waitLoad(t, a, "the commit")
		}},
		{"start over", func(a *app) { a.startOver() }},
		{"archive", func(a *app) {
			archiveOnReload(t, a)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp(t)
			fake := newFakeAgent(t, app)
			fake.answer("q1", "Because.")
			app.queue = make(chan func(), 64)
			left := app.rev
			left.Overview = &review.Overview{Agent: agent.Claude, Generated: time.Now(), Walk: &review.Walkthrough{
				Summary: "s", Steps: []review.Step{{Title: "Lower-case keys", Text: "t"}},
			}}
			app.saveReview()
			tt := ui.NewTester(app.view, 1400, 900)
			tt.Frame()
			if err := tt.Click("Ask about this step"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			tt.Type("Why lower-case?")
			tt.Frame()

			tc.leave(app)
			tt.Frame()
			tt.Key(ui.Cmd, ui.KeyEnter)
			tt.Frame()
			if app.questionOpen() || tt.HasText("About: Lower-case keys") {
				t.Error("the Ask panel stayed open")
			}
			if len(left.Walks) != 0 || len(app.rev.Walks) != 0 || len(fake.received()) != 0 {
				t.Errorf("questions: %d in the left review, %d in the new one, %d sent", len(left.Walks), len(app.rev.Walks), len(fake.received()))
			}
		})
	}
}

// archiveOnReload reloads the uncommitted changes after it marks the
// overview as written for a file that is no longer changed, so the
// review goes to the archive.
func archiveOnReload(t *testing.T, app *app) {
	t.Helper()
	app.rev.Overview.Files = map[string]string{"gone.go": "x"}
	app.reload()
	pump(t, app, "the archive", func() bool { return len(app.reviews.Earlier()) == 1 })
}

// TestArchiveDropsSavedNote archives a review whose feedback was saved:
// the feedback page of the new review shows no "Saved to" note.
func TestArchiveDropsSavedNote(t *testing.T) {
	app := newTestApp(t)
	app.rev.Overview = &review.Overview{Agent: agent.Claude, Generated: time.Now(), Markdown: "x"}
	app.saveReview()
	app.savedPath = "/tmp/feedback.md"
	app.nav.page = page{kind: kindFeedback}
	tt := ui.NewTester(app.view, 1400, 900)
	tt.Frame()
	if !tt.HasText("Saved to /tmp/feedback.md") {
		t.Fatal("the note does not show before archiving")
	}
	archiveOnReload(t, app)
	tt.Frame()
	if tt.HasText("Saved to") {
		t.Error("the feedback page kept the Saved to note of the archived review")
	}
}

// TestStartOverResetsExport starts a review over after its feedback was
// saved with resolved threads: the feedback page of the new review leaves
// them out and shows no "Saved to" note.
func TestStartOverResetsExport(t *testing.T) {
	app := newTestApp(t)
	app.exportDone, app.savedPath = true, "/tmp/feedback.md"
	app.nav.page = page{kind: kindFeedback}
	app.startOver()
	if app.exportDone || app.savedPath != "" || app.nav.page != overview {
		t.Errorf("after Start Over: resolved %v, saved %q, page %q", app.exportDone, app.savedPath, app.nav.page.String())
	}
}

// TestScopeSwitchKeepsGroupsFolded closes a group of files and switches
// scope and back: the group stays closed.
func TestScopeSwitchKeepsGroupsFolded(t *testing.T) {
	app := newTestApp(t)
	id := app.groupID(0)
	*app.groupOpen(id) = false
	app.setScope(gitx.Scope{Kind: gitx.ScopeCommit, Commit: "HEAD"})
	app.setScope(gitx.Scope{Kind: gitx.ScopeUncommitted})
	if *app.groupOpen(id) {
		t.Errorf("group %q opened again after a scope switch", id)
	}
}

// TestSidePanelShowsOne opens the Ask panel, a question and a thread in
// turn: each one closes the one before.
func TestSidePanelShowsOne(t *testing.T) {
	app := newTestApp(t)
	app.rev.Overview = &review.Overview{Agent: agent.Claude, Generated: time.Now(), Walk: &review.Walkthrough{
		Summary: "s", Steps: []review.Step{{Title: "One", Text: "t"}},
	}}
	question := app.rev.AddWalk("", 0, "Why?")
	th := app.rev.AddThread(review.Anchor{Kind: review.AnchorFile, File: "cache/cache.go"})
	app.askAbout("", 0)
	app.openThread(th.ID)
	if app.questionOpen() || app.threadID() != th.ID {
		t.Errorf("after the thread: question open %v, thread %q", app.questionOpen(), app.threadID())
	}
	app.openQuestion(question.ID)
	if !app.questionOpen() || app.threadID() != "" {
		t.Errorf("after the question: question open %v, thread %q", app.questionOpen(), app.threadID())
	}
}

// TestLeavingReviewDropsEmptyThread opens a thread on a line, writes
// nothing, and switches scope and back: the thread is gone from the
// review, its file and its saved copy.
func TestLeavingReviewDropsEmptyThread(t *testing.T) {
	app := newTestApp(t)
	first := app.rev
	app.nav.page = page{kindFile, "cache/cache.go"}
	app.lineThread("cache/cache.go", lineRange(5, 5))
	app.saveReview()
	app.setScope(gitx.Scope{Kind: gitx.ScopeCommit, Commit: "HEAD"})
	app.setScope(gitx.Scope{Kind: gitx.ScopeUncommitted})
	if app.rev != first {
		t.Fatal("switching back loaded a second copy of the review")
	}
	if len(first.Threads) != 0 || first.ThreadCounts()["cache/cache.go"] != 0 || !first.Empty() {
		t.Errorf("threads = %d, counts %v", len(first.Threads), first.ThreadCounts())
	}
	if saved := app.store.LoadReview(app.repo.Root, app.scope); len(saved.Threads) != 0 {
		t.Errorf("saved threads = %d", len(saved.Threads))
	}

	app.lineThread("cache/cache.go", lineRange(5, 5))
	app.comment(app.rev.Thread(app.threadID()), "Why lower-case?")
	app.setScope(gitx.Scope{Kind: gitx.ScopeCommit, Commit: "HEAD"})
	app.setScope(gitx.Scope{Kind: gitx.ScopeUncommitted})
	if len(first.Threads) != 1 {
		t.Errorf("leaving the review dropped a thread with a comment: %d threads", len(first.Threads))
	}
}

// TestDeleteWalkClosesItsAskPanel deletes the parent of the walk the Ask
// panel asks about, or of the question the panel shows: the panel closes.
// Deleting another walk keeps it.
func TestDeleteWalkClosesItsAskPanel(t *testing.T) {
	app := newTestApp(t)
	parent := app.rev.AddWalk("", 0, "")
	child := app.rev.AddWalk(parent.ID, 1, "")
	other := app.rev.AddWalk("", 1, "")
	app.askAbout(other.ID, 0)
	app.deleteWalk(app.rev.AddWalk("", 2, ""))
	if !app.questionOpen() {
		t.Fatal("deleting another walk closed the Ask panel")
	}
	app.askAbout(child.ID, 0)
	app.deleteWalk(parent)
	if app.questionOpen() {
		t.Error("the Ask panel of a deleted walk stayed open")
	}
	parent = app.rev.AddWalk("", 0, "")
	app.openQuestion(app.rev.AddWalk(parent.ID, 0, "Why?").ID)
	app.deleteWalk(parent)
	if app.questionID() != "" {
		t.Error("the panel of a deleted question stayed open")
	}
}

// TestRegenerateClosesAskPanelOfItsStepWalk regenerates a walk while the
// Ask panel asks about a step of a deeper walk under it: that deeper walk
// goes, and the panel closes.
func TestRegenerateClosesAskPanelOfItsStepWalk(t *testing.T) {
	app := newTestApp(t)
	newFakeAgent(t, app).answer("w1", "Because.")
	app.queue = make(chan func(), 64)
	parent := app.rev.AddWalk("", 0, "")
	deeper := app.rev.AddWalk(parent.ID, 0, "")
	app.askAbout(deeper.ID, 0)
	app.regenerate(parent.ID)
	pump(t, app, "the regenerated walk", func() bool { return !app.busy(parent.ID) })
	if app.rev.Walk(deeper.ID) != nil || app.questionOpen() {
		t.Errorf("deeper walk kept %v, panel open %v", app.rev.Walk(deeper.ID) != nil, app.questionOpen())
	}
}

// TestQuestionDraftsPerTarget types in the Ask panel of two steps and in
// the reply box of two questions: each text comes back with its target.
func TestQuestionDraftsPerTarget(t *testing.T) {
	app := newTestApp(t)
	app.rev.Overview = &review.Overview{Agent: agent.Claude, Generated: time.Now(), Walk: &review.Walkthrough{
		Summary: "s", Steps: []review.Step{{Title: "One", Text: "t"}, {Title: "Two", Text: "t"}},
	}}
	q1 := app.rev.AddWalk("", 0, "First?")
	q2 := app.rev.AddWalk("", 1, "Second?")
	tt := ui.NewTester(app.view, 1400, 900)
	// shown is the text of the focused box, read through the clipboard.
	shown := func(open func()) string {
		t.Helper()
		open()
		tt.Frame()
		tt.SetClipboard("")
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		tt.Frame()
		return tt.Clipboard()
	}
	targets := []struct {
		open func()
		text string
	}{
		{func() { app.askAbout("", 0) }, "draft of step one"},
		{func() { app.askAbout("", 1) }, "draft of step two"},
		{func() { app.openQuestion(q1.ID) }, "reply to the first"},
		{func() { app.openQuestion(q2.ID) }, "reply to the second"},
	}
	for _, tc := range targets {
		if got := shown(tc.open); got != "" {
			t.Fatalf("before %q was typed, the box shows %q", tc.text, got)
		}
		tt.Type(tc.text)
		tt.Frame()
	}
	for _, tc := range targets {
		if got := shown(tc.open); got != tc.text {
			t.Errorf("the box shows %q, want %q", got, tc.text)
		}
	}
}

func TestKeyboardNavigation(t *testing.T) {
	app := newTestApp(t)
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()

	app.stepFile(1)
	first := app.nav.page
	app.stepFile(1)
	second := app.nav.page
	app.stepFile(1)
	if first != (page{kindFile, "api/handler.go"}) || second != (page{kindFile, "cache/cache.go"}) || app.nav.page != second {
		t.Errorf("files: %v, %v, then %v", first, second, app.nav.page)
	}
	app.nav.goTo(page{kind: kindFeedback})

	app.newThread(review.Anchor{Kind: review.AnchorFile, File: "cache/cache.go"})
	app.rev.Thread(app.threadID()).Messages = []review.Message{{Role: review.RoleUser, Text: "x"}}
	app.toggleLog()
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	if app.threadID() != "" || !app.logOpen {
		t.Errorf("Escape: thread %q, log %v", app.threadID(), app.logOpen)
	}
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	if app.logOpen {
		t.Error("Escape left the log open")
	}
	tt.Key(0, ui.KeyBack)
	if app.nav.page != second {
		t.Errorf("mouse back = %q", app.nav.page.String())
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
	walk(app.menu().Items())
	if seen["CmdOrCtrl+["] != "Back" || seen["CmdOrCtrl+P"] != "Switch Project…" || seen["CmdOrCtrl+B"] != "Switch Branch…" {
		t.Errorf("shortcuts = %v", seen)
	}
}

func TestPickersFromKeyboard(t *testing.T) {
	app := newTestApp(t)
	dir := app.repo.Root
	for _, b := range []string{"agent", "agent-two", "docs"} {
		git(t, dir, "branch", b)
	}
	other := fixtureRepo(t)
	app.cfg.AddRecent(other)
	app.openRepo(dir)
	tt := ui.NewTester(app.view, 1300, 800)
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
	if app.branchesOpen || app.cmpr.head != "agent-two" {
		t.Fatalf("open %v, head %q", app.branchesOpen, app.cmpr.head)
	}
	if !tt.Focused("Branch") {
		t.Error("the focus did not go back to the branch button")
	}

	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if !app.branchesOpen || app.branchNav.hi != slices.Index(app.cmpr.branches, "agent-two") {
		t.Fatalf("Down on the button: open %v, highlight %d", app.branchesOpen, app.branchNav.hi)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if app.branchesOpen {
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
	if app.projectsOpen || !strings.HasSuffix(app.repo.Root, other) {
		t.Fatalf("open %v, repo %q, want %q", app.projectsOpen, app.repo.Root, other)
	}
}

// TestPickListScrollKeepsHighlight scrolls the branch list from the
// keyboard while the pointer rests on a row.
func TestPickListScrollKeepsHighlight(t *testing.T) {
	app := newTestApp(t)
	for i := range 30 {
		git(t, app.repo.Root, "branch", fmt.Sprintf("b%02d", i))
	}
	app.openRepo(app.repo.Root)
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	if err := tt.Click("Branch"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	row, ok := tt.Find("b25")
	if !ok || row.H == 0 {
		t.Fatalf("no row b25: %v", row)
	}
	tt.Move(row.X+row.W/2, row.Y+row.H/2)
	tt.Frame()
	if got := app.cmpr.branches[app.branchNav.hi]; got != "b25" {
		t.Fatalf("the pointer left the highlight on %q", got)
	}
	for range 20 {
		tt.Key(0, ui.KeyUp)
		tt.Frame()
	}
	if got := app.cmpr.branches[app.branchNav.hi]; got != "b05" {
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
	app := newTestApp(t)
	app.rev.Overview = &review.Overview{Markdown: "x", Groups: []review.Group{
		{Title: "Cache keys", Files: []string{"cache/cache.go"}}, {Title: "API", Files: []string{"api/handler.go"}},
	}}
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	if !shows(tt, "Cache keys") || app.fileOrder()[0] != "cache/cache.go" {
		t.Fatalf("grouped: %q", tt.Texts())
	}
	if err := tt.Click("Flat"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	snapshot(t, tt, "flat-files")
	if shows(tt, "Cache keys") || !tt.HasText("api") || !app.store.LoadConfig().FlatFiles {
		t.Errorf("flat: %q", tt.Texts())
	}
	app.stepFile(1)
	if app.nav.page != (page{kindFile, "api/handler.go"}) {
		t.Errorf("first file in flat order: %q", app.nav.page.String())
	}
}

// TestFlatFileRows shows the whole file name in a narrow sidebar, with a
// long folder on a second line below it, and picks the file when its first
// letters are typed.
func TestFlatFileRows(t *testing.T) {
	app := newTestApp(t)
	const (
		dir  = "astrobook/src/stories/07-data-display"
		name = "TwoColumnsSample.tsx"
	)
	write(t, app.repo.Root, dir+"/"+name, "export const TwoColumnsSample = 1\n")
	write(t, app.repo.Root, "README.md", "# Demo\n\nMore.\n")
	app.openRepo(app.repo.Root)
	app.cfg.FlatFiles = true
	app.sidebarW = 220
	app.cmd("mark-viewed").run(nil, "api/handler.go")
	app.nav.goTo(page{kindFile, "cache/cache.go"})
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	if row, ok := tt.Find("README.md"); ok {
		tt.Move(row.X+row.W/2, row.Y+row.H/2)
		tt.Frame()
	}
	snapshot(t, tt, "flat-files-narrow")

	nameBox, ok := tt.Find(name)
	folderBox, ok2 := tt.Find(dir)
	if !ok || !ok2 {
		t.Fatalf("texts %q", tt.Texts())
	}
	if full := textWidth(name); nameBox.W < full-0.01 {
		t.Errorf("name cut to %.1f of %.1f", nameBox.W, full)
	}
	if folderBox.Y < nameBox.Y+nameBox.H-0.5 || math.Abs(float64(folderBox.X-nameBox.X)) > 0.5 {
		t.Errorf("folder at %v, name at %v", folderBox, nameBox)
	}
	if shows(tt, ".") {
		t.Errorf("a root file shows its folder: %q", tt.Texts())
	}

	if err := tt.Click("README.md"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	snapshot(t, tt, "flat-files-narrow-focused")
	tt.Key(0, ui.KeyT)
	tt.Key(0, ui.KeyW)
	tt.Frame()
	snapshot(t, tt, "flat-files-narrow-typed")
	if app.nav.page != (page{kindFile, dir + "/" + name}) {
		t.Errorf("typing the name chose %q", app.nav.page.String())
	}
}

// textWidth is the width of s alone, in the default font.
func textWidth(text string) float32 {
	tt := ui.NewTester(func(c *ui.Context) { ui.Row(c).Children(func() { ui.Text(c, text) }) }, 800, 100)
	tt.Frame()
	bounds, _ := tt.Find(text)
	return bounds.W
}

// shows tells whether the last frame shows exactly the text s.
func shows(tt *ui.Tester, text string) bool { return slices.Contains(tt.Texts(), text) }

// TestSelectLineRange selects lines by their numbers: a click, then a
// Shift+click.
func TestSelectLineRange(t *testing.T) {
	app := newTestApp(t)
	app.nav.page = page{kindFile, "api/handler.go"}
	tt := ui.NewTester(app.view, 1300, 800)
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
		if selection, ok := app.diffs["api/handler.go"].state.SelectedLines(); !ok || selection.Start != 0 || selection.End != 2 {
			t.Fatalf("selected %+v, %v", selection, ok)
		}
	}
	check := func(how string) {
		t.Helper()
		th := app.rev.Thread(app.threadID())
		if th == nil || th.Anchor.Start != 1 || th.Anchor.End != 3 {
			t.Errorf("thread from %s = %+v", how, th)
		}
		if _, ok := app.diffs["api/handler.go"].state.SelectedLines(); ok {
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

	app.panel = nil
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
	app := newTestApp(t)
	app.rev.Overview = &review.Overview{Markdown: "x", Groups: []review.Group{
		{Title: "Cache keys", Files: []string{"cache/cache.go"}}, {Title: "API", Files: []string{"api/handler.go"}},
	}}
	app.nav.page = page{kindGroup, "Cache keys"}
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	if err := tt.Click("Mark all as viewed"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !app.rev.Viewed["cache/cache.go"] || !tt.HasText("Mark all as not viewed") {
		t.Fatalf("viewed = %v", app.rev.Viewed)
	}
	if err := tt.Click("Hide viewed files"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	snapshot(t, tt, "hide-viewed")
	if shows(tt, "Cache keys") || !shows(tt, "API") || !app.store.LoadConfig().HideViewed {
		t.Errorf("hidden: %q", tt.Texts())
	}
	app.stepFile(1)
	if app.nav.page != (page{kindFile, "api/handler.go"}) {
		t.Errorf("next file skips the hidden one: %q", app.nav.page.String())
	}

	// On a group page, a file marked viewed leaves the sidebar.
	app.nav.goTo(page{kindGroup, "API"})
	tt.Frame()
	if err := tt.Click("Viewed"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if shows(tt, "handler.go") || shows(tt, "API") {
		t.Errorf("group page: %q", tt.Texts())
	}
	app.rev.Viewed["api/handler.go"] = false

	// The open file leaves the sidebar too when it is marked viewed.
	app.nav.goTo(page{kindFile, "api/handler.go"})
	tt.Frame()
	if err := tt.Click("Viewed"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !app.rev.Viewed["api/handler.go"] || shows(tt, "API") || !tt.HasText("All files are viewed.") {
		t.Errorf("open file: %q", tt.Texts())
	}

	// Previous file steps from the hidden open file.
	app.rev.Viewed["cache/cache.go"] = false
	app.stepFile(-1)
	if app.nav.page != (page{kindFile, "cache/cache.go"}) {
		t.Errorf("previous file: %q", app.nav.page.String())
	}
	app.rev.Viewed["cache/cache.go"] = true
	tt.Frame()

	if err := tt.Click("Hide viewed files"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !shows(tt, "Cache keys") || !shows(tt, "API") {
		t.Errorf("shown again: %q", tt.Texts())
	}
}

// TestGroupItem opens a group from its title in the sidebar and shows
// and hides its files with the chevron.
func TestGroupItem(t *testing.T) {
	app := newTestApp(t)
	app.rev.Overview = &review.Overview{Markdown: "x", Groups: []review.Group{
		{Title: "Cache keys", Files: []string{"cache/cache.go"}}, {Title: "API", Files: []string{"api/handler.go"}},
	}}
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	snapshot(t, tt, "group-items")
	if tt.HasText("All files in group") || !shows(tt, "cache.go") {
		t.Fatalf("sidebar: %q", tt.Texts())
	}
	if err := tt.Click("Cache keys"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.nav.page != (page{kindGroup, "Cache keys"}) {
		t.Fatalf("page = %q", app.nav.page.String())
	}
	if err := tt.Click("Hide files"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.nav.page != (page{kindGroup, "Cache keys"}) || shows(tt, "cache.go") || !shows(tt, "handler.go") {
		t.Errorf("hidden: %q, %q", app.nav.page.String(), tt.Texts())
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
	app := newTestApp(t)
	git(t, app.repo.Root, "branch", "docs")
	app.openRepo(app.repo.Root)
	tt := ui.NewTester(app.view, 1300, 800)
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
		walk(app.menu().Items())
		tt.Frame()
	}

	click("Switch Branch…")
	if !app.branchesOpen {
		t.Fatal("branch list closed")
	}
	tt.Type("docs")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if app.branchesOpen || app.cmpr.head != "docs" {
		t.Errorf("open = %v, branch = %q", app.branchesOpen, app.cmpr.head)
	}

	click("Switch Project…")
	if !app.projectsOpen {
		t.Fatal("project list closed")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if app.projectsOpen {
		t.Error("Escape left the project list open")
	}
}

// TestFocusAndShortcuts moves the focus to the sidebars from the menu
// bar and lists the shortcuts in a dialog.
func TestFocusAndShortcuts(t *testing.T) {
	app := newTestApp(t)
	tt := ui.NewTester(app.view, 1300, 800)
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
		walk(app.menu().Items())
		tt.Frame()
		tt.Frame()
	}

	// Focus Files opens the first file, in the list of files only.
	click("Focus Files")
	if !tt.Focused("Files") || app.nav.page != (page{kindFile, "api/handler.go"}) {
		t.Errorf("files sidebar: focused %v, page %q", tt.Focused("Files"), app.nav.page.String())
	}
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	if app.nav.page != (page{kindGroup, "api"}) {
		t.Errorf("Up in the files sidebar: %q", app.nav.page.String())
	}
	// It keeps the file open.
	app.nav.goTo(page{kindFile, "cache/cache.go"})
	click("Focus Files")
	if app.nav.page != (page{kindFile, "cache/cache.go"}) {
		t.Errorf("open file: %q", app.nav.page.String())
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
	if app.shortcutsOpen {
		t.Error("Escape left the dialog open")
	}
}

// TestCommandPalette runs commands from the palette: only the ones that
// apply on the page show, and the ones used last come first.
func TestCommandPalette(t *testing.T) {
	app := newTestApp(t)
	app.nav.page = page{kindFile, "api/handler.go"}
	tt := ui.NewTester(app.view, 1300, 800)
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
		walk(app.menu().Items())
		tt.Frame()
		tt.Frame()
		if !app.paletteOpen {
			t.Fatal("palette closed")
		}
	}
	titles := func() []string {
		var out []string
		for _, cm := range app.paletteCommands() {
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
	if app.paletteOpen || !app.rev.Viewed["api/handler.go"] {
		t.Fatalf("open = %v, viewed = %v", app.paletteOpen, app.rev.Viewed)
	}
	if got := app.store.LoadConfig().RecentCommands; len(got) == 0 || got[0] != "mark-viewed" {
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
	if app.paletteOpen || !app.logOpen {
		t.Fatalf("open = %v, log = %v", app.paletteOpen, app.logOpen)
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
	before := app.store.LoadConfig().RecentCommands
	for _, it := range app.menu().Items() {
		for _, sub := range it.Submenu {
			if sub.Label == "Next File" {
				sub.Click(sub, nil)
			}
		}
	}
	tt.Frame()
	if got := app.store.LoadConfig().RecentCommands; !slices.Equal(got, before) {
		t.Errorf("menu changed recent: %q, was %q", got, before)
	}
	open()
	// Escape closes the palette, and only it.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if app.paletteOpen || !app.logOpen {
		t.Errorf("Escape: open = %v, log = %v", app.paletteOpen, app.logOpen)
	}

	app.nav.goTo(overview)
	tt.Frame()
	open()
	if got := titles(); slices.Contains(got, "Mark File as Viewed") || slices.Contains(got, "Comment on File") {
		t.Errorf("overview commands = %q", got)
	}
}

func TestCloseRepoForgetsTheLoad(t *testing.T) {
	app := newTestApp(t)
	if len(app.files) == 0 || len(app.diffs) == 0 {
		t.Fatalf("the first load read no files: %+v", app.perLoad)
	}
	app.queue = make(chan func(), 64)
	app.refresh()
	if !app.loader.switching() {
		t.Fatal("no load runs")
	}

	app.closeRepo()
	drain(app)
	if !app.loader.idle() {
		t.Error("the load still runs")
	}
	if app.snap != nil || len(app.files) != 0 || len(app.diffs) != 0 {
		t.Errorf("the closed project keeps %d files and %d diffs", len(app.files), len(app.diffs))
	}
}

// TestWelcomeList opens a recent project from the welcome screen with the
// keyboard.
func TestWelcomeList(t *testing.T) {
	app := newTestApp(t)
	app.cfg.AddRecent(fixtureRepo(t))
	app.closeRepo()
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	tt.Frame()
	snapshot(t, tt, "welcome")
	if !tt.Focused("Projects") {
		t.Fatal("the list has no focus")
	}
	want := app.cfg.Recent[1]
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if app.repo == nil || app.repo.Root != want {
		t.Fatalf("opened %v, want %s", app.repo, want)
	}
}

// TestShortcutSettings records shortcuts in the settings dialog: the menu
// bar and the saved config take them, and the commands run by them.
func TestShortcutSettings(t *testing.T) {
	app := newTestApp(t)
	app.nav.page = page{kindFile, "api/handler.go"}
	tt := ui.NewTester(app.view, 1300, 800)
	app.openSettings()
	app.settings.tab = shortcutsTab
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
		walk(app.menu().Items())
		return keys
	}
	startRecording := func(filter, current string) {
		t.Helper()
		app.keysFilter = filter
		tt.Frame()
		if err := tt.Click(current); err != nil {
			t.Fatalf("%s: %v in %q", filter, err, tt.Texts())
		}
		tt.Frame()
		tt.Frame()
		if app.recording == "" {
			t.Fatalf("%s: not recording", filter)
		}
	}

	startRecording("refresh", "⌘R")
	tt.Key(ui.Cmd|ui.Shift, ui.KeyY)
	tt.Frame()
	snapshot(t, tt, "shortcut-settings")
	if menuKeys("refresh") != "Shift+CmdOrCtrl+Y" || app.store.LoadConfig().Keys["refresh"] != "Shift+CmdOrCtrl+Y" {
		t.Errorf("refresh: menu %q, config %v", menuKeys("refresh"), app.store.LoadConfig().Keys)
	}

	startRecording("mark file as viewed", "None")
	tt.Key(ui.Cmd|ui.Shift, ui.KeyY)
	tt.Frame()
	if !tt.HasText("⇧⌘Y moved from Refresh to Mark File as Viewed.") {
		t.Errorf("move note: %q", app.keysNote)
	}

	startRecording("refresh", "None")
	tt.Key(ui.Cmd, ui.KeyC)
	tt.Frame()
	if !tt.HasText("⌘C is taken by Copy, which you cannot change.") {
		t.Errorf("taken note: %q", app.keysNote)
	}
	if got := app.keymap().assign("refresh", "CmdOrCtrl+Enter"); got.kind != assignTaken {
		t.Errorf("a fixed key: %+v", got)
	}

	// The modifiers held show before the key comes.
	startRecording("refresh", "None")
	tt.HoldModifiers(ui.Cmd | ui.Shift)
	tt.Frame()
	if !tt.HasText("⇧⌘…") {
		t.Errorf("held keys: %q", tt.Texts())
	}
	tt.HoldModifiers(0)
	tt.Frame()

	// Escape cancels the recording and keeps the dialog.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if app.recording != "" || app.settings == nil || !app.settings.open {
		t.Errorf("Escape: recording %q, settings %v", app.recording, app.settings)
	}

	// The focus moving away cancels the recording.
	startRecording("refresh", "None")
	if err := tt.Click("Filter commands"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if app.recording != "" {
		t.Errorf("focus away: recording %q", app.recording)
	}

	// A menu bar shortcut pressed while recording is the recorded one, and
	// the next frame puts it in the menu bar.
	app.startRecording("focus-files")
	for _, it := range app.menu().Items() {
		for _, sub := range it.Submenu {
			if sub.ID == "switch-branch" {
				sub.Click(sub, nil)
			}
		}
	}
	if app.keymap().of("focus-files") != "CmdOrCtrl+B" || app.keymap().of("switch-branch") != "" || app.pending != "" || !app.menuDirty {
		t.Errorf("menu key: %v, pending %q, dirty %v", app.cfg.Keys, app.pending, app.menuDirty)
	}

	// A command outside the menu bar runs by its keys where it applies.
	app.settings.open = false
	tt.Frame()
	tt.Key(ui.Cmd|ui.Shift, ui.KeyY)
	tt.Frame()
	if !app.rev.Viewed["api/handler.go"] {
		t.Error("the shortcut did not mark the file viewed")
	}
	// Also while the focus is in a text field.
	app.setViewed([]string{"api/handler.go"}, false)
	app.keymap().assign("mark-viewed", "CmdOrCtrl+Shift+U")
	if err := tt.Click("Comment on file"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	tt.Type("hello")
	tt.Frame()
	tt.Key(ui.Cmd|ui.Shift, ui.KeyU)
	tt.Frame()
	if !app.rev.Viewed["api/handler.go"] || app.drafts[app.threadID()] != "hello" {
		t.Errorf("in a field: viewed %v, draft %q", app.rev.Viewed, app.drafts[app.threadID()])
	}
}

// TestScopeShowsAtOnce keeps the shown changes while the next ones load,
// then shows them with the files of the code snippets already read.
func TestScopeShowsAtOnce(t *testing.T) {
	app := newTestApp(t)
	app.rev.Overview = &review.Overview{Walk: &review.Walkthrough{Summary: "s", Steps: []review.Step{
		{Title: "A", Text: "a", Refs: []review.Ref{{File: "api/handler.go", Start: 1, End: 2}}},
	}}}
	app.queue = make(chan func(), 64)
	app.reload()
	if !app.loader.switching() || len(app.files) == 0 {
		t.Fatalf("switching %v, files %d", app.loader.switching(), len(app.files))
	}
	(<-app.queue)()
	if !app.loader.idle() || len(app.files) == 0 {
		t.Fatalf("idle %v, files %d", app.loader.idle(), len(app.files))
	}
	if diff := app.diffs["api/handler.go"]; diff == nil || diff.state == nil {
		t.Error("the file of the snippet is not read with the changes")
	}
	if diff := app.diffs["cache/cache.go"]; diff == nil || !diff.loading {
		t.Error("the other file is read before the changes show")
	}

	// A new scope shows once its changes are read.
	old := app.scope
	commit := gitx.Scope{Kind: gitx.ScopeCommit, Commit: app.commits.rows[0].SHA}
	app.setScope(commit)
	if app.scope != old || app.chosen != commit || !app.loader.switching() {
		t.Fatalf("scope %+v, chosen %+v, switching %v", app.scope, app.chosen, app.loader.switching())
	}
	waitLoad(t, app, "the commit")
	if app.scope != commit || app.nav.page != overview {
		t.Errorf("scope %+v, page %q", app.scope, app.nav.page.String())
	}
}

// TestScopeLoadsInOrder opens a commit while a project is still read,
// and refreshes while the commit is.
func TestScopeLoadsInOrder(t *testing.T) {
	app := newTestApp(t)
	commit := gitx.Scope{Kind: gitx.ScopeCommit, Commit: app.commits.rows[0].SHA}
	app.queue = make(chan func(), 64)
	app.openRepo(app.repo.Root)
	app.setScope(commit)
	waitLoad(t, app, "the commit")
	if app.scope != commit || app.rev.Scope != commit {
		t.Fatalf("scope %+v, review of %+v", app.scope, app.rev.Scope)
	}

	uncommitted := gitx.Scope{Kind: gitx.ScopeUncommitted}
	app.setScope(uncommitted)
	app.refresh()
	waitLoad(t, app, "the uncommitted changes")
	if app.scope != uncommitted || app.chosen != uncommitted {
		t.Errorf("scope %+v, chosen %+v", app.scope, app.chosen)
	}
}

// TestSettingsRoom leaves as much room under a tall settings dialog as
// above it.
func TestSettingsRoom(t *testing.T) {
	app := newTestApp(t)
	tt := ui.NewTester(app.view, 1300, 800)
	app.openSettings()
	app.settings.tab = shortcutsTab
	tt.Frame()
	snapshot(t, tt, "settings-room")
	// 15% of the window above the dialog, and Done about 26 points
	// inside it: the padding of the dialog and of the button.
	done, _ := tt.Find("Done")
	if below := 800 - (done.Y + done.H); below < 140 || below > 152 {
		t.Errorf("room under Done %v, want about 146", below)
	}
}

// TestOverlaysCloseEachOther opens a dialog or a list over another from
// the menu bar: the one under it closes.
func TestOverlaysCloseEachOther(t *testing.T) {
	app := newTestApp(t)
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	menu := func(id string) {
		for _, it := range app.menu().Items() {
			for _, sub := range it.Submenu {
				if sub.ID == id {
					sub.Click(sub, nil)
				}
			}
		}
		tt.Frame()
		tt.Frame()
	}

	menu("palette")
	menu("settings")
	if app.paletteOpen || app.settings == nil {
		t.Fatalf("palette %v, settings %v", app.paletteOpen, app.settings)
	}
	menu("switch-project")
	if app.settings != nil || !app.projectsOpen {
		t.Fatalf("settings %v, projects %v", app.settings, app.projectsOpen)
	}
	menu("switch-branch")
	if app.projectsOpen || !app.branchesOpen {
		t.Fatalf("projects %v, branches %v", app.projectsOpen, app.branchesOpen)
	}
	// Its own shortcut still closes a list.
	menu("switch-branch")
	if app.branchesOpen {
		t.Error("the branch list stayed open")
	}
	// Every other command closes the palette too.
	menu("palette")
	menu("refresh")
	if app.paletteOpen {
		t.Error("Refresh left the palette open")
	}
}

// TestOverlayFocus opens lists and dialogs over the palette: each takes
// the keyboard focus. Cmd+1 puts it on the page.
func TestOverlayFocus(t *testing.T) {
	app := newTestApp(t)
	git(t, app.repo.Root, "branch", "docs")
	app.openRepo(app.repo.Root)
	app.cfg.AddRecent(fixtureRepo(t))
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	menu := func(id string) {
		for _, it := range app.menu().Items() {
			for _, sub := range it.Submenu {
				if sub.ID == id {
					sub.Click(sub, nil)
				}
			}
		}
		tt.Frame()
		tt.Frame()
	}

	menu("palette")
	menu("switch-project")
	hi := app.projectNav.hi
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	if !tt.Focused("Projects") || app.projectNav.hi == hi {
		t.Errorf("project list: focused %v, highlight %d then %d", tt.Focused("Projects"), hi, app.projectNav.hi)
	}

	menu("palette")
	menu("switch-branch")
	tt.Type("docs")
	tt.Frame()
	if app.branchQuery != "docs" {
		t.Errorf("branch filter %q", app.branchQuery)
	}

	menu("settings")
	menu("shortcuts")
	if !tt.Focused("Done") {
		t.Error("the shortcuts dialog has no focus")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	// From the list of files, Cmd+1 moves the focus to the page.
	menu("focus-files")
	menu("go-overview")
	if tt.Focused("Files") || app.nav.page != overview {
		t.Fatalf("files focused %v, page %q", tt.Focused("Files"), app.nav.page.String())
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if app.nav.page != overview {
		t.Errorf("Down moved the sidebar to %q", app.nav.page.String())
	}
}

// TestReviewGroupOnHover shows a group card's Review group button while
// the pointer is over the card.
func TestReviewGroupOnHover(t *testing.T) {
	app := newTestApp(t)
	app.rev.Overview = &review.Overview{Markdown: "x", Groups: []review.Group{
		{Title: "Cache keys", Files: []string{"cache/cache.go"}}, {Title: "API", Files: []string{"api/handler.go"}},
	}}
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	tt.Move(1, 790)
	tt.Frame()
	snapshot(t, tt, "groups-hidden")
	button, ok := tt.Find("Review group")
	if !ok {
		t.Fatalf("no button: %q", tt.Texts())
	}
	tt.Move(button.X-300, button.Y+button.H/2)
	tt.Frame()
	tt.Frame()
	snapshot(t, tt, "groups-hover")
	if err := tt.Click("Review group"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.nav.page != (page{kindGroup, "Cache keys"}) {
		t.Errorf("page %q", app.nav.page.String())
	}
}

// TestFocusPage moves the keyboard focus from the list of files to the
// page: the overview's scroll, or a file's diff.
func TestFocusPage(t *testing.T) {
	app := newTestApp(t)
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	run := func(id string) {
		app.queueCommand(id)
		tt.Frame()
		tt.Frame()
		tt.Frame()
	}
	app.nav.goTo(overview)
	run("focus-files")
	want := app.nav.page
	run("focus-page")
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if tt.Focused("Files") || app.nav.page != want {
		t.Errorf("overview: files focused %v, page %q", tt.Focused("Files"), app.nav.page.String())
	}

	app.nav.goTo(page{kindFile, "api/handler.go"})
	run("focus-files")
	run("focus-page")
	if tt.Focused("Files") || !tt.Focused("New text") || app.refocus != "" {
		t.Errorf("file page: files focused %v, diff focused %v, refocus %q", tt.Focused("Files"), tt.Focused("New text"), app.refocus)
	}
}

// TestAllFiles opens the diffs of every file on one page, under the titles
// of their groups, and marks them all viewed.
func TestAllFiles(t *testing.T) {
	app := newTestApp(t)
	app.rev.Overview = &review.Overview{Markdown: "x", Groups: []review.Group{
		{Title: "Cache keys", Files: []string{"cache/cache.go"}}, {Title: "API", Files: []string{"api/handler.go"}},
	}}
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	if err := tt.Click("All files diff"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if app.nav.page != (page{kind: kindAll}) {
		t.Fatalf("page %q", app.nav.page.String())
	}
	snapshot(t, tt, "all-files")
	var ys []float32
	for _, want := range []string{"1. Cache keys", "cache/cache.go", "2. API", "api/handler.go"} {
		label, ok := tt.Find(want)
		if !ok {
			t.Fatalf("page lacks %q; texts %q", want, tt.Texts())
		}
		ys = append(ys, label.Y)
	}
	if !slices.IsSorted(ys) {
		t.Errorf("not in review order: %v", ys)
	}
	if err := tt.Click("Mark all as viewed"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !app.rev.Viewed["cache/cache.go"] || !app.rev.Viewed["api/handler.go"] {
		t.Errorf("viewed %v", app.rev.Viewed)
	}

	app.cfg.FlatFiles = true
	tt.Frame()
	if tt.HasText("1. Cache keys") || !tt.HasText("api/handler.go") || !tt.HasText("cache/cache.go") {
		t.Errorf("flat page texts %q", tt.Texts())
	}
}

// TestFoldFileCards folds a card when its file is marked viewed, unfolds
// it when the mark goes, and folds it on a click.
func TestFoldFileCards(t *testing.T) {
	app := newTestApp(t)
	app.nav.goTo(page{kind: kindAll})
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Advance(time.Second)
	diffs := func() int { return strings.Count(strings.Join(tt.Texts(), "\n"), "Old text") }
	if diffs() != 2 {
		t.Fatalf("unfolded texts %q", tt.Texts())
	}
	click := func(s string) {
		t.Helper()
		if err := tt.Click(s); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	click("Viewed")
	if !app.rev.Viewed[app.fileNames()[0]] || diffs() != 1 {
		t.Fatalf("viewed card not folded; texts %q", tt.Texts())
	}
	snapshot(t, tt, "folded-card")
	app.cmd("mark-not-viewed").run(nil, app.fileNames()[0])
	tt.Frame()
	if app.rev.Viewed[app.fileNames()[0]] || diffs() != 2 {
		t.Fatalf("not viewed card folded; texts %q", tt.Texts())
	}
	click("Fold")
	if diffs() != 1 {
		t.Fatalf("card not folded; texts %q", tt.Texts())
	}
	click("Unfold")
	if diffs() != 2 {
		t.Fatalf("card not unfolded; texts %q", tt.Texts())
	}
}

// TestBigFileStartsFolded folds the card of a file with more than 1000
// changed lines until the reviewer unfolds it.
func TestBigFileStartsFolded(t *testing.T) {
	dir := fixtureRepo(t)
	lines := func(n int) string {
		var builder strings.Builder
		for i := range n {
			fmt.Fprintf(&builder, "line %d\n", i)
		}
		return builder.String()
	}
	write(t, dir, "big.txt", lines(1001))
	write(t, dir, "edge.txt", lines(1000))
	app := newApp(store.Store{Dir: t.TempDir()})
	app.openRepo(dir)
	app.nav.goTo(page{kind: kindAll})
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Advance(time.Second)
	// Each card has a fold button: Unfold on a folded card, Fold on one open.
	folds := func() []string {
		var out []string
		for _, s := range tt.Texts() {
			if s == "Fold" || s == "Unfold" {
				out = append(out, s)
			}
		}
		return out
	}
	if got := folds(); len(got) < 2 || !slices.Equal(got[:2], []string{"Unfold", "Fold"}) {
		t.Fatalf("fold buttons of big.txt and edge.txt: %q", got)
	}
	if err := tt.Click("Unfold"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if got := folds(); len(got) < 2 || !slices.Equal(got[:2], []string{"Fold", "Fold"}) {
		t.Fatalf("big.txt not unfolded; fold buttons %q", got)
	}
}

// longFilePage opens the page of all files, with the deleted long.txt
// first and the modified more.txt after it. header finds the header of
// long.txt, as its badge shows there alone.
func longFilePage(t *testing.T) (app *app, tt *ui.Tester, header func() ui.Rect) {
	dir := fixtureRepo(t)
	var long strings.Builder
	for i := range 300 {
		fmt.Fprintf(&long, "line %d\n", i)
	}
	write(t, dir, "long.txt", long.String())
	write(t, dir, "more.txt", long.String())
	git(t, dir, "add", "long.txt", "more.txt")
	git(t, dir, "commit", "-q", "-m", "Add long.txt")
	if err := os.Remove(filepath.Join(dir, "long.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "more.txt", long.String()+long.String())
	app = newApp(store.Store{Dir: t.TempDir()})
	app.openRepo(dir)
	app.nav.goTo(page{kind: kindAll})
	tt = ui.NewTester(app.view, 1300, 800)
	tt.Advance(time.Second)
	header = func() ui.Rect {
		t.Helper()
		badge, ok := tt.Find("deleted")
		if !ok {
			t.Fatalf("texts %q", tt.Texts())
		}
		return badge
	}
	return app, tt, header
}

// TestStickyFileHeader scrolls the page of all files into a long diff:
// the header of its card stays at the top of the page.
func TestStickyFileHeader(t *testing.T) {
	_, tt, header := longFilePage(t)
	start := header()
	for _, dy := range []float32{1000, 500} {
		tt.Wheel(650, 400, 0, dy)
		tt.Advance(time.Second)
		// A label scrolled out of the page reports the part of it that shows.
		if scrolled := header(); scrolled.Y >= start.Y || scrolled.H != start.H {
			t.Errorf("header of long.txt at %v after scrolling by %v, at %v before", scrolled, dy, start)
		}
	}
	snapshot(t, tt, "sticky-header")
}

// TestPushPinnedHeader scrolls past the end of a card: the next card
// pushes its pinned header away, with the gap between the cards kept.
func TestPushPinnedHeader(t *testing.T) {
	_, tt, header := longFilePage(t)
	full := header()
	tt.Wheel(650, 400, 0, 5500)
	tt.Advance(time.Second)
	pushed := false
	for range 100 {
		tt.Wheel(650, 400, 0, 20)
		tt.Advance(100 * time.Millisecond)
		first, ok := tt.Find("deleted")
		next, nok := tt.Find("modified")
		if !ok || !nok || first.H != full.H || next.H != full.H {
			continue
		}
		// A header is about 46 high, with a gap of 24 after its card.
		if gap := next.Y - first.Y; gap < 46+24 {
			t.Fatalf("header of more.txt %v below the header of long.txt, at %v", gap, first)
		}
		if next.Y < first.Y+200 {
			pushed = true
		}
	}
	if !pushed {
		t.Error("never scrolled to where more.txt pushes the header of long.txt")
	}
}

// TestFoldPinnedCard folds a card while its header is pinned: the folded
// card stays at the top of the page, with the next card under it.
func TestFoldPinnedCard(t *testing.T) {
	app, tt, header := longFilePage(t)
	tt.Wheel(650, 400, 0, 1000)
	tt.Advance(time.Second)
	pinned := header()
	app.cmd("mark-viewed").run(nil, "long.txt")
	tt.Frame()
	tt.Advance(time.Second)
	if folded := header(); folded != pinned {
		t.Errorf("header of long.txt at %v after folding, at %v before", folded, pinned)
	}
	next, ok := tt.Find("modified")
	if !ok || next.Y < pinned.Y || next.Y > pinned.Y+120 {
		t.Errorf("header of more.txt at %v, %v; header of long.txt at %v", next, ok, pinned)
	}
}

// TestEarlierReviewPage archives a review and opens it on its own page,
// next to the review of the changes that replaced it. The page shows the
// code the snippets had while the review was current.
func TestEarlierReviewPage(t *testing.T) {
	app := newTestApp(t)
	dir := app.repo.Root
	answer := "```json\n" + `{"summary": "Lower-case keys.", "steps": [{"title": "Keys", "text": "a", "refs": [{"file": "cache/cache.go", "side": "new", "start": 5, "end": 7, "why": "the call"}]}], "groups": []}` + "\n```"
	fake := newFakeAgent(t, app)
	fake.answer("s1", answer)
	app.cfg.DeeperAhead = false
	app.queue = make(chan func(), 64)
	app.generateOverview()
	pump(t, app, "the overview", func() bool { return !app.busy(store.TargetOverview) })
	git(t, dir, "stash", "-u", "-q")
	write(t, dir, "output.json", "{}\n")
	app.refresh()
	waitLoad(t, app, "the other changes")
	fake.answer("s2", "```json\n"+`{"summary": "Other change.", "steps": [{"title": "B", "text": "b"}], "groups": []}`+"\n```")
	app.generateOverview()
	pump(t, app, "the second overview", func() bool { return !app.busy(store.TargetOverview) })
	tt := ui.NewTester(app.view, 1300, 900)
	tt.Frame()
	if !tt.HasText("Other change.") || !tt.HasText("Earlier reviews") {
		t.Fatalf("archived %v; texts %q", app.reviews.Earlier(), tt.Texts())
	}

	if label, ok := tt.Find("Lower-case keys."); ok {
		tt.Move(label.X, label.Y+label.H/2)
		tt.Frame()
		snapshot(t, tt, "earlier-hover")
	}
	if button, ok := tt.Find("Delete"); ok {
		tt.Move(button.X+button.W/2, button.Y+button.H/2)
		tt.Frame()
		snapshot(t, tt, "earlier-hover-delete")
	}
	if err := tt.Click("Lower-case keys."); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	snapshot(t, tt, "earlier-review")
	if !tt.HasText("    return strings.ToLower(key)") {
		t.Fatalf("the page lacks the saved code: %q", tt.Texts())
	}
	if _, ok := tt.Find("Restore"); ok {
		t.Error("restore offered over the review of the current changes")
	}

	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("Earlier reviews") || !tt.HasText("Other change.") {
		t.Errorf("after delete: texts %q", tt.Texts())
	}
}

// TestEarlierReviewInFull shows an archived review with its deeper
// walkthroughs, its questions with their follow-ups, and its comments.
func TestEarlierReviewInFull(t *testing.T) {
	app := newTestApp(t)
	rev := review.New(app.repo.Root, gitx.Scope{Kind: gitx.ScopeArchived, From: "p1"})
	rev.Overview = &review.Overview{Agent: agent.Claude, Generated: time.Now(), Walk: &review.Walkthrough{
		Summary: "Root summary.",
		Steps: []review.Step{{Title: "Root step", Refs: []review.Ref{
			{File: "cache/cache.go", Side: review.SideNew, Start: 5, End: 7},
		}}},
	}}
	rev.Walks = []*review.Walk{
		{ID: "d1", Parent: "", Step: 0, Content: &review.Walkthrough{Steps: []review.Step{{Title: "Deeper step"}}}},
		{ID: "q1", Parent: "", Step: -1, Question: "Why lower case?", Markdown: "Callers compare keys.", More: []review.Turn{
			{Question: "Which callers?", Content: &review.Walkthrough{Steps: []review.Step{{Title: "Follow-up step"}}}},
		}},
	}
	th := rev.AddThread(review.Anchor{Kind: review.AnchorLines, File: "cache/cache.go", Side: review.SideNew, Start: 6, End: 6, Quote: "\treturn strings.ToLower(key)"})
	th.Messages = []review.Message{{Role: review.RoleUser, Text: "Keep keys as they are."}}
	if err := app.store.SaveReview(rev); err != nil {
		t.Fatal(err)
	}
	app.reload()
	app.nav.goTo(page{kindArchived, "p1"})
	tt := ui.NewTester(app.view, 1300, 2400)
	tt.Frame()
	for _, want := range []string{
		"Root summary.", "Deeper: Root step", "1. Deeper step", "Question: Why lower case?", "Callers compare keys.",
		"Question: Which callers?", "1. Follow-up step", "The code of this snippet was not saved with the review.",
		"    return strings.ToLower(key)", "Keep keys as they are.",
	} {
		if !tt.HasText(want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if index := slices.Index(tt.Texts(), "1. Root step"); index < 0 || slices.Contains(tt.Texts()[index+1:], "1. Root step") {
		t.Errorf("the root step shows %v times; texts %q", index, tt.Texts())
	}
}

// TestOverviewShowsCommitMessage opens a commit: the overview shows its
// subject and body, and the uncommitted changes show neither.
func TestOverviewShowsCommitMessage(t *testing.T) {
	app := newTestApp(t)
	app.queue = make(chan func(), 64)
	git(t, app.repo.Root, "commit", "-qam", "Lower the cache keys\n\nKeys differ only by case in some callers.")
	app.setScope(gitx.Scope{Kind: gitx.ScopeCommit, Commit: "HEAD"})
	waitLoad(t, app, "the commit")
	tt := ui.NewTester(app.view, 1300, 900)
	tt.Frame()
	snapshot(t, tt, "overview-commit")
	for _, want := range []string{"Lower the cache keys", "Keys differ only by case in some callers."} {
		if !tt.HasText(want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	app.setScope(gitx.Scope{Kind: gitx.ScopeUncommitted})
	waitLoad(t, app, "the uncommitted changes")
	tt.Frame()
	if tt.HasText("Keys differ only by case in some callers.") {
		t.Error("the uncommitted changes show the commit body")
	}
}

// TestCommitRowShowsOverviewState starts the overview of a commit and
// opens the uncommitted changes while it runs. The commit's row shows the
// run, then that the overview is ready, until the user opens the commit.
func TestCommitRowShowsOverviewState(t *testing.T) {
	app := newTestApp(t)
	app.queue = make(chan func(), 64)
	app.cfg.DeeperAhead = false
	fake := newFakeAgent(t, app)
	fake.answer("o1", sessionsAnswer)
	fake.hold()
	pump(t, app, "the commit list", func() bool { return len(app.commits.rows) > 0 })
	commit := gitx.Scope{Kind: gitx.ScopeCommit, Commit: app.commits.rows[0].SHA}
	app.setScope(commit)
	waitLoad(t, app, "the commit")
	rev := app.rev
	overviewOf := func() targetState { return app.targets.state(rev, review.TargetOverview) }
	app.generateOverview()
	app.setScope(gitx.Scope{Kind: gitx.ScopeUncommitted})
	waitLoad(t, app, "the uncommitted changes")
	if got := overviewOf(); got.status != targetRunning {
		t.Fatalf("state while the overview runs = %+v", got)
	}
	tt := ui.NewTester(app.view, 1300, 900)
	tt.Frame()
	snapshot(t, tt, "commit-row-writing")
	fake.release()
	pump(t, app, "the overview", func() bool { return !overviewOf().busy() })
	if got := overviewOf(); got != (targetState{status: targetAnswered, unseen: true}) {
		t.Fatalf("state after the overview = %+v, want answered and unseen", got)
	}
	tt.Frame()
	snapshot(t, tt, "commit-row-ready")
	if app.targets.unseenIn(app.rev) {
		t.Error("the uncommitted changes have an unseen answer")
	}
	app.setScope(commit)
	waitLoad(t, app, "the commit again")
	if overviewOf().unseen {
		t.Error("the overview stays unseen after opening the commit")
	}

	fake.fail(errors.New("claude: executable file not found in $PATH"))
	fake.hold()
	app.generateOverview()
	app.setScope(gitx.Scope{Kind: gitx.ScopeUncommitted})
	waitLoad(t, app, "the uncommitted changes")
	fake.release()
	pump(t, app, "the failed overview", func() bool { return !overviewOf().busy() })
	if got := overviewOf(); got != (targetState{status: targetFailed, unseen: true}) {
		t.Errorf("state after a failed overview = %+v, want failed and unseen", got)
	}
}
