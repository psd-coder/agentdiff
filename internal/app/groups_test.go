package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/review"
)

// finishOverview runs an overview that answers with the groups in
// groupsJSON and waits until it is done.
func finishOverview(t *testing.T, a *app, groupsJSON string) {
	t.Helper()
	answer := "```json\n" + `{"summary": "s", "steps": [{"title": "A", "text": "a"}], "groups": ` + groupsJSON + "}\n```"
	a.cfg.Agent = agent.Config{Provider: agent.Claude, Binary: fakeClaude(t, "s1", answer)}
	a.cfg.DeeperAhead = false
	a.generateOverview()
	pump(t, a, "the overview", func() bool { return a.overviewRunning() == nil })
}

// TestGroupPageKeepsGroupAfterOverview opens a directory group page and
// lets an overview with other groups finish: the page keeps the group
// with its title, and turns into the Overview when the title is gone.
func TestGroupPageKeepsGroupAfterOverview(t *testing.T) {
	a := newTestApp(t)
	a.queue = make(chan func(), 64)
	a.goTo("group:api")
	a.goTo("group:cache")
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	if !tt.HasText("2. cache") {
		t.Fatalf("directory group page: %q", tt.Texts())
	}

	finishOverview(t, a, `[{"title": "cache", "files": ["cache/cache.go"]}, {"title": "Handler", "files": ["api/handler.go"]}]`)
	tt.Frame()
	if a.page != "group:cache" || !tt.HasText("1. cache") {
		t.Fatalf("page %q, texts %q", a.page, tt.Texts())
	}

	finishOverview(t, a, `[{"title": "Keys", "files": ["cache/cache.go"]}, {"title": "Handler", "files": ["api/handler.go"]}]`)
	tt.Frame()
	if a.page != "overview" || !shows(tt, "Review order") {
		t.Fatalf("page %q, texts %q", a.page, tt.Texts())
	}
	// Back skips the gone group pages and returns to the first page.
	a.goTo("file:api/handler.go")
	a.goBack()
	if a.page != "overview" {
		t.Errorf("back from the file: %q", a.page)
	}
	a.goBack()
	if a.page != "overview" || len(a.history) != 0 {
		t.Errorf("back again: %q, history %q", a.page, a.history)
	}
}

// TestGroupPageKeepsGroupAfterReload adds a file in a directory that
// sorts before the open group, and reloads: the page keeps its group.
func TestGroupPageKeepsGroupAfterReload(t *testing.T) {
	a := newTestApp(t)
	a.queue = make(chan func(), 64)
	a.goTo("group:cache")
	write(t, a.repo.Root, "app/main.go", "package main\n")
	a.refresh()
	pump(t, a, "the reload", func() bool { return !a.loading && !a.switching })
	drain(a)
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	if a.page != "group:cache" || !tt.HasText("3. cache") || !shows(tt, "cache.go") {
		t.Fatalf("page %q, texts %q", a.page, tt.Texts())
	}
}

// TestDuplicateTitlesGetOwnPageAndOpenState shows two groups with the
// same title, each with its own page and its own open state.
func TestDuplicateTitlesGetOwnPageAndOpenState(t *testing.T) {
	a := newTestApp(t)
	a.rev.Overview = &review.Overview{Markdown: "x", Groups: []review.Group{
		{Title: "Tests", Files: []string{"cache/cache.go"}}, {Title: "Tests", Files: []string{"api/handler.go"}},
	}}
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	if err := tt.Click("Hide files"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if shows(tt, "cache.go") || !shows(tt, "handler.go") {
		t.Fatalf("one group hidden: %q", tt.Texts())
	}
	a.goTo("group:Tests #2")
	tt.Frame()
	if !tt.HasText("2. Tests") || tt.HasText("1. Tests") {
		t.Errorf("second group page: %q", tt.Texts())
	}
}
