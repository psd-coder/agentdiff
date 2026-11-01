package app

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
	"github.com/psd-coder/agentdiff/internal/store"
)

// streamCall is a run the runner started. The test sends the agent's
// events through onEvent.
type streamCall struct {
	ctx     context.Context
	rev     *review.Review
	info    store.RunInfo
	onEvent func(agent.Event)
}

type fakeStream struct{ calls []*streamCall }

func (f *fakeStream) stream(ctx context.Context, _ agent.Config, _ agent.Request, rev *review.Review, info store.RunInfo, onEvent func(agent.Event)) {
	f.calls = append(f.calls, &streamCall{ctx, rev, info, onEvent})
}

// last is the latest run started for target of rev.
func (f *fakeStream) last(t *testing.T, rev *review.Review, target string) *streamCall {
	t.Helper()
	for _, c := range slices.Backward(f.calls) {
		if c.rev == rev && c.info.Target == target {
			return c
		}
	}
	t.Fatalf("no run for %s", target)
	return nil
}

func newTestRunner() (*runner, *fakeStream) {
	f := &fakeStream{}
	return newRunner(f.stream, func(*review.Review) {}), f
}

func newRunnerReview() *review.Review {
	return review.New("/repo", gitx.Scope{Kind: gitx.ScopeUncommitted})
}

var runnerCfg = agent.Config{Provider: agent.Claude}

func done(text string) agent.Event { return agent.Event{Kind: agent.EventDone, Text: text} }

func startWalk(r *runner, rev *review.Review, w *review.Walk) {
	r.start(review.DeeperExchange(rev, w, runnerCfg, nil, ""), "Deeper", "", nil)
}

// writeAhead adds n step walks to rev and queues each to be written
// ahead.
func writeAhead(r *runner, rev *review.Review, n int) []*review.Walk {
	var walks []*review.Walk
	for i := range n {
		w := rev.AddWalk("", i, "")
		walks = append(walks, w)
		r.ahead(rev, w.ID, func() (*review.Exchange, string, bool) {
			if rev.Walk(w.ID) == nil {
				return nil, "", false
			}
			return review.DeeperExchange(rev, w, runnerCfg, nil, ""), fmt.Sprintf("Step %d", i), true
		})
	}
	return walks
}

// running reports for each walk whether its run goes on now.
func running(r *runner, rev *review.Review, walks []*review.Walk) []bool {
	var out []bool
	for _, w := range walks {
		out = append(out, r.run(rev, w.ID) != nil)
	}
	return out
}

func TestQuestionSkipsWriteAheadQueue(t *testing.T) {
	r, _ := newTestRunner()
	rev := newRunnerReview()
	walks := writeAhead(r, rev, 4)
	q := rev.AddWalk("", -1, "Why?")
	startWalk(r, rev, q)
	if r.run(rev, q.ID) == nil {
		t.Fatal("the question waits")
	}
	if got := running(r, rev, walks); !slices.Equal(got, []bool{true, true, true, false}) || !r.queued(rev, walks[3].ID) {
		t.Errorf("running %v, the fourth step queued %v", got, r.queued(rev, walks[3].ID))
	}
}

func TestOtherReviewTakesNoWriteAheadSlot(t *testing.T) {
	r, _ := newTestRunner()
	other, rev := newRunnerReview(), newRunnerReview()
	for range 3 {
		startWalk(r, other, other.AddWalk("", -1, "Why?"))
	}
	walks := writeAhead(r, rev, 4)
	if got := running(r, rev, walks); !slices.Equal(got, []bool{true, true, true, false}) || !r.queued(rev, walks[3].ID) {
		t.Errorf("running %v, the fourth step queued %v", got, r.queued(rev, walks[3].ID))
	}
}

func TestWriteAheadQueueStartsInOrder(t *testing.T) {
	r, _ := newTestRunner()
	rev := newRunnerReview()
	w := writeAhead(r, rev, 5)
	if got := running(r, rev, w); !slices.Equal(got, []bool{true, true, true, false, false}) {
		t.Fatalf("running %v", got)
	}
	r.stop(rev, w[1].ID)
	if got := running(r, rev, w); !slices.Equal(got, []bool{true, false, true, true, false}) || !r.queued(rev, w[4].ID) {
		t.Errorf("after stopping step 1: running %v, step 4 queued %v", got, r.queued(rev, w[4].ID))
	}
}

func TestWriteAheadAnswerStartsNextQueuedWalk(t *testing.T) {
	r, f := newTestRunner()
	rev := newRunnerReview()
	w := writeAhead(r, rev, 4)
	f.last(t, rev, w[0].ID).onEvent(done("Answer."))
	if got := running(r, rev, w); !slices.Equal(got, []bool{false, true, true, true}) {
		t.Errorf("running %v", got)
	}
}

func TestDeclinedWriteAheadTakesNoSlot(t *testing.T) {
	r, _ := newTestRunner()
	rev := newRunnerReview()
	w := writeAhead(r, rev, 5)
	rev.DeleteWalk(w[3].ID)
	r.stop(rev, w[0].ID)
	if r.run(rev, w[3].ID) != nil || r.run(rev, w[4].ID) == nil {
		t.Errorf("deleted step runs %v, the next step runs %v", r.run(rev, w[3].ID) != nil, r.run(rev, w[4].ID) != nil)
	}
}

func TestStopQueuedWalk(t *testing.T) {
	r, _ := newTestRunner()
	rev := newRunnerReview()
	w := writeAhead(r, rev, 4)
	r.stop(rev, w[3].ID)
	if r.queued(rev, w[3].ID) || w[3].Err != "" {
		t.Errorf("stopped queued step: queued %v, err %q", r.queued(rev, w[3].ID), w[3].Err)
	}
	r.stop(rev, w[0].ID)
	if r.run(rev, w[3].ID) != nil {
		t.Error("the stopped step started when a slot freed")
	}
}

