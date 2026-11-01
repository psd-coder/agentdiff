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
func (state targetState) busy() bool {
	return state.status == targetQueued || state.status == targetRunning
}

// targets tells the state of every run target from the runner and the
// saved outcome, and keeps the targets with an unseen result.
type targets struct {
	runner *runner
	unseen map[*review.Review]map[string]bool
}

// newTargets follows the runs of r: a run the user started that ends
// while onScreen is false for its target marks the target unseen. A write-ahead
// walk and a run the user stopped do not.
func newTargets(agentRunner *runner, onScreen func(*review.Review, string) bool) *targets {
	targetSet := &targets{runner: agentRunner, unseen: map[*review.Review]map[string]bool{}}
	agentRunner.onEnd = func(key runKey, ahead bool) {
		if ahead || onScreen(key.rev, key.target) || key.rev.Outcome(key.target) == review.OutcomeStopped {
			return
		}
		if targetSet.unseen[key.rev] == nil {
			targetSet.unseen[key.rev] = map[string]bool{}
		}
		targetSet.unseen[key.rev][key.target] = true
	}
	return targetSet
}

// state is the state of target of rev. A run beats the saved outcome.
func (targetSet *targets) state(rev *review.Review, target string) targetState {
	state := targetState{unseen: targetSet.unseen[rev][target]}
	switch {
	case targetSet.runner.run(rev, target) != nil:
		state.status = targetRunning
	case targetSet.runner.queued(rev, target):
		state.status = targetQueued
	default:
		switch rev.Outcome(target) {
		case review.OutcomeAnswered:
			state.status = targetAnswered
		case review.OutcomeFailed:
			state.status = targetFailed
		case review.OutcomeStopped:
			state.status = targetStopped
		case review.OutcomeNone:
		}
	}
	return state
}

// unseenIn reports whether rev has a target with an unseen result.
func (targetSet *targets) unseenIn(rev *review.Review) bool {
	for target := range targetSet.unseen[rev] {
		if _, ok := rev.Resolve(target); ok {
			return true
		}
	}
	return false
}

// seen clears the unseen mark of target of rev.
func (targetSet *targets) seen(rev *review.Review, target string) {
	delete(targetSet.unseen[rev], target)
}

// forget drops the unseen marks of rev.
func (targetSet *targets) forget(rev *review.Review) { delete(targetSet.unseen, rev) }

// onScreen reports whether the user sees target of rev: the overview of the
// review on screen, the walk on the page or in the side panel, or the
// thread in the side panel.
func (app *app) onScreen(rev *review.Review, target string) bool {
	if rev != app.rev {
		return false
	}
	return target == review.TargetOverview || app.nav.page == (page{kindWalk, target}) ||
		app.questionID() == target || app.threadID() == target
}

// markSeen clears the unseen marks of the targets on screen. The view
// calls it in every frame, so a page that opens in any way clears its
// mark.
func (app *app) markSeen() {
	if app.rev == nil {
		return
	}
	app.targets.seen(app.rev, review.TargetOverview)
	if app.nav.page.kind == kindWalk {
		app.targets.seen(app.rev, app.nav.page.key)
	}
	app.targets.seen(app.rev, app.questionID())
	app.targets.seen(app.rev, app.threadID())
}
