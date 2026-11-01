package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/psd-coder/agentdiff/internal/review"
	"github.com/psd-coder/agentdiff/internal/store"
)

// settled waits until every run in the log of rev has ended, so the
// events the agent sent after a stop have reached the app.
func settled(t *testing.T, app *app, rev *review.Review) {
	t.Helper()
	pump(t, app, "the ended runs", func() bool {
		for _, r := range app.logsOf(rev) {
			if r.running {
				return false
			}
		}
		return true
	})
}

// TestStopOverviewKeepsOldContent stops an overview that replaces a
// written one: the run ends at once, "Stopped" shows over the old
// overview without the box of a failure, and the agent's late events
// change nothing.
func TestStopOverviewKeepsOldContent(t *testing.T) {
	app := newTestApp(t)
	app.queue = make(chan func(), 64)
	fake := newFakeAgent(t, app)
	app.rev.Overview = &review.Overview{Markdown: "## Summary\nThe old overview.", Generated: time.Now()}
	fake.hold()
	app.generateOverview()
	app.runner.stop(app.rev, store.TargetOverview)
	if app.busy(store.TargetOverview) || app.runner.reviewBusy(app.rev) {
		t.Fatal("the overview still runs after Stop")
	}
	settled(t, app, app.rev)
	ov := app.rev.Overview
	if ov.Err != "Stopped" || ov.Markdown != "## Summary\nThe old overview." {
		t.Fatalf("overview err %q, markdown %q", ov.Err, ov.Markdown)
	}
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	if !tt.HasText("Stopped") || !tt.HasText("The old overview.") || tt.HasText("The agent failed") {
		t.Errorf("texts %q", tt.Texts())
	}
	snapshot(t, tt, "overview-stopped")
	saved := app.store.LoadReview(app.repo.Root, app.scope).Overview
	if saved == nil || saved.Err != "Stopped" {
		t.Errorf("saved overview %+v", saved)
	}
}

// TestRegenerateAfterReloadInDialog confirms Regenerate after a reload
// put another review of the scope on screen: the review on screen
// regenerates, and the one that went to the archive keeps its walks.
func TestRegenerateAfterReloadInDialog(t *testing.T) {
	app := newTestApp(t)
	dir := app.repo.Root
	answer := "```json\n" + `{"summary": "Lower-case keys.", "steps": [{"title": "A", "text": "a"}], "groups": []}` + "\n```"
	fake := newFakeAgent(t, app)
	fake.answer("s1", answer)
	app.cfg.DeeperAhead = false
	app.queue = make(chan func(), 64)
	app.generateOverview()
	pump(t, app, "the overview", func() bool { return !app.busy(store.TargetOverview) })
	old := app.rev
	step := old.AddWalk("", 0, "")
	var confirmed func()
	app.askConfirm = func(_, _, _ string, fn func()) { confirmed = fn }
	app.regenerate("")
	if confirmed == nil {
		t.Fatal("Regenerate did not ask")
	}

	git(t, dir, "stash", "-u", "-q")
	write(t, dir, "output.json", "{}\n")
	app.refresh()
	pump(t, app, "the reload", func() bool { return app.loader.idle() && app.rev != old })
	fake.hold()
	confirmed()
	if old.Walk(step.ID) == nil {
		t.Error("the archived review lost its step walk")
	}
	if app.runner.run(app.rev, store.TargetOverview) == nil || app.runner.run(old, store.TargetOverview) != nil {
		t.Errorf("overview runs on screen %v, in the archived review %v",
			app.runner.run(app.rev, store.TargetOverview) != nil, app.runner.run(old, store.TargetOverview) != nil)
	}
}

// TestStreamingRunsShowProgress holds an overview, a walk and a thread
// answer while they stream: each screen shows what the agent does now,
// and the overview and the thread show the text written so far.
func TestStreamingRunsShowProgress(t *testing.T) {
	app := newTestApp(t)
	app.queue = make(chan func(), 64)
	fake := newFakeAgent(t, app)
	tt := ui.NewTester(app.view, 1300, 800)
	streaming := func(what, streamed, activity string, start func(), shown ...string) {
		t.Helper()
		fake.set(fakeReply{session: "s", answer: "Done.", hold: true, streamed: streamed, activity: activity})
		start()
		pump(t, app, what, func() bool {
			tt.Frame()
			return tt.HasText(activity)
		})
		// Streamed Markdown fades in.
		tt.Advance(time.Second)
		for _, want := range shown {
			if !tt.HasText(want) {
				t.Errorf("%s: view lacks %q", what, want)
			}
		}
		snapshot(t, tt, what)
	}

	streaming("overview-streaming", "## Summary\nKeys are lower-cased now.", "Reading cache/cache.go", app.generateOverview, "Keys are lower-cased now.")

	walk := app.rev.AddWalk("", -1, "Why lower-case?")
	app.nav.goTo(page{kindWalk, walk.ID})
	streaming("walk-streaming", "Partial walkthrough", "Searching for callers", func() { app.runWalk(app.rev, walk) })

	th := app.rev.AddThread(review.Anchor{Kind: review.AnchorGeneral})
	app.openThread(th.ID)
	streaming("thread-streaming", "The tests cover upper-case keys.", "Running go test", func() { app.ask(app.rev, th, "And the tests?") }, "The tests cover upper-case keys.")
}

func TestTextBatchJoinsDeltasUntilFlushTime(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	batch := textBatch{last: start}
	if !batch.empty() {
		t.Fatal("new batch is not empty")
	}
	if batch.add("Hel", start.Add(10*time.Millisecond)) || batch.add("lo", start.Add(39*time.Millisecond)) {
		t.Fatal("asked to flush before 40ms passed")
	}
	if !batch.add("!", start.Add(40*time.Millisecond)) {
		t.Fatal("did not ask to flush once 40ms passed")
	}
	taken := start.Add(41 * time.Millisecond)
	if got := batch.take(taken); got != "Hello!" || !batch.empty() {
		t.Fatalf("take = %q, empty after take %v", got, batch.empty())
	}
	if batch.add("next", taken.Add(39*time.Millisecond)) {
		t.Error("the next batch did not start at the take")
	}
}
