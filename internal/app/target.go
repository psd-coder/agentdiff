package app

import "github.com/psd-coder/agentdiff/internal/review"

// targetStatus is where a run target of a review stands: its run, or how
// its latest answer ended.
type targetStatus uint8

const (
	targetIdle targetStatus = iota
	targetQueued
	targetRunning
	targetAnswered
	targetFailed
	targetStopped
)

// targetState is what the views show for a run target. unseen marks a
// run that ended while the target was not on screen, until the user
// opens it.
type targetState struct {
	status targetStatus
	unseen bool
}

// busy reports whether a run writes into the target or waits in the
// queue to.
func (s targetState) busy() bool { return s.status == targetQueued || s.status == targetRunning }

// targets tells the state of every run target from the runner and the
// saved outcome, and keeps the targets with an unseen result.
type targets struct {
	runner *runner
	unseen map[*review.Review]map[string]bool
}

// newTargets follows the runs of r: a run the user started that ends
// while onScreen is false for its target marks the target unseen. A write-ahead
// walk and a run the user stopped do not.
func newTargets(r *runner, onScreen func(*review.Review, string) bool) *targets {
	t := &targets{runner: r, unseen: map[*review.Review]map[string]bool{}}
	r.onEnd = func(key runKey, ahead bool) {
		if ahead || onScreen(key.rev, key.target) || key.rev.Outcome(key.target) == review.OutcomeStopped {
			return
		}
		if t.unseen[key.rev] == nil {
			t.unseen[key.rev] = map[string]bool{}
		}
		t.unseen[key.rev][key.target] = true
	}
	return t
}

// state is the state of target of rev. A run beats the saved outcome.
func (t *targets) state(rev *review.Review, target string) targetState {
	s := targetState{unseen: t.unseen[rev][target]}
	switch {
	case t.runner.run(rev, target) != nil:
		s.status = targetRunning
	case t.runner.queued(rev, target):
		s.status = targetQueued
	default:
		switch rev.Outcome(target) {
		case review.OutcomeAnswered:
			s.status = targetAnswered
		case review.OutcomeFailed:
			s.status = targetFailed
		case review.OutcomeStopped:
			s.status = targetStopped
		case review.OutcomeNone:
		}
	}
	return s
}

// unseenIn reports whether rev has a target with an unseen result.
func (t *targets) unseenIn(rev *review.Review) bool {
	for target := range t.unseen[rev] {
		if _, ok := rev.Resolve(target); ok {
			return true
		}
	}
	return false
}

// seen clears the unseen mark of target of rev.
func (t *targets) seen(rev *review.Review, target string) {
	delete(t.unseen[rev], target)
}

// forget drops the unseen marks of rev.
func (t *targets) forget(rev *review.Review) { delete(t.unseen, rev) }

// onScreen reports whether the user sees target of rev: the overview of the
// review on screen, the walk on the page or in the side panel, or the
// thread in the side panel.
func (a *app) onScreen(rev *review.Review, target string) bool {
	if rev != a.rev {
		return false
	}
	return target == review.TargetOverview || a.nav.page == (page{kindWalk, target}) ||
		a.questionID() == target || a.threadID() == target
}

// markSeen clears the unseen marks of the targets on screen. The view
// calls it in every frame, so a page that opens in any way clears its
// mark.
func (a *app) markSeen() {
	if a.rev == nil {
		return
	}
	a.targets.seen(a.rev, review.TargetOverview)
	if a.nav.page.kind == kindWalk {
		a.targets.seen(a.rev, a.nav.page.key)
	}
	a.targets.seen(a.rev, a.questionID())
	a.targets.seen(a.rev, a.threadID())
}
