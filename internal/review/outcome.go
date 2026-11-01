package review

import "slices"

// Stopped is the error of a run that the user stopped. It is saved in the
// Err field like any failure, so a stopped run reads as Failed when it
// does not compare the error with Stopped.
const Stopped = "Stopped"

// Outcome is how the latest answer of a run target ended, as the review
// saved it.
type Outcome uint8

const (
	// OutcomeNone is a target without an answer or a failure: nothing
	// ran yet, or the target does not exist.
	OutcomeNone Outcome = iota
	OutcomeAnswered
	OutcomeFailed
	OutcomeStopped
)

// Outcome tells how the latest answer of target ended: the overview, the
// latest turn of a walk, or the latest agent message of a thread. An
// overview that failed over an earlier explanation failed.
func (review *Review) Outcome(target string) Outcome {
	kind, ok := review.Resolve(target)
	if !ok {
		return OutcomeNone
	}
	switch kind {
	case KindOverview:
		if ov := review.Overview; ov != nil {
			return outcomeOf(ov.Err, ov.Done())
		}
	case KindWalk:
		walk := review.Walk(target)
		if count := len(walk.More); count > 0 {
			turn := walk.More[count-1]
			return outcomeOf(turn.Err, turn.Content != nil || turn.Markdown != "")
		}
		return outcomeOf(walk.Err, walk.Content != nil || walk.Markdown != "")
	case KindThread:
		msgs := review.Thread(target).Messages
		for _, msg := range slices.Backward(msgs) {
			if msg.Role == RoleAgent {
				return outcomeOf(msg.Err, true)
			}
		}
	}
	return OutcomeNone
}

func outcomeOf(err string, answered bool) Outcome {
	switch {
	case err == Stopped:
		return OutcomeStopped
	case err != "":
		return OutcomeFailed
	case answered:
		return OutcomeAnswered
	}
	return OutcomeNone
}

// Resolve tells what the run target id is: the overview, a walk or a
// thread. ok is false when the review has no such target.
func (review *Review) Resolve(id string) (kind Kind, ok bool) {
	switch {
	case id == TargetOverview:
		return KindOverview, true
	case review.Walk(id) != nil:
		return KindWalk, true
	case review.Thread(id) != nil:
		return KindThread, true
	}
	return 0, false
}
