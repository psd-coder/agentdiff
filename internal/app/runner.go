package app

import (
	"context"
	"slices"
	"time"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/review"
	"github.com/psd-coder/agentdiff/internal/store"
)

// runKey names an agent run: the review it writes into, and its target
// in the run log, store.TargetOverview or the ID of a walk or a thread.
type runKey struct {
	rev    *review.Review
	target string
}

type runKind int

const (
	overviewKind runKind = iota
	walkKind
	threadKind
)

// agentRun is an answer the agent writes for the overview, a walk or a
// thread. text and activity are what it streamed so far.
type agentRun struct {
	kind     runKind
	text     string
	activity string
	started  time.Time
	// ahead marks a walk the app writes ahead of the reviewer. Only
	// these runs count toward maxAhead.
	ahead  bool
	ex     *review.Exchange
	cancel context.CancelFunc
	// ended runs once on the main thread after endRun wrote the result.
	// A run that a new run with the same key replaces, or that release
	// drops, ends without it.
	ended func(answered bool)
}

// maxAhead is how many walkthroughs the app writes at once ahead of the
// reviewer, over all reviews.
const maxAhead = 3

// runner holds every agent run, the write-ahead walks that wait for a
// free slot, and the run logs of each review. A run belongs to the review
// it was started for, so switching scopes leaves it running.
type runner struct {
	runs map[runKey]*agentRun
	// waiting is oldest first. start builds the request when the walk
	// leaves the queue, from the review as it is then.
	waiting []waitingRun
	// logs is the run log of each review, newest first, in memory.
	logs map[*review.Review][]*logRun
}

func newRunner() runner {
	return runner{runs: map[runKey]*agentRun{}, logs: map[*review.Review][]*logRun{}}
}

type waitingRun struct {
	key   runKey
	start func()
}

// run is the run that writes into rev for target, nil when there is none.
func (r *runner) run(rev *review.Review, target string) *agentRun {
	return r.runs[runKey{rev, target}]
}

// busy reports whether a run writes into rev for target, or waits in the
// queue to.
func (r *runner) busy(rev *review.Review, target string) bool {
	return r.run(rev, target) != nil || r.queued(rev, target)
}

// aheadCount counts the write-ahead walks that run now.
func (r *runner) aheadCount() int {
	n := 0
	for _, run := range r.runs {
		if run.ahead {
			n++
		}
	}
	return n
}

// runAhead puts a write-ahead run at the end of the queue. start launches
// it when a slot is free.
func (a *app) runAhead(rev *review.Review, target string, start func()) {
	a.runner.waiting = append(a.runner.waiting, waitingRun{runKey{rev, target}, start})
	a.startWaiting()
}

func (a *app) startWaiting() {
	for len(a.runner.waiting) > 0 && a.runner.aheadCount() < maxAhead {
		next := a.runner.waiting[0]
		a.runner.waiting = a.runner.waiting[1:]
		next.start()
	}
}

// queued reports whether a write-ahead run waits for its slot.
func (r *runner) queued(rev *review.Review, target string) bool {
	return slices.ContainsFunc(r.waiting, func(q waitingRun) bool { return q.key == runKey{rev, target} })
}

// reviewBusy reports whether an agent run writes into rev or waits to.
func (r *runner) reviewBusy(rev *review.Review) bool {
	for key := range r.runs {
		if key.rev == rev {
			return true
		}
	}
	return slices.ContainsFunc(r.waiting, func(q waitingRun) bool { return q.key.rev == rev })
}

// launch asks the agent for ex. A run with the same key stops without
// a trace, because the new run writes the same place.
func (a *app) launch(run *agentRun, info store.RunInfo) {
	rev, cfg := run.ex.Review(), run.ex.Config()
	key := runKey{rev, info.Target}
	a.unqueue(rev, []string{info.Target})
	old := a.runner.runs[key]
	if old != nil {
		old.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel
	run.started = time.Now()
	run.activity = "Starting " + providerName(cfg.Provider) + "…"
	if run.kind == threadKind {
		run.activity = "Thinking…"
	}
	a.runner.runs[key] = run
	a.saveReviewOf(rev)
	a.stream(ctx, cfg, run.ex.Request, rev, info, func(ev agent.Event) {
		if a.runner.runs[key] != run {
			return
		}
		switch ev.Kind {
		case agent.EventSession:
			run.ex.Started(ev.SessionID)
		case agent.EventText:
			run.text += ev.Text
			run.activity = ""
			if run.kind == walkKind {
				run.activity = "Writing the walkthrough…"
			}
		case agent.EventActivity:
			run.activity = ev.Text
		case agent.EventDone:
			a.endRun(key, run, true, func(at time.Time) { run.ex.Answered(answer(ev.Text, run.text), at) })
		case agent.EventError:
			a.endRun(key, run, false, func(at time.Time) { run.ex.Failed(ev.Err.Error(), at) })
		}
	})
	// A user's run in place of a write-ahead walk frees its slot.
	if old != nil && old.ahead {
		a.startWaiting()
	}
}

// busy reports whether a run writes into the review on screen for
// target, or waits in the queue to.
func (a *app) busy(target string) bool { return a.runner.busy(a.rev, target) }

func (a *app) stopRun(rev *review.Review, target string) { a.stopRuns(rev, []string{target}) }

// stopRuns ends the runs of rev for targets at once with "Stopped". The
// agent's later events find the runs gone and change nothing. A target
// that waits in the queue leaves it without a trace. All of them leave
// the queue first, so that a stop does not start a waiting one among them.
func (a *app) stopRuns(rev *review.Review, targets []string) {
	a.unqueue(rev, targets)
	for _, target := range targets {
		key := runKey{rev, target}
		if run := a.runner.runs[key]; run != nil {
			a.endRun(key, run, false, run.ex.Stopped)
		}
	}
}

func (a *app) unqueue(rev *review.Review, targets []string) {
	a.runner.waiting = slices.DeleteFunc(a.runner.waiting, func(q waitingRun) bool {
		return q.key.rev == rev && slices.Contains(targets, q.key.target)
	})
}

// release forgets rev: it stops every run of rev and the walks queued
// for it without writing into rev, and drops its run log from memory.
// Queued walks of other reviews take the free places.
func (a *app) release(rev *review.Review) {
	for key, run := range a.runner.runs {
		if key.rev == rev {
			run.cancel()
			delete(a.runner.runs, key)
		}
	}
	a.runner.waiting = slices.DeleteFunc(a.runner.waiting, func(q waitingRun) bool { return q.key.rev == rev })
	delete(a.runner.logs, rev)
	a.startWaiting()
}

// endRun is the one way a run ends: it forgets the run, writes its result
// into the review and saves it. A write-ahead run frees its slot for the
// next one in the queue.
func (a *app) endRun(key runKey, run *agentRun, answered bool, write func(time.Time)) {
	delete(a.runner.runs, key)
	run.cancel()
	write(time.Now())
	a.saveReviewOf(key.rev)
	if run.ended != nil {
		run.ended(answered)
	}
	if run.ahead {
		a.startWaiting()
	}
}
