package app

import (
	"errors"
	"testing"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/review"
)

// newTestTargets follows a test runner. The targets in onScreen are on
// screen.
func newTestTargets() (*targets, *runner, *fakeStream, map[string]bool) {
	agentRunner, stream := newTestRunner()
	onScreen := map[string]bool{}
	return newTargets(agentRunner, func(_ *review.Review, target string) bool { return onScreen[target] }), agentRunner, stream, onScreen
}

func startThread(agentRunner *runner, rev *review.Review, th *review.Thread) {
	agentRunner.start(review.ThreadExchange(rev, th, runnerCfg, nil, "", "Why?"), "Thread", "", nil)
}

func failed(msg string) agent.Event { return agent.Event{Kind: agent.EventError, Err: errors.New(msg)} }

func TestTargetStateRunBeatsSavedFailure(t *testing.T) {
	ts, agentRunner, _, _ := newTestTargets()
	rev := newRunnerReview()
	rev.Overview = &review.Overview{Markdown: "Old.", Err: "boom"}
	if got := ts.state(rev, review.TargetOverview); got != (targetState{status: targetFailed}) {
		t.Fatalf("before the run: %+v", got)
	}
	agentRunner.start(review.OverviewExchange(rev, review.ChangeSet{}, runnerCfg, nil), "Overview", "", nil)
	if got := ts.state(rev, review.TargetOverview); got.status != targetRunning || !got.busy() {
		t.Errorf("during the run: %+v", got)
	}
}

func TestTargetStateQueued(t *testing.T) {
	ts, agentRunner, _, _ := newTestTargets()
	rev := newRunnerReview()
	walks := writeAhead(agentRunner, rev, maxAhead+1)
	if got := ts.state(rev, walks[maxAhead].ID); got.status != targetQueued || !got.busy() {
		t.Errorf("the walk past the slots: %+v", got)
	}
	if got := ts.state(rev, walks[0].ID); got.status != targetRunning {
		t.Errorf("the first walk: %+v", got)
	}
}

func TestTargetStateUnseen(t *testing.T) {
	for _, tc := range []struct {
		name     string
		onScreen bool
		end      func(subtest *testing.T, agentRunner *runner, stream *fakeStream, rev *review.Review, th *review.Thread)
		want     targetState
	}{
		{
			name: "an answer off screen",
			end: func(t *testing.T, _ *runner, f *fakeStream, rev *review.Review, th *review.Thread) {
				f.last(t, rev, th.ID).onEvent(done("Because."))
			},
			want: targetState{status: targetAnswered, unseen: true},
		},
		{
			name: "a failure off screen",
			end: func(t *testing.T, _ *runner, f *fakeStream, rev *review.Review, th *review.Thread) {
				f.last(t, rev, th.ID).onEvent(failed("boom"))
			},
			want: targetState{status: targetFailed, unseen: true},
		},
		{
			name:     "an answer on screen",
			onScreen: true,
			end: func(t *testing.T, _ *runner, f *fakeStream, rev *review.Review, th *review.Thread) {
				f.last(t, rev, th.ID).onEvent(done("Because."))
			},
			want: targetState{status: targetAnswered},
		},
		{
			name: "a stop off screen",
			end: func(_ *testing.T, r *runner, _ *fakeStream, rev *review.Review, th *review.Thread) {
				r.stop(rev, th.ID)
			},
			want: targetState{status: targetStopped},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts, agentRunner, stream, onScreen := newTestTargets()
			rev := newRunnerReview()
			th := rev.AddThread(review.Anchor{Kind: review.AnchorGeneral})
			onScreen[th.ID] = tc.onScreen
			startThread(agentRunner, rev, th)
			tc.end(t, agentRunner, stream, rev, th)
			if got := ts.state(rev, th.ID); got != tc.want {
				t.Errorf("state = %+v, want %+v", got, tc.want)
			}
			if got := ts.unseenIn(rev); got != tc.want.unseen {
				t.Errorf("unseenIn = %v, want %v", got, tc.want.unseen)
			}
		})
	}
}

func TestWriteAheadWalkEndsWithoutUnseenMark(t *testing.T) {
	ts, agentRunner, stream, _ := newTestTargets()
	rev := newRunnerReview()
	walk := writeAhead(agentRunner, rev, 1)[0]
	stream.last(t, rev, walk.ID).onEvent(done("Answer."))
	if got := ts.state(rev, walk.ID); got != (targetState{status: targetAnswered}) {
		t.Errorf("state = %+v", got)
	}
}

func TestSeenClearsTheUnseenMark(t *testing.T) {
	ts, agentRunner, stream, _ := newTestTargets()
	rev := newRunnerReview()
	first, second := rev.AddThread(review.Anchor{}), rev.AddThread(review.Anchor{})
	for _, th := range []*review.Thread{first, second} {
		startThread(agentRunner, rev, th)
		stream.last(t, rev, th.ID).onEvent(done("Because."))
	}
	ts.seen(rev, first.ID)
	if ts.state(rev, first.ID).unseen || !ts.state(rev, second.ID).unseen || !ts.unseenIn(rev) {
		t.Fatalf("after opening a: a unseen %v, b unseen %v", ts.state(rev, first.ID).unseen, ts.state(rev, second.ID).unseen)
	}
	rev.DeleteThread(second.ID)
	if ts.unseenIn(rev) {
		t.Error("a deleted thread keeps its review unseen")
	}
}
