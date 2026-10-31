package app

import (
	"context"
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
// thread. reply is where a thread's answer goes in its messages.
type agentRun struct {
	kind     runKind
	text     string
	activity string
	started  time.Time
	reply    int
	ex       *review.Exchange
	cancel   context.CancelFunc
	// ended runs once on the main thread after endRun wrote the result.
	// A run that a new run with the same key replaces, or that dropRuns
	// drops, ends without it.
	ended func(answered bool)
}

func (a *app) running(rev *review.Review, target string) *agentRun {
	return a.agentRuns[runKey{rev, target}]
}

// walkRunCount counts the walk runs of all reviews.
func (a *app) walkRunCount() int {
	n := 0
	for _, run := range a.agentRuns {
		if run.kind == walkKind {
			n++
		}
	}
	return n
}

// launch asks the agent for ex. A run with the same key stops without
// a trace, because the new run writes the same place.
func (a *app) launch(run *agentRun, info store.RunInfo) {
	rev, cfg := run.ex.Review(), run.ex.Config()
	key := runKey{rev, info.Target}
	if old := a.agentRuns[key]; old != nil {
		old.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel
	run.started = time.Now()
	run.activity = "Starting " + providerName(cfg.Provider) + "…"
	if run.kind == threadKind {
		run.activity = "Thinking…"
	}
	a.agentRuns[key] = run
	a.saveReviewOf(rev)
	a.stream(ctx, cfg, run.ex.Request, rev, info, func(ev agent.Event) {
		if a.agentRuns[key] != run {
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
}

// stopRun ends a run at once with "Stopped". The agent's later events
// find the run gone and change nothing.
func (a *app) stopRun(rev *review.Review, target string) {
	key := runKey{rev, target}
	if run := a.agentRuns[key]; run != nil {
		a.endRun(key, run, false, run.ex.Stopped)
	}
}

// dropRuns stops every run of rev, and the walks queued for it, without
// writing into rev. Queued walks of other reviews take the free places.
func (a *app) dropRuns(rev *review.Review) {
	for key, run := range a.agentRuns {
		if key.rev == rev {
			run.cancel()
			delete(a.agentRuns, key)
		}
	}
	a.unqueueReview(rev)
	a.startAhead()
}

// endRun is the one way a run ends: it forgets the run, writes its result
// into the review and saves it.
func (a *app) endRun(key runKey, run *agentRun, answered bool, write func(time.Time)) {
	delete(a.agentRuns, key)
	run.cancel()
	write(time.Now())
	a.saveReviewOf(key.rev)
	if run.ended != nil {
		run.ended(answered)
	}
}
