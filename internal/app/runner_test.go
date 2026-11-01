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

func (stream *fakeStream) stream(ctx context.Context, _ agent.Config, _ agent.Request, rev *review.Review, info store.RunInfo, onEvent func(agent.Event)) {
	stream.calls = append(stream.calls, &streamCall{ctx, rev, info, onEvent})
}

// last is the latest run started for target of rev.
func (stream *fakeStream) last(t *testing.T, rev *review.Review, target string) *streamCall {
	t.Helper()
	for _, c := range slices.Backward(stream.calls) {
		if c.rev == rev && c.info.Target == target {
			return c
		}
	}
	t.Fatalf("no run for %s", target)
	return nil
}

func newTestRunner() (*runner, *fakeStream) {
	stream := &fakeStream{}
	return newRunner(stream.stream, func(*review.Review) {}), stream
}

func newRunnerReview() *review.Review {
	return review.New("/repo", gitx.Scope{Kind: gitx.ScopeUncommitted})
}

var runnerCfg = agent.Config{Provider: agent.Claude}

func done(text string) agent.Event { return agent.Event{Kind: agent.EventDone, Text: text} }

func startWalk(agentRunner *runner, rev *review.Review, walk *review.Walk) {
	agentRunner.start(review.DeeperExchange(rev, walk, runnerCfg, nil, ""), "Deeper", "", nil)
}

// writeAhead adds n step walks to rev and queues each to be written
// ahead.
func writeAhead(agentRunner *runner, rev *review.Review, count int) []*review.Walk {
	var walks []*review.Walk
	for i := range count {
		walk := rev.AddWalk("", i, "")
		walks = append(walks, walk)
		agentRunner.ahead(rev, walk.ID, func() (*review.Exchange, string, bool) {
			if rev.Walk(walk.ID) == nil {
				return nil, "", false
			}
			return review.DeeperExchange(rev, walk, runnerCfg, nil, ""), fmt.Sprintf("Step %d", i), true
		})
	}
	return walks
}

// running reports for each walk whether its run goes on now.
func running(agentRunner *runner, rev *review.Review, walks []*review.Walk) []bool {
	var out []bool
	for _, w := range walks {
		out = append(out, agentRunner.run(rev, w.ID) != nil)
	}
	return out
}

func TestQuestionSkipsWriteAheadQueue(t *testing.T) {
	agentRunner, _ := newTestRunner()
	rev := newRunnerReview()
	walks := writeAhead(agentRunner, rev, 4)
	question := rev.AddWalk("", -1, "Why?")
	startWalk(agentRunner, rev, question)
	if agentRunner.run(rev, question.ID) == nil {
		t.Fatal("the question waits")
	}
	if got := running(agentRunner, rev, walks); !slices.Equal(got, []bool{true, true, true, false}) || !agentRunner.queued(rev, walks[3].ID) {
		t.Errorf("running %v, the fourth step queued %v", got, agentRunner.queued(rev, walks[3].ID))
	}
}

func TestOtherReviewTakesNoWriteAheadSlot(t *testing.T) {
	agentRunner, _ := newTestRunner()
	other, rev := newRunnerReview(), newRunnerReview()
	for range 3 {
		startWalk(agentRunner, other, other.AddWalk("", -1, "Why?"))
	}
	walks := writeAhead(agentRunner, rev, 4)
	if got := running(agentRunner, rev, walks); !slices.Equal(got, []bool{true, true, true, false}) || !agentRunner.queued(rev, walks[3].ID) {
		t.Errorf("running %v, the fourth step queued %v", got, agentRunner.queued(rev, walks[3].ID))
	}
}

func TestWriteAheadQueueStartsInOrder(t *testing.T) {
	agentRunner, _ := newTestRunner()
	rev := newRunnerReview()
	walks := writeAhead(agentRunner, rev, 5)
	if got := running(agentRunner, rev, walks); !slices.Equal(got, []bool{true, true, true, false, false}) {
		t.Fatalf("running %v", got)
	}
	agentRunner.stop(rev, walks[1].ID)
	if got := running(agentRunner, rev, walks); !slices.Equal(got, []bool{true, false, true, true, false}) || !agentRunner.queued(rev, walks[4].ID) {
		t.Errorf("after stopping step 1: running %v, step 4 queued %v", got, agentRunner.queued(rev, walks[4].ID))
	}
}

