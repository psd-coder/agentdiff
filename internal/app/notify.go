package app

import (
	"log"
	"path/filepath"

	"github.com/egoist/mygo"

	"github.com/psd-coder/agentdiff/internal/review"
)

// runNotice is the title and the body of the notification about the run
// of key that just ended. ok is false for a run that needs none: one the
// user stopped, or one whose target is gone.
func runNotice(key runKey) (title, body string, ok bool) {
	rev := key.rev
	kind, found := rev.Resolve(key.target)
	if !found {
		return "", "", false
	}
	what, about := "Overview", filepath.Base(rev.Repo)+" — "+rev.Scope.Label()
	switch kind {
	case review.KindWalk:
		walk := rev.Walk(key.target)
		what, about = "Walkthrough", rev.WalkTitle(walk)
		if walk.Question != "" {
			what = "Answer"
		}
	case review.KindThread:
		what, about = "Reply", rev.Thread(key.target).Anchor.Label()
	case review.KindOverview:
	}
	switch rev.Outcome(key.target) {
	case review.OutcomeAnswered:
		return what + " ready", about, true
	case review.OutcomeFailed:
		return what + " failed", about, true
	case review.OutcomeNone, review.OutcomeStopped:
	}
	return "", "", false
}

// notifyEnd tells the user that a run they started ended while AgentDiff
// was in the background. A click on the notification brings the window to
// the front on the target, when its review is still open.
func (app *app) notifyEnd(key runKey, ahead bool) {
	if ahead || !app.inactive || app.win == nil {
		return
	}
	title, body, ok := runNotice(key)
	if !ok {
		return
	}
	notification := mygo.NewNotification(mygo.NotificationOptions{Title: title, Body: body})
	notification.OnClick(func() {
		app.update(func() {
			app.win.Focus()
			if key.rev == app.rev {
				app.openTarget(key.target)
			}
		})
	})
	go func() {
		// Show waits while macOS asks the user to allow notifications.
		if err := notification.Show(); err != nil {
			log.Printf("notification: %v", err)
		}
	}()
}

// openTarget shows the target of the open review: the overview, a walk on
// its page, or a question or a thread in the side panel.
func (app *app) openTarget(target string) {
	switch kind, _ := app.rev.Resolve(target); kind {
	case review.KindOverview:
		app.openPage(overview)
	case review.KindWalk:
		if app.rev.Walk(target).Question != "" {
			app.openQuestion(target)
			return
		}
		app.openPage(page{kindWalk, target})
	case review.KindThread:
		app.openThread(target)
	}
}
