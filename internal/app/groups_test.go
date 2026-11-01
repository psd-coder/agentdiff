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
func finishOverview(t *testing.T, app *app, groupsJSON string) {
	t.Helper()
	answer := "```json\n" + `{"summary": "s", "steps": [{"title": "A", "text": "a"}], "groups": ` + groupsJSON + "}\n```"
	newFakeAgent(t, app).answer("s1", answer)
	app.cfg.DeeperAhead = false
	app.generateOverview()
	pump(t, app, "the overview", func() bool { return !app.busy(store.TargetOverview) })
}

// TestGroupPageKeepsGroupAfterOverview opens a directory group page and
// lets an overview with other groups finish: the page keeps the group
// with its title, and turns into the Overview when the title is gone,
// which Back does not return to.
func TestGroupPageKeepsGroupAfterOverview(t *testing.T) {
	app := newTestApp(t)
	app.queue = make(chan func(), 64)
	app.nav.goTo(page{kindGroup, "api"})
	app.nav.goTo(page{kindGroup, "cache"})
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	if !tt.HasText("2. cache") {
		t.Fatalf("directory group page: %q", tt.Texts())
	}

	finishOverview(t, app, `[{"title": "cache", "files": ["cache/cache.go"]}, {"title": "Handler", "files": ["api/handler.go"]}]`)
	tt.Frame()
	if app.nav.page != (page{kindGroup, "cache"}) || !tt.HasText("1. cache") {
		t.Fatalf("page %q, texts %q", app.nav.page.String(), tt.Texts())
	}

	finishOverview(t, app, `[{"title": "Keys", "files": ["cache/cache.go"]}, {"title": "Handler", "files": ["api/handler.go"]}]`)
	tt.Frame()
	if app.nav.page != overview || !shows(tt, "Review order") {
		t.Fatalf("page %q, texts %q", app.nav.page.String(), tt.Texts())
	}
	if !slices.Equal(app.nav.history, []page{overview}) {
		t.Errorf("history %+v keeps the gone group pages", app.nav.history)
	}
}

// TestGroupPageKeepsGroupAfterReload adds a file in a directory that
// sorts before the open group, and reloads: the page keeps its group.
func TestGroupPageKeepsGroupAfterReload(t *testing.T) {
	app := newTestApp(t)
	app.queue = make(chan func(), 64)
	app.nav.goTo(page{kindGroup, "cache"})
	write(t, app.repo.Root, "app/main.go", "package main\n")
	app.refresh()
	waitLoad(t, app, "the reload")
	drain(app)
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	if app.nav.page != (page{kindGroup, "cache"}) || !tt.HasText("3. cache") || !shows(tt, "cache.go") {
		t.Fatalf("page %q, texts %q", app.nav.page.String(), tt.Texts())
	}
}

// TestDuplicateTitlesGetOwnPageAndOpenState shows two groups with the
// same title, each with its own page and its own open state.
func TestDuplicateTitlesGetOwnPageAndOpenState(t *testing.T) {
	app := newTestApp(t)
	app.rev.Overview = &review.Overview{Markdown: "x", Groups: []review.Group{
		{Title: "Tests", Files: []string{"cache/cache.go"}}, {Title: "Tests", Files: []string{"api/handler.go"}},
	}}
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	if err := tt.Click("Hide files"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if shows(tt, "cache.go") || !shows(tt, "handler.go") {
		t.Fatalf("one group hidden: %q", tt.Texts())
	}
	app.nav.goTo(page{kindGroup, "Tests #2"})
	tt.Frame()
	if !tt.HasText("2. Tests") || tt.HasText("1. Tests") {
		t.Errorf("second group page: %q", tt.Texts())
	}
}
