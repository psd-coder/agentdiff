package app

import (
	"context"
	"slices"
	"time"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/review"
	"github.com/psd-coder/agentdiff/internal/store"
)

// runKey names an agent run: the review it writes into, and its target,
// see review.Exchange.Target.
type runKey struct {
	rev    *review.Review
	target string
}

// agentRun is an answer the agent writes for the overview, a walk or a
// thread. text and activity are what it streamed so far.
type agentRun struct {
	text     string
	activity string
	started  time.Time
	ex       *review.Exchange
	// ahead marks a walk the app writes ahead of the reviewer. Only
	// these runs count toward maxAhead.
	ahead  bool
	cancel context.CancelFunc
	ended  func(answered bool)
}

// maxAhead is how many walkthroughs the app writes at once ahead of the
// reviewer, over all reviews.
const maxAhead = 3

// streamFunc starts the agent for req and hands its events to onEvent on
// the main thread.
type streamFunc func(ctx context.Context, cfg agent.Config, req agent.Request, rev *review.Review, info store.RunInfo, onEvent func(agent.Event))

// buildFunc builds a write-ahead run when it leaves the queue, from the
// review as it is then. ok is false when there is nothing to write any
// more.
type buildFunc func() (ex *review.Exchange, title string, ok bool)

// runner holds every agent run and the write-ahead walks that wait for a
// free slot. A run belongs to the review it was started for, so switching
// scopes leaves it running. At most one run writes into a target of a
// review, and a run writes its events into its exchange until it ends.
//
// A run ends once: the runner forgets it, writes the result into the
// exchange, saves the review, calls the run's ended and starts the queued
// walks that now have a slot. A run that a new run of the same target
// replaces, or that release drops, ends without a write and without
// ended, and its later events change nothing.
//
// All methods and the events run on the main thread, so the runner has
// no locks. ended may call the runner again.
type runner struct {
	stream streamFunc
	save   func(*review.Review)
	runs   map[runKey]*agentRun
	// waiting is oldest first.
	waiting []waitingRun
}

type waitingRun struct {
	key   runKey
	build buildFunc
}

func newRunner(stream streamFunc, save func(*review.Review)) *runner {
	return &runner{stream: stream, save: save, runs: map[runKey]*agentRun{}}
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

// start asks the agent for ex at once, in place of a run or a queued
// walk of the same target. title and note go to the run log. ended runs
// after the run wrote its result, nil for none.
func (r *runner) start(ex *review.Exchange, title, note string, ended func(answered bool)) {
	r.launch(&agentRun{ex: ex, ended: ended}, title, note)
}

// ahead puts a write-ahead run for target at the end of the queue. build
// makes it when a slot is free. A run that build declines takes no slot.
func (r *runner) ahead(rev *review.Review, target string, build buildFunc) {
	r.waiting = append(r.waiting, waitingRun{runKey{rev, target}, build})
	r.startWaiting()
}

func (r *runner) startWaiting() {
	for len(r.waiting) > 0 && r.aheadCount() < maxAhead {
		next := r.waiting[0]
		r.waiting = r.waiting[1:]
		r.launchAhead(next)
	}
}

// startNow starts the queued walk of rev for target at once, past the
// limit of slots.
func (r *runner) startNow(rev *review.Review, target string) {
	i := slices.IndexFunc(r.waiting, func(q waitingRun) bool { return q.key == runKey{rev, target} })
	if i < 0 {
		return
	}
	next := r.waiting[i]
	r.waiting = slices.Delete(r.waiting, i, i+1)
	r.launchAhead(next)
}

func (r *runner) launchAhead(q waitingRun) {
	if ex, title, ok := q.build(); ok {
		r.launch(&agentRun{ex: ex, ahead: true}, title, "")
	}
}

func (r *runner) aheadCount() int {
	n := 0
	for _, run := range r.runs {
		if run.ahead {
			n++
		}
	}
	return n
}

func (r *runner) launch(run *agentRun, title, note string) {
	rev, cfg, target := run.ex.Review(), run.ex.Config(), run.ex.Target()
	key := runKey{rev, target}
	r.unqueue(rev, []string{target})
	old := r.runs[key]
	if old != nil {
		old.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel
	run.started = time.Now()
	run.activity = "Starting " + providerName(cfg.Provider) + "…"
	if run.ex.Kind() == review.KindThread {
		run.activity = "Thinking…"
	}
	r.runs[key] = run
	r.save(rev)
	info := store.RunInfo{
		ID: store.NewRunID(run.started), Target: target, Title: title, Note: note,
		Agent: cfg.Provider, Model: cfg.Model, Effort: cfg.Effort, Started: run.started,
	}
	r.stream(ctx, cfg, run.ex.Request, rev, info, func(ev agent.Event) {
		if r.runs[key] != run {
			return
		}
		switch ev.Kind {
		case agent.EventSession:
			run.ex.Started(ev.SessionID)
		case agent.EventText:
			run.text += ev.Text
			run.activity = ""
			if run.ex.Kind() == review.KindWalk {
				run.activity = "Writing the walkthrough…"
			}
		case agent.EventActivity:
			run.activity = ev.Text
		case agent.EventDone:
			r.end(key, run, true, func(at time.Time) { run.ex.Answered(answer(ev.Text, run.text), at) })
		case agent.EventError:
			r.end(key, run, false, func(at time.Time) { run.ex.Failed(ev.Err.Error(), at) })
		}
	})
	// A user's run in place of a write-ahead walk frees its slot.
	if old != nil && old.ahead {
		r.startWaiting()
	}
}

// stop ends the runs of rev for targets at once with "Stopped". A target
// that waits in the queue leaves it without a trace. All of them leave
// the queue first, so that a stop does not start a waiting one among them.
func (r *runner) stop(rev *review.Review, targets ...string) {
	r.unqueue(rev, targets)
	for _, target := range targets {
		key := runKey{rev, target}
		if run := r.runs[key]; run != nil {
			r.end(key, run, false, run.ex.Stopped)
		}
	}
}

func (r *runner) unqueue(rev *review.Review, targets []string) {
	r.waiting = slices.DeleteFunc(r.waiting, func(q waitingRun) bool {
		return q.key.rev == rev && slices.Contains(targets, q.key.target)
	})
}

// release stops every run of rev and the walks queued for it without
// writing into rev. Queued walks of other reviews take the free slots.
func (r *runner) release(rev *review.Review) {
	for key, run := range r.runs {
		if key.rev == rev {
			run.cancel()
			delete(r.runs, key)
		}
	}
	r.waiting = slices.DeleteFunc(r.waiting, func(q waitingRun) bool { return q.key.rev == rev })
	r.startWaiting()
}

// end forgets the run before anything else, so that ended, which may
// start runs, finds its slot free.
func (r *runner) end(key runKey, run *agentRun, answered bool, write func(time.Time)) {
	delete(r.runs, key)
	run.cancel()
	write(time.Now())
	r.save(key.rev)
	if run.ended != nil {
		run.ended(answered)
	}
	if run.ahead {
		r.startWaiting()
	}
}
