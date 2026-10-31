package app

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/psd-coder/agentdiff/internal/review"
	"github.com/psd-coder/agentdiff/internal/store"
)

// finishOverview runs an overview that answers with the groups in
// groupsJSON and waits until it is done.
func finishOverview(t *testing.T, a *app, groupsJSON string) {
	t.Helper()
	answer := "```json\n" + `{"summary": "s", "steps": [{"title": "A", "text": "a"}], "groups": ` + groupsJSON + "}\n```"
	newFakeAgent(t, a).answer("s1", answer)
	a.cfg.DeeperAhead = false
	a.generateOverview()
	pump(t, a, "the overview", func() bool { return !a.busy(store.TargetOverview) })
}

// TestGroupPageKeepsGroupAfterOverview opens a directory group page and
// lets an overview with other groups finish: the page keeps the group
// with its title, and turns into the Overview when the title is gone,
// which Back does not return to.
func TestGroupPageKeepsGroupAfterOverview(t *testing.T) {
	a := newTestApp(t)
	a.queue = make(chan func(), 64)
	a.nav.goTo(page{kindGroup, "api"})
	a.nav.goTo(page{kindGroup, "cache"})
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	if !tt.HasText("2. cache") {
		t.Fatalf("directory group page: %q", tt.Texts())
	}

	finishOverview(t, a, `[{"title": "cache", "files": ["cache/cache.go"]}, {"title": "Handler", "files": ["api/handler.go"]}]`)
	tt.Frame()
	if a.nav.page != (page{kindGroup, "cache"}) || !tt.HasText("1. cache") {
		t.Fatalf("page %q, texts %q", a.nav.page.String(), tt.Texts())
	}

	finishOverview(t, a, `[{"title": "Keys", "files": ["cache/cache.go"]}, {"title": "Handler", "files": ["api/handler.go"]}]`)
	tt.Frame()
	if a.nav.page != overview || !shows(tt, "Review order") {
		t.Fatalf("page %q, texts %q", a.nav.page.String(), tt.Texts())
	}
	if !slices.Equal(a.nav.history, []page{overview}) {
		t.Errorf("history %+v keeps the gone group pages", a.nav.history)
	}
}

// TestGroupPageKeepsGroupAfterReload adds a file in a directory that
// sorts before the open group, and reloads: the page keeps its group.
func TestGroupPageKeepsGroupAfterReload(t *testing.T) {
	a := newTestApp(t)
	a.queue = make(chan func(), 64)
	a.nav.goTo(page{kindGroup, "cache"})
	write(t, a.repo.Root, "app/main.go", "package main\n")
	a.refresh()
	pump(t, a, "the reload", func() bool { return !a.loading && !a.switching })
	drain(a)
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	if a.nav.page != (page{kindGroup, "cache"}) || !tt.HasText("3. cache") || !shows(tt, "cache.go") {
		t.Fatalf("page %q, texts %q", a.nav.page.String(), tt.Texts())
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
	a.nav.goTo(page{kindGroup, "Tests #2"})
	tt.Frame()
	if !tt.HasText("2. Tests") || tt.HasText("1. Tests") {
		t.Errorf("second group page: %q", tt.Texts())
	}
}
