package app

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
	"github.com/psd-coder/agentdiff/internal/store"
)

// settled waits until every run in the log of rev has ended, so the
// events the agent sent after a stop have reached the app.
func settled(t *testing.T, a *app, rev *review.Review) {
	t.Helper()
	pump(t, a, "the ended runs", func() bool {
		for _, r := range a.runsOf(rev) {
			if r.running {
				return false
			}
		}
		return true
	})
}

// TestSameWalkInReviewAndCopy runs a walk in a review and the same walk
// in a copy of the review: each run writes its own answer.
func TestSameWalkInReviewAndCopy(t *testing.T) {
	a := newTestApp(t)
	a.queue = make(chan func(), 64)
	fake := newFakeAgent(t, a)
	w := a.rev.AddWalk("", -1, "Why?")
	a.saveReview()
	cp := a.store.LoadReview(a.repo.Root, a.scope)
	cp.Scope = gitx.Scope{Kind: gitx.ScopeCommit, Commit: "HEAD"}
	wc := cp.Walk(w.ID)

	fake.set(fakeReply{session: "s1", answer: "In the review.", hold: true})
	a.runWalk(a.rev, w, false)
	fake.set(fakeReply{session: "s2", answer: "In the copy.", hold: true})
	a.runWalk(cp, wc, false)
	if a.running(a.rev, w.ID) == nil || a.running(cp, w.ID) == nil {
		t.Fatal("the walk does not run in both reviews")
	}
	fake.release()
	pump(t, a, "both answers", func() bool { return a.running(a.rev, w.ID) == nil && a.running(cp, w.ID) == nil })
	if w.Markdown != "In the review." || w.Err != "" {
		t.Errorf("walk of the review: markdown %q, err %q", w.Markdown, w.Err)
	}
	if wc.Markdown != "In the copy." || wc.Err != "" {
		t.Errorf("walk of the copy: markdown %q, err %q", wc.Markdown, wc.Err)
	}
}

// TestStopOverviewKeepsOldContent stops an overview that replaces a
// written one: the run ends at once, "Stopped" shows over the old
// overview, and the agent's late events change nothing.
func TestStopOverviewKeepsOldContent(t *testing.T) {
	a := newTestApp(t)
	a.queue = make(chan func(), 64)
	fake := newFakeAgent(t, a)
	a.rev.Overview = &review.Overview{Markdown: "## Summary\nThe old overview.", Generated: time.Now()}
	fake.hold()
	a.generateOverview()
	a.stopOverview()
	if a.overviewRunning() != nil || a.reviewBusy(a.rev) {
		t.Fatal("the overview still runs after Stop")
	}
	settled(t, a, a.rev)
	ov := a.rev.Overview
	if ov.Err != "Stopped" || ov.Markdown != "## Summary\nThe old overview." {
		t.Fatalf("overview err %q, markdown %q", ov.Err, ov.Markdown)
	}
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	if !tt.HasText("Stopped") || !tt.HasText("The old overview.") {
		t.Errorf("texts %q", tt.Texts())
	}
	saved := a.store.LoadReview(a.repo.Root, a.scope).Overview
	if saved == nil || saved.Err != "Stopped" {
		t.Errorf("saved overview %+v", saved)
	}
}

// TestStopWalkRecordsStoppedOnce stops a walk: the run ends at once with
// "Stopped", and the agent's late events change nothing.
func TestStopWalkRecordsStoppedOnce(t *testing.T) {
	a := newTestApp(t)
	a.queue = make(chan func(), 64)
	fake := newFakeAgent(t, a)
	w := a.rev.AddWalk("", -1, "Why?")
	fake.hold()
	a.runWalk(a.rev, w, false)
	a.stopWalk(w.ID)
	if a.walkBusy(w.ID) || w.Err != "Stopped" {
		t.Fatalf("busy %v, err %q right after Stop", a.walkBusy(w.ID), w.Err)
	}
	stopped := w.Generated
	settled(t, a, a.rev)
	if w.Err != "Stopped" || !w.Generated.Equal(stopped) {
		t.Errorf("err %q, generated %v, want %v", w.Err, w.Generated, stopped)
	}
}