func TestWriteAheadAnswerStartsNextQueuedWalk(t *testing.T) {
	agentRunner, stream := newTestRunner()
	rev := newRunnerReview()
	walks := writeAhead(agentRunner, rev, 4)
	stream.last(t, rev, walks[0].ID).onEvent(done("Answer."))
	if got := running(agentRunner, rev, walks); !slices.Equal(got, []bool{false, true, true, true}) {
		t.Errorf("running %v", got)
	}
}

func TestDeclinedWriteAheadTakesNoSlot(t *testing.T) {
	agentRunner, _ := newTestRunner()
	rev := newRunnerReview()
	walks := writeAhead(agentRunner, rev, 5)
	rev.DeleteWalk(walks[3].ID)
	agentRunner.stop(rev, walks[0].ID)
	if agentRunner.run(rev, walks[3].ID) != nil || agentRunner.run(rev, walks[4].ID) == nil {
		t.Errorf("deleted step runs %v, the next step runs %v", agentRunner.run(rev, walks[3].ID) != nil, agentRunner.run(rev, walks[4].ID) != nil)
	}
}

func TestStopQueuedWalk(t *testing.T) {
	agentRunner, _ := newTestRunner()
	rev := newRunnerReview()
	walks := writeAhead(agentRunner, rev, 4)
	agentRunner.stop(rev, walks[3].ID)
	if agentRunner.queued(rev, walks[3].ID) || walks[3].Err != "" {
		t.Errorf("stopped queued step: queued %v, err %q", agentRunner.queued(rev, walks[3].ID), walks[3].Err)
	}
	agentRunner.stop(rev, walks[0].ID)
	if agentRunner.run(rev, walks[3].ID) != nil {
		t.Error("the stopped step started when a slot freed")
	}
}

func TestStartQueuedWalkNow(t *testing.T) {
	agentRunner, _ := newTestRunner()
	rev := newRunnerReview()
	walks := writeAhead(agentRunner, rev, 5)
	agentRunner.startNow(rev, walks[4].ID)
	if got := running(agentRunner, rev, walks); !slices.Equal(got, []bool{true, true, true, false, true}) || !agentRunner.queued(rev, walks[3].ID) {
		t.Errorf("running %v, step 3 queued %v", got, agentRunner.queued(rev, walks[3].ID))
	}
}

func TestOtherReviewWriteAheadSharesSlots(t *testing.T) {
	agentRunner, _ := newTestRunner()
	other, rev := newRunnerReview(), newRunnerReview()
	writeAhead(agentRunner, other, 3)
	walks := writeAhead(agentRunner, rev, 1)
	if !agentRunner.queued(rev, walks[0].ID) {
		t.Error("the walk does not wait for the other review's walks")
	}
}

func TestUserRunFreesWriteAheadSlot(t *testing.T) {
	agentRunner, _ := newTestRunner()
	rev := newRunnerReview()
	walks := writeAhead(agentRunner, rev, 4)
	startWalk(agentRunner, rev, walks[0])
	if agentRunner.run(rev, walks[0].ID) == nil {
		t.Fatal("the user's run does not go on")
	}
	if agentRunner.run(rev, walks[3].ID) == nil {
		t.Error("the fourth step still waits")
	}
}

func TestReleaseFreesSlotsForOtherReviews(t *testing.T) {
	agentRunner, stream := newTestRunner()
	gone, rev := newRunnerReview(), newRunnerReview()
	goneWalks := writeAhead(agentRunner, gone, 3)
	walks := writeAhead(agentRunner, rev, 1)
	first := stream.last(t, gone, goneWalks[0].ID)
	agentRunner.release(gone)
	if agentRunner.reviewBusy(gone) || first.ctx.Err() == nil {
		t.Errorf("released review busy %v, its run cancelled %v", agentRunner.reviewBusy(gone), first.ctx.Err() != nil)
	}
	if agentRunner.run(rev, walks[0].ID) == nil {
		t.Error("the other review's walk still waits")
	}
	first.onEvent(done("Late."))
	if goneWalks[0].Markdown != "" || goneWalks[0].Err != "" {
		t.Errorf("released walk: markdown %q, err %q", goneWalks[0].Markdown, goneWalks[0].Err)
	}
}

