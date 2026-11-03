package app

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/review"
	"github.com/psd-coder/agentdiff/internal/store"
)

func overviewAnswer(summary string, steps ...string) string {
	var parts []string
	for _, s := range steps {
		parts = append(parts, `{"title": "`+s+`", "text": "about `+s+`"}`)
	}
	return "```json\n" + `{"summary": "` + summary + `", "steps": [` + strings.Join(parts, ", ") + `], "groups": []}` + "\n```"
}

// writeOverview answers the next overview run in session and waits for it.
func writeOverview(t *testing.T, app *app, fake *fakeAgent, session, answer string) *review.Overview {
	t.Helper()
	fake.answer(session, answer)
	app.regenerate("")
	pump(t, app, "the overview", func() bool { return !app.busy(store.TargetOverview) })
	return app.rev.Overview
}

func TestRegenerateKeepsTheEarlierVersion(t *testing.T) {
	app := newTestApp(t)
	app.queue = make(chan func(), 64)
	app.cfg.DeeperAhead = false
	app.askConfirm = func(message, _, _ string, _ func()) { t.Errorf("asked %q", message) }
	fake := newFakeAgent(t, app)
	first := writeOverview(t, app, fake, "v1", overviewAnswer("First summary.", "Keys", "Cache"))
	app.deeper(first.ID, first.Walk.Steps[0].ID)
	deeper := app.rev.Walks[0]
	pump(t, app, "the deeper walkthrough", func() bool { return !app.busy(deeper.ID) })

	second := writeOverview(t, app, fake, "v2", overviewAnswer("Second summary.", "Handler"))
	if second == first || second.Version != 2 || len(app.rev.Previous) != 1 || app.rev.Previous[0] != first {
		t.Fatalf("latest v%d, earlier versions %v", second.Version, app.rev.Previous)
	}
	if app.rev.Walk(deeper.ID) == nil || app.rev.VersionOf(deeper.ID) != first.ID {
		t.Fatalf("the deeper walkthrough of v1 is gone or moved: %+v", app.rev.Walks)
	}

	tt := ui.NewTester(app.view, 1300, 1000)
	if !tt.HasText("Second summary.") || tt.HasText("First summary.") {
		t.Fatalf("the Overview does not show the latest version: %q", tt.Texts())
	}
	labels := versionLabels([]*review.Overview{second, first}, second)
	if err := tt.Click(labels[0]); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click(labels[1]); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	snapshot(t, tt, "overview-earlier-version")
	if app.nav.page != (page{kindOverview, first.ID}) || !tt.HasText("First summary.") || !tt.HasText("Deeper ›") {
		t.Fatalf("page %q shows %q", app.nav.page.String(), tt.Texts())
	}

	app.deeper(first.ID, first.Walk.Steps[1].ID)
	if req := fake.last(); req.ResumeID != "v1" || !req.Fork {
		t.Errorf("Go deeper on v1 resumes %q (fork %v), want a fork of v1", req.ResumeID, req.Fork)
	}
	app.nav.goTo(page{kindOverview, first.ID})
	tt.Frame()
	// The sidebar item comes before the title of the page.
	if err := tt.Click("Overview"); err != nil {
		t.Fatal(err)
	}
	if app.nav.page != overview {
		t.Errorf("Overview in the sidebar on v1 opens %q", app.nav.page.String())
	}
	if got := app.targetLabel(first.ID, first.Walk.Steps[0].ID); got != "Overview v1 › Keys" {
		t.Errorf("a question on a step of v1 is about %q", got)
	}
	if got := app.targetLabel(second.ID, second.Walk.Steps[0].ID); got != "Handler" {
		t.Errorf("a question on a step of the latest version is about %q", got)
	}
}

func TestDeleteVersion(t *testing.T) {
	app := newTestApp(t)
	app.queue = make(chan func(), 64)
	app.cfg.DeeperAhead = false
	fake := newFakeAgent(t, app)
	first := writeOverview(t, app, fake, "v1", overviewAnswer("First summary.", "Keys"))
	deeper := app.rev.AddWalk(first.ID, first.Walk.Steps[0].ID, "")
	under := app.rev.AddWalk(deeper.ID, "", "Why?")
	second := writeOverview(t, app, fake, "v2", overviewAnswer("Second summary.", "Handler"))
	kept := app.rev.AddWalk(second.ID, second.Walk.Steps[0].ID, "")
	thread := app.rev.AddThread(review.Anchor{Kind: review.AnchorGeneral})

	app.nav.goTo(page{kindOverview, first.ID})
	if app.applies(app.cmd("delete-overview-version"), second.ID) {
		t.Error("the latest version can be deleted")
	}
	var confirmed func()
	app.askConfirm = func(_, _, _ string, fn func()) { confirmed = fn }
	target, ok := app.here(app.cmd("delete-overview-version"))
	if !ok || target != first.ID {
		t.Fatalf("Delete Overview Version acts on %q (%v)", target, ok)
	}
	app.cmd("delete-overview-version").run(nil, target)
	confirmed()
	if app.rev.OverviewOf(first.ID) != nil || app.rev.Walk(deeper.ID) != nil || app.rev.Walk(under.ID) != nil {
		t.Errorf("v1 or its walks stayed: earlier %v, walks %+v", app.rev.Previous, app.rev.Walks)
	}
	if app.rev.Walk(kept.ID) == nil || app.rev.Thread(thread.ID) == nil || app.rev.Overview != second {
		t.Error("deleting v1 took the latest version, its walk or a thread")
	}
	if app.nav.page != overview {
		t.Errorf("page after the delete = %q", app.nav.page.String())
	}
}