// TestStopThreadRecordsStoppedOnce stops an answer in a thread: the run
// ends at once, and the thread gets one "Stopped" reply.
func TestStopThreadRecordsStoppedOnce(t *testing.T) {
	a := newTestApp(t)
	a.queue = make(chan func(), 64)
	fake := newFakeAgent(t, a)
	th := a.rev.AddThread(review.Anchor{Kind: review.AnchorGeneral})
	fake.hold()
	a.ask(a.rev, th, "Why?")
	a.stopThread(th)
	if a.threadBusy(th.ID) {
		t.Fatal("the thread is busy after Stop")
	}
	settled(t, a, a.rev)
	if len(th.Messages) != 2 || th.Messages[1].Err != "Stopped" {
		t.Errorf("messages %+v", th.Messages)
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
	pump(t, a, "the overview", func() bool { return a.overviewRunning() == nil })
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
	pump(t, a, "the reload", func() bool { return !a.loading && !a.switching && a.rev != old })
	fake.hold()
	confirmed()
	if old.Walk(step.ID) == nil {
		t.Error("the archived review lost its step walk")
	}
	if a.running(a.rev, store.TargetOverview) == nil || a.running(old, store.TargetOverview) != nil {
		t.Errorf("overview runs on screen %v, in the archived review %v",
			a.running(a.rev, store.TargetOverview) != nil, a.running(old, store.TargetOverview) != nil)
	}
}

// heldAgent makes every agent run of the app wait until the test ends.
func heldAgent(t *testing.T, a *app) {
	t.Helper()
	a.queue = make(chan func(), 256)
	newFakeAgent(t, a).hold()
}

// writeAhead gives the review on screen an overview of n steps and
// queues the deeper walkthrough of every step.
func writeAhead(t *testing.T, a *app, n int) []*review.Walk {
	t.Helper()
	var steps []review.Step
	for i := range n {
		steps = append(steps, review.Step{Title: fmt.Sprintf("Step %d", i), Text: "Text."})
	}
	a.rev.Overview = &review.Overview{Generated: time.Now(), Walk: &review.Walkthrough{Summary: "s", Steps: steps}}
	a.deeperAhead(a.rev)
	if len(a.rev.Walks) != n {
		t.Fatalf("%d walks, want %d", len(a.rev.Walks), n)
	}
	return slices.Clone(a.rev.Walks)
}

// TestQuestionSkipsWriteAheadQueue asks a question while the write-ahead
// slots are full: the question runs at once and takes no slot.
func TestQuestionSkipsWriteAheadQueue(t *testing.T) {
	a := newTestApp(t)
	heldAgent(t, a)
	walks := writeAhead(t, a, 4)
	a.askQuestion("", -1, "Why?")
	q := a.rev.Walks[len(a.rev.Walks)-1]
	if a.running(a.rev, q.ID) == nil {
		t.Fatal("the question waits")
	}
	if n := a.aheadCount(); n != 3 {
		t.Errorf("%d write-ahead walks run, want 3", n)
	}
	if !a.queued(a.rev, walks[3].ID) {
		t.Error("the fourth step left the queue")
	}
}

// TestOtherReviewTakesNoWriteAheadSlot runs walks the user asked for in
// another review, then writes ahead in the review on screen: all three
// write-ahead slots are free.
func TestOtherReviewTakesNoWriteAheadSlot(t *testing.T) {
	a := newTestApp(t)
	a.saveReview()
	cp := a.store.LoadReview(a.repo.Root, a.scope)
	cp.Scope = gitx.Scope{Kind: gitx.ScopeCommit, Commit: "HEAD"}
	heldAgent(t, a)
	for range 3 {
		a.runWalk(cp, cp.AddWalk("", -1, "Why?"), false)
	}
	walks := writeAhead(t, a, 4)
	for _, w := range walks[:3] {
		if a.running(a.rev, w.ID) == nil {
			t.Errorf("step %d waits", w.Step)
		}
	}
	if !a.queued(a.rev, walks[3].ID) {
		t.Error("the fourth step does not wait")
	}
}

// aheadRunning reports for each walk whether its run goes on now.
func aheadRunning(a *app, walks []*review.Walk) []bool {
	var out []bool
	for _, w := range walks {
		out = append(out, a.running(a.rev, w.ID) != nil)
	}
	return out
}

// TestWriteAheadQueueStartsInOrder runs three write-ahead walks at most,
// and starts the oldest queued walk when a slot frees.
func TestWriteAheadQueueStartsInOrder(t *testing.T) {
	a := newTestApp(t)
	heldAgent(t, a)
	w := writeAhead(t, a, 5)
	if got := aheadRunning(a, w); !slices.Equal(got, []bool{true, true, true, false, false}) {
		t.Fatalf("running %v", got)
	}
	a.stopWalk(w[1].ID)
	if got := aheadRunning(a, w); !slices.Equal(got, []bool{true, false, true, true, false}) || !a.queued(a.rev, w[4].ID) {
		t.Errorf("after stopping step 1: running %v, step 4 queued %v", got, a.queued(a.rev, w[4].ID))
	}
}

// TestStopQueuedWalk stops a queued walk: it leaves the queue without a
// trace, and a later free slot does not start it.
func TestStopQueuedWalk(t *testing.T) {
	a := newTestApp(t)
	heldAgent(t, a)
	w := writeAhead(t, a, 4)
	a.stopWalk(w[3].ID)
	if a.queued(a.rev, w[3].ID) || w[3].Err != "" {
		t.Errorf("stopped queued step: queued %v, err %q", a.queued(a.rev, w[3].ID), w[3].Err)
	}
	a.stopWalk(w[0].ID)
	if a.running(a.rev, w[3].ID) != nil {
		t.Error("the stopped step started when a slot freed")
	}
}

// TestOtherReviewWriteAheadSharesSlots writes ahead in another review
// first: the walks of the review on screen wait, because the limit holds
// for all reviews.
func TestOtherReviewWriteAheadSharesSlots(t *testing.T) {
	a := newTestApp(t)
	a.saveReview()
	cp := a.store.LoadReview(a.repo.Root, a.scope)
	cp.Scope = gitx.Scope{Kind: gitx.ScopeCommit, Commit: "HEAD"}
	heldAgent(t, a)
	cp.Overview = &review.Overview{Generated: time.Now(), Walk: &review.Walkthrough{Summary: "s", Steps: []review.Step{{Title: "A"}, {Title: "B"}, {Title: "C"}}}}
	a.deeperAhead(cp)
	w := writeAhead(t, a, 1)
	if !a.queued(a.rev, w[0].ID) {
		t.Error("the walk on screen does not wait for the other review's walks")
	}
}

// TestUserRunFreesWriteAheadSlot regenerates a walk that the app writes
// ahead: the user's run takes its place, and the next queued walk starts.
func TestUserRunFreesWriteAheadSlot(t *testing.T) {
	a := newTestApp(t)
	heldAgent(t, a)
	w := writeAhead(t, a, 4)
	a.runWalk(a.rev, w[0], false)
	if a.running(a.rev, w[0].ID) == nil {
		t.Fatal("the user's run does not go on")
	}
	if a.running(a.rev, w[3].ID) == nil {
		t.Error("the fourth step still waits")
	}
}