func TestReplacedRunIsSilent(t *testing.T) {
	agentRunner, stream := newTestRunner()
	rev := newRunnerReview()
	walk := rev.AddWalk("", -1, "Why?")
	var ended []bool
	agentRunner.start(review.DeeperExchange(rev, walk, runnerCfg, nil, ""), "Old", "", func(answered bool) { ended = append(ended, answered) })
	old := stream.last(t, rev, walk.ID)
	startWalk(agentRunner, rev, walk)
	if old.ctx.Err() == nil {
		t.Error("the replaced run goes on")
	}
	old.onEvent(done("Late."))
	if walk.Markdown != "" || walk.Err != "" || len(ended) != 0 || agentRunner.run(rev, walk.ID) == nil {
		t.Errorf("after a late event: markdown %q, err %q, ended %v, running %v", walk.Markdown, walk.Err, ended, agentRunner.run(rev, walk.ID) != nil)
	}
}

func TestStopWalkRecordsStoppedOnce(t *testing.T) {
	agentRunner, stream := newTestRunner()
	rev := newRunnerReview()
	walk := rev.AddWalk("", -1, "Why?")
	var ended []bool
	agentRunner.start(review.DeeperExchange(rev, walk, runnerCfg, nil, ""), "Question", "", func(answered bool) { ended = append(ended, answered) })
	agentRunner.stop(rev, walk.ID)
	if agentRunner.busy(rev, walk.ID) || walk.Err != "Stopped" || !slices.Equal(ended, []bool{false}) {
		t.Fatalf("busy %v, err %q, ended %v right after Stop", agentRunner.busy(rev, walk.ID), walk.Err, ended)
	}
	stopped := walk.Generated
	stream.last(t, rev, walk.ID).onEvent(done("Late."))
	if walk.Err != "Stopped" || !walk.Generated.Equal(stopped) || len(ended) != 1 {
		t.Errorf("err %q, generated %v, want %v, ended %v", walk.Err, walk.Generated, stopped, ended)
	}
}

func TestStopThreadRecordsStoppedOnce(t *testing.T) {
	agentRunner, stream := newTestRunner()
	rev := newRunnerReview()
	th := rev.AddThread(review.Anchor{Kind: review.AnchorGeneral})
	agentRunner.start(review.ThreadExchange(rev, th, runnerCfg, nil, "", "Why?"), "Thread", "Why?", nil)
	agentRunner.stop(rev, th.ID)
	if agentRunner.busy(rev, th.ID) {
		t.Fatal("the thread is busy after Stop")
	}
	stream.last(t, rev, th.ID).onEvent(done("Late."))
	if len(th.Messages) != 2 || th.Messages[1].Err != "Stopped" {
		t.Errorf("messages %+v", th.Messages)
	}
}

func TestSameWalkInReviewAndCopy(t *testing.T) {
	agentRunner, stream := newTestRunner()
	rev, cp := newRunnerReview(), newRunnerReview()
	walk := rev.AddWalk("", -1, "Why?")
	wc := &review.Walk{ID: walk.ID, Step: -1, Question: "Why?"}
	cp.Walks = append(cp.Walks, wc)
	startWalk(agentRunner, rev, walk)
	startWalk(agentRunner, cp, wc)
	if agentRunner.run(rev, walk.ID) == nil || agentRunner.run(cp, walk.ID) == nil {
		t.Fatal("the walk does not run in both reviews")
	}
	stream.last(t, rev, walk.ID).onEvent(done("In the review."))
	stream.last(t, cp, walk.ID).onEvent(done("In the copy."))
	if walk.Markdown != "In the review." || wc.Markdown != "In the copy." {
		t.Errorf("walk of the review %q, walk of the copy %q", walk.Markdown, wc.Markdown)
	}
}