func TestStopWriteAheadOfAnEarlierVersion(t *testing.T) {
	app := newTestApp(t)
	app.queue = make(chan func(), 64)
	app.cfg.DeeperAhead = true
	fake := newFakeAgent(t, app)
	fake.answer("v1", overviewAnswer("First summary.", "Keys", "Cache"))
	app.generateOverview()
	fake.hold()
	pump(t, app, "the overview", func() bool { return !app.busy(store.TargetOverview) })
	first := app.rev.Overview
	ahead := app.aheadOf(first.ID)
	if len(ahead) != 2 {
		t.Fatalf("write-ahead of v1 = %v", ahead)
	}

	second := writeOverview(t, app, fake, "v2", overviewAnswer("Second summary.", "Handler"))
	pump(t, app, "the write-ahead of v2", func() bool { return len(app.aheadOf(second.ID)) == 0 })
	if got := app.aheadOf(first.ID); len(got) != 2 {
		t.Fatalf("the write-ahead of v1 after v2 = %v", got)
	}
	for _, req := range fake.received()[1:3] {
		if req.ResumeID != "v1" {
			t.Errorf("a write-ahead of v1 resumes %q", req.ResumeID)
		}
	}

	tt := ui.NewTester(app.view, 1300, 1000)
	banner := "Overview v1 still writes 2 deeper walkthroughs ahead."
	if !tt.HasText(banner) {
		t.Fatalf("no banner for the write-ahead of v1: %q", tt.Texts())
	}
	snapshot(t, tt, "overview-earlier-write-ahead")
	if err := tt.Click("Stop"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(app.aheadOf(first.ID)) != 0 || tt.HasText(banner) {
		t.Errorf("write-ahead of v1 after Stop = %v", app.aheadOf(first.ID))
	}
	for _, id := range ahead {
		if app.rev.Walk(id) != nil {
			t.Errorf("the stopped walk %s without an answer stayed", id)
		}
	}
}

func TestRegenerateWalkStopsTheRunsUnderIt(t *testing.T) {
	app := newTestApp(t)
	app.queue = make(chan func(), 64)
	app.cfg.DeeperAhead = false
	fake := newFakeAgent(t, app)
	first := writeOverview(t, app, fake, "v1", overviewAnswer("First summary.", "Keys"))
	deeper := app.rev.AddWalk(first.ID, first.Walk.Steps[0].ID, "")
	deeper.Content = &review.Walkthrough{Steps: []review.Step{{ID: "inner", Title: "Inner"}}}
	child := app.rev.AddWalk(deeper.ID, "inner", "")
	child.Content = &review.Walkthrough{Steps: []review.Step{{ID: "deep", Title: "Deep"}}}
	grandchild := app.rev.AddWalk(child.ID, "deep", "")
	fake.hold()
	app.runWalk(app.rev, grandchild)
	app.askAbout(deeper.ID, "inner")
	app.askConfirm = func(_, _, _ string, fn func()) { fn() }
	app.regenerate(deeper.ID)
	if app.runner.run(app.rev, grandchild.ID) != nil {
		t.Error("the run of a walk two levels under the regenerated one goes on")
	}
	if app.questionOpen() {
		t.Error("the Ask panel about a step the regenerated walk no longer has stayed open")
	}
}

func TestVersionLabels(t *testing.T) {
	at := func(clock string) time.Time {
		parsed, err := time.Parse("2006-01-02 15:04:05", "2026-11-03 "+clock)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	versions := []*review.Overview{
		{Version: 5, Generated: at("22:48:30"), Agent: agent.Claude, Prompt: review.SourceRepo},
		{Version: 4, Generated: at("22:48:05"), Agent: agent.Codex, Prompt: review.SourceRepo},
		{Version: 3, Generated: at("18:10:00"), Agent: agent.OpenCode, Prompt: review.SourceSettings},
		{Version: 2, Generated: at("11:02:00"), Agent: agent.Claude, Prompt: review.SourceDefault},
		{Version: 1, Generated: at("09:00:00"), Agent: agent.Claude},
	}
	want := []string{
		"22:48 Nov 3 · repo prompt by Claude (latest)",
		"22:48 Nov 3 · repo prompt by Codex",
		"18:10 Nov 3 · settings prompt by OpenCode",
		"11:02 Nov 3 · default prompt by Claude",
		"09:00 Nov 3 by Claude",
	}
	if got := versionLabels(versions, versions[0]); !slices.Equal(got, want) {
		t.Errorf("labels =\n%q\nwant\n%q", got, want)
	}

	versions[4].Generated, versions[4].Prompt = at("11:02:40"), review.SourceDefault
	got := versionLabels(versions, versions[0])
	if !slices.Equal(got[3:], []string{"11:02:00 Nov 3 · default prompt by Claude", "11:02:40 Nov 3 · default prompt by Claude"}) {
		t.Errorf("labels in the same minute = %q", got[3:])
	}

	versions[4].Generated = versions[3].Generated
	got = versionLabels(versions, versions[0])
	if !slices.Equal(got[3:], []string{"11:02:00 Nov 3 · default prompt by Claude (v2)", "11:02:00 Nov 3 · default prompt by Claude (v1)"}) {
		t.Errorf("labels in the same second = %q", got[3:])
	}
}