func TestStartQueuedWalkNow(t *testing.T) {
	r, _ := newTestRunner()
	rev := newRunnerReview()
	w := writeAhead(r, rev, 5)
	r.startNow(rev, w[4].ID)
	if got := running(r, rev, w); !slices.Equal(got, []bool{true, true, true, false, true}) || !r.queued(rev, w[3].ID) {
		t.Errorf("running %v, step 3 queued %v", got, r.queued(rev, w[3].ID))
	}
}

func TestOtherReviewWriteAheadSharesSlots(t *testing.T) {
	r, _ := newTestRunner()
	other, rev := newRunnerReview(), newRunnerReview()
	writeAhead(r, other, 3)
	w := writeAhead(r, rev, 1)
	if !r.queued(rev, w[0].ID) {
		t.Error("the walk does not wait for the other review's walks")
	}
}

func TestUserRunFreesWriteAheadSlot(t *testing.T) {
	r, _ := newTestRunner()
	rev := newRunnerReview()
	w := writeAhead(r, rev, 4)
	startWalk(r, rev, w[0])
	if r.run(rev, w[0].ID) == nil {
		t.Fatal("the user's run does not go on")
	}
	if r.run(rev, w[3].ID) == nil {
		t.Error("the fourth step still waits")
	}
}

func TestReleaseFreesSlotsForOtherReviews(t *testing.T) {
	r, f := newTestRunner()
	gone, rev := newRunnerReview(), newRunnerReview()
	goneWalks := writeAhead(r, gone, 3)
	w := writeAhead(r, rev, 1)
	first := f.last(t, gone, goneWalks[0].ID)
	r.release(gone)
	if r.reviewBusy(gone) || first.ctx.Err() == nil {
		t.Errorf("released review busy %v, its run cancelled %v", r.reviewBusy(gone), first.ctx.Err() != nil)
	}
	if r.run(rev, w[0].ID) == nil {
		t.Error("the other review's walk still waits")
	}
	first.onEvent(done("Late."))
	if goneWalks[0].Markdown != "" || goneWalks[0].Err != "" {
		t.Errorf("released walk: markdown %q, err %q", goneWalks[0].Markdown, goneWalks[0].Err)
	}
}

func TestReplacedRunIsSilent(t *testing.T) {
	r, f := newTestRunner()
	rev := newRunnerReview()
	w := rev.AddWalk("", -1, "Why?")
	var ended []bool
	r.start(review.DeeperExchange(rev, w, runnerCfg, nil, ""), "Old", "", func(answered bool) { ended = append(ended, answered) })
	old := f.last(t, rev, w.ID)
	startWalk(r, rev, w)
	if old.ctx.Err() == nil {
		t.Error("the replaced run goes on")
	}
	old.onEvent(done("Late."))
	if w.Markdown != "" || w.Err != "" || len(ended) != 0 || r.run(rev, w.ID) == nil {
		t.Errorf("after a late event: markdown %q, err %q, ended %v, running %v", w.Markdown, w.Err, ended, r.run(rev, w.ID) != nil)
	}
}

func TestStopWalkRecordsStoppedOnce(t *testing.T) {
	r, f := newTestRunner()
	rev := newRunnerReview()
	w := rev.AddWalk("", -1, "Why?")
	var ended []bool
	r.start(review.DeeperExchange(rev, w, runnerCfg, nil, ""), "Question", "", func(answered bool) { ended = append(ended, answered) })
	r.stop(rev, w.ID)
	if r.busy(rev, w.ID) || w.Err != "Stopped" || !slices.Equal(ended, []bool{false}) {
		t.Fatalf("busy %v, err %q, ended %v right after Stop", r.busy(rev, w.ID), w.Err, ended)
	}
	stopped := w.Generated
	f.last(t, rev, w.ID).onEvent(done("Late."))
	if w.Err != "Stopped" || !w.Generated.Equal(stopped) || len(ended) != 1 {
		t.Errorf("err %q, generated %v, want %v, ended %v", w.Err, w.Generated, stopped, ended)
	}
}

func TestStopThreadRecordsStoppedOnce(t *testing.T) {
	r, f := newTestRunner()
	rev := newRunnerReview()
	th := rev.AddThread(review.Anchor{Kind: review.AnchorGeneral})
	r.start(review.ThreadExchange(rev, th, runnerCfg, nil, "", "Why?"), "Thread", "Why?", nil)
	r.stop(rev, th.ID)
	if r.busy(rev, th.ID) {
		t.Fatal("the thread is busy after Stop")
	}
	f.last(t, rev, th.ID).onEvent(done("Late."))
	if len(th.Messages) != 2 || th.Messages[1].Err != "Stopped" {
		t.Errorf("messages %+v", th.Messages)
	}
}

func TestSameWalkInReviewAndCopy(t *testing.T) {
	r, f := newTestRunner()
	rev, cp := newRunnerReview(), newRunnerReview()
	w := rev.AddWalk("", -1, "Why?")
	wc := &review.Walk{ID: w.ID, Step: -1, Question: "Why?"}
	cp.Walks = append(cp.Walks, wc)
	startWalk(r, rev, w)
	startWalk(r, cp, wc)
	if r.run(rev, w.ID) == nil || r.run(cp, w.ID) == nil {
		t.Fatal("the walk does not run in both reviews")
	}
	f.last(t, rev, w.ID).onEvent(done("In the review."))
	f.last(t, cp, w.ID).onEvent(done("In the copy."))
	if w.Markdown != "In the review." || wc.Markdown != "In the copy." {
		t.Errorf("walk of the review %q, walk of the copy %q", w.Markdown, wc.Markdown)
	}
}
