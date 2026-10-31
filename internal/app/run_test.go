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
func settled(t *testing.T, a *app, rev *review.Review) {
	t.Helper()
	pump(t, a, "the ended runs", func() bool {
		for _, r := range a.logsOf(rev) {
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
	a := newTestApp(t)
	a.queue = make(chan func(), 64)
	fake := newFakeAgent(t, a)
	a.rev.Overview = &review.Overview{Markdown: "## Summary\nThe old overview.", Generated: time.Now()}
	fake.hold()
	a.generateOverview()
	a.runner.stop(a.rev, store.TargetOverview)
	if a.busy(store.TargetOverview) || a.runner.reviewBusy(a.rev) {
		t.Fatal("the overview still runs after Stop")
	}
	settled(t, a, a.rev)
	ov := a.rev.Overview
	if ov.Err != "Stopped" || ov.Markdown != "## Summary\nThe old overview." {
		t.Fatalf("overview err %q, markdown %q", ov.Err, ov.Markdown)
	}
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	if !tt.HasText("Stopped") || !tt.HasText("The old overview.") || tt.HasText("The agent failed") {
		t.Errorf("texts %q", tt.Texts())
	}
	snapshot(t, tt, "overview-stopped")
	saved := a.store.LoadReview(a.repo.Root, a.scope).Overview
	if saved == nil || saved.Err != "Stopped" {
		t.Errorf("saved overview %+v", saved)
	}
}

// TestRegenerateAfterReloadInDialog confirms Regenerate after a reload
// put another review of the scope on screen: the review on screen
// regenerates, and the one that went to the archive keeps its walks.
func TestRegenerateAfterReloadInDialog(t *testing.T) {
	a := newTestApp(t)
	dir := a.repo.Root
	answer := "```json\n" + `{"summary": "Lower-case keys.", "steps": [{"title": "A", "text": "a"}], "groups": []}` + "\n```"
	fake := newFakeAgent(t, a)
	fake.answer("s1", answer)
	a.cfg.DeeperAhead = false
	a.queue = make(chan func(), 64)
	a.generateOverview()
	pump(t, a, "the overview", func() bool { return !a.busy(store.TargetOverview) })
	old := a.rev
	step := old.AddWalk("", 0, "")
	var confirmed func()
	a.askConfirm = func(_, _, _ string, fn func()) { confirmed = fn }
	a.regenerate("")
	if confirmed == nil {
		t.Fatal("Regenerate did not ask")
	}

	git(t, dir, "stash", "-u", "-q")
	write(t, dir, "output.json", "{}\n")
	a.refresh()
	pump(t, a, "the reload", func() bool { return a.loader.idle() && a.rev != old })
	fake.hold()
	confirmed()
	if old.Walk(step.ID) == nil {
		t.Error("the archived review lost its step walk")
	}
	if a.runner.run(a.rev, store.TargetOverview) == nil || a.runner.run(old, store.TargetOverview) != nil {
		t.Errorf("overview runs on screen %v, in the archived review %v",
			a.runner.run(a.rev, store.TargetOverview) != nil, a.runner.run(old, store.TargetOverview) != nil)
	}
}

// TestStreamingRunsShowProgress holds an overview, a walk and a thread
// answer while they stream: each screen shows what the agent does now,
// and the overview and the thread show the text written so far.
func TestStreamingRunsShowProgress(t *testing.T) {
	a := newTestApp(t)
	a.queue = make(chan func(), 64)
	fake := newFakeAgent(t, a)
	tt := ui.NewTester(a.view, 1300, 800)
	streaming := func(what, streamed, activity string, start func(), shown ...string) {
		t.Helper()
		fake.set(fakeReply{session: "s", answer: "Done.", hold: true, streamed: streamed, activity: activity})
		start()
		pump(t, a, what, func() bool {
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

	streaming("overview-streaming", "## Summary\nKeys are lower-cased now.", "Reading cache/cache.go", a.generateOverview, "Keys are lower-cased now.")

	w := a.rev.AddWalk("", -1, "Why lower-case?")
	a.nav.goTo(page{kindWalk, w.ID})
	streaming("walk-streaming", "Partial walkthrough", "Searching for callers", func() { a.runWalk(a.rev, w) })

	th := a.rev.AddThread(review.Anchor{Kind: review.AnchorGeneral})
	a.openThread(th.ID)
	streaming("thread-streaming", "The tests cover upper-case keys.", "Running go test", func() { a.ask(a.rev, th, "And the tests?") }, "The tests cover upper-case keys.")
}
