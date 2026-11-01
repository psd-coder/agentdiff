package app

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/review"
	"github.com/psd-coder/agentdiff/internal/store"
)

// streamFlush is how often streamed text reaches the view: tokens come
// far faster than frames are worth drawing.
const streamFlush = 40 * time.Millisecond

type agentFunc func(context.Context, agent.Config, agent.Request) <-chan agent.Event

// stream runs the agent and hands its events to fn on the main thread,
// text deltas joined into one event per streamFlush. It records the run in
// the log of rev, in memory and in the run's file.
func (app *app) stream(ctx context.Context, cfg agent.Config, req agent.Request, rev *review.Review, info store.RunInfo, fn func(agent.Event)) {
	info.Resumed = req.ResumeID
	rec := &runRecorder{run: app.startRun(rev, info), provider: cfg.Provider}
	req.Log = rec.entry
	events := app.startAgent(ctx, cfg, req)
	repo, scope := rev.Repo, rev.Scope
	app.streams.Go(func() {
		app.createRunFile(rec, rev, repo, scope, info)
		batch := textBatch{last: time.Now()}
		flush := func() {
			items := rec.take()
			if batch.empty() && len(items) == 0 {
				return
			}
			text := batch.take(time.Now())
			app.update(func() {
				rec.run.info.Usage = rec.run.add(items)
				if text != "" {
					fn(agent.Event{Kind: agent.EventText, Text: text})
				}
			})
		}
		for ev := range events {
			if ev.Kind == agent.EventText {
				if batch.add(ev.Text, time.Now()) {
					flush()
				}
				continue
			}
			flush()
			app.update(func() { fn(ev) })
		}
		flush()
		rec.file.close()
		app.update(func() { app.endRun(rec, rev) })
	})
}

// textBatch joins the text deltas of a run, so that streamed text reaches
// the view at most once per streamFlush.
type textBatch struct {
	text strings.Builder
	last time.Time
}

// add appends text and reports whether streamFlush passed since the last
// take.
func (textBatch *textBatch) add(text string, now time.Time) bool {
	textBatch.text.WriteString(text)
	return now.Sub(textBatch.last) >= streamFlush
}

func (textBatch *textBatch) empty() bool { return textBatch.text.Len() == 0 }

// take returns the joined text and starts the next batch at now.
func (textBatch *textBatch) take(now time.Time) string {
	text := textBatch.text.String()
	textBatch.text.Reset()
	textBatch.last = now
	return text
}

func (app *app) generateOverview() {
	if app.rev == nil || len(app.files) == 0 {
		return
	}
	rev := app.rev
	cfg := app.cfg.Agent.Auto()
	changes := review.ChangeSet{Diffstat: app.diffstatOf(rev), Files: app.fileNames(), PatchID: app.fp.Patch, FileIDs: app.fp.Files}
	ex := review.OverviewExchange(rev, changes, cfg, app.prompts(rev.Repo))
	app.runner.start(ex, "Overview", "", func(answered bool) {
		if rev == app.rev {
			app.leaveGone()
		}
		if answered {
			app.deeperAhead(rev)
		}
	})
}

// answer is the final text of a run: the result the CLI reports, or the
// text it streamed when the result is empty.
func answer(result, streamed string) string {
	if strings.TrimSpace(result) != "" {
		return result
	}
	return streamed
}

// regenerate rewrites the walkthrough of parent: the overview for "".
// The deeper walkthroughs into its steps go with the old steps, so the
// user confirms that first. A reload can put another review on screen
// while the dialog is open. Regenerate rewrites the review on screen at
// the moment the user confirms.
func (app *app) regenerate(parent string) {
	shown, repo, scope := app.rev, app.rev.Repo, app.rev.Scope
	run := func() {
		rev := app.rev
		if rev == nil || rev.Repo != repo || rev.Scope != scope {
			return
		}
		// The user did not see the deeper walkthroughs of a review that
		// came in during the dialog, so they stay.
		if rev != shown && rev.HasStepWalks(parent) {
			return
		}
		var steps []string
		for _, w := range rev.Walks {
			if w.Parent == parent && w.Step >= 0 {
				steps = append(steps, w.ID)
			}
		}
		app.runner.stop(rev, steps...)
		rev.DeleteStepWalks(parent)
		app.closeGoneQuestion()
		app.leaveGone()
		if parent == "" {
			app.generateOverview()
		} else if walk := rev.Walk(parent); walk != nil {
			app.runWalk(rev, walk)
		}
	}
	if !app.rev.HasStepWalks(parent) {
		run()
		return
	}
	app.confirm("Regenerate this walkthrough?", "The new walkthrough has new steps, so the deeper walkthroughs into the current steps are deleted. Questions about the page as a whole stay.", "Regenerate", run)
}

// deeper starts a walkthrough one level below parent: into one of its
// steps, or about the question for step -1.
func (app *app) deeper(parent string, step int, question string) {
	walk := app.rev.AddWalk(parent, step, question)
	app.nav.goTo(page{kindWalk, walk.ID})
	app.runWalk(app.rev, walk)
}

// askQuestion starts a question's conversation about a step of parent, or about
// parent as a whole for step -1, in the side panel.
func (app *app) askQuestion(parent string, step int, question string) {
	question = strings.TrimSpace(question)
	if question == "" {
		return
	}
	walk := app.rev.AddWalk(parent, step, question)
	app.openQuestion(walk.ID)
	app.runWalk(app.rev, walk)
}

// deeperAhead queues a deeper walkthrough of every step of the overview,
// so that Go deeper opens one that is already written.
func (app *app) deeperAhead(rev *review.Review) {
	if !app.cfg.DeeperAhead || rev.Overview == nil || rev.Overview.Walk == nil {
		return
	}
	var walks []*review.Walk
	for i := range rev.Overview.Walk.Steps {
		if !slices.ContainsFunc(rev.WalksOn("", i), func(w *review.Walk) bool { return w.Question == "" }) {
			walks = append(walks, rev.AddWalk("", i, ""))
		}
	}
	app.saveReviewOf(rev)
	for _, w := range walks {
		app.runner.ahead(rev, w.ID, func() (*review.Exchange, string, bool) {
			if rev.Walk(w.ID) == nil {
				return nil, "", false
			}
			ex, title := app.walkExchange(rev, w, true)
			return ex, title, true
		})
	}
}

// runWalk asks the agent for a walk's walkthrough. Walks next to each
// other stay apart, because each forks the session of the walkthrough
// above.
func (app *app) runWalk(rev *review.Review, walk *review.Walk) {
	ex, title := app.walkExchange(rev, walk, false)
	app.runner.start(ex, title, "", nil)
}

// walkExchange is the exchange for a walk's walkthrough and its title in
// the run log. An auto walk is one the app writes ahead of the reviewer.
func (app *app) walkExchange(rev *review.Review, walk *review.Walk, auto bool) (*review.Exchange, string) {
	cfg := app.cfg.Agent
	if auto {
		cfg = cfg.Auto()
	}
	title := "Deeper: " + rev.WalkTitle(walk)
	if walk.Question != "" {
		title = "Question: " + walk.Question
	}
	return review.DeeperExchange(rev, walk, cfg, app.prompts(rev.Repo), app.diffstatOf(rev)), title
}

// askMore asks a follow-up question in a question's conversation.
func (app *app) askMore(rev *review.Review, walk *review.Walk, question string) {
	question = strings.TrimSpace(question)
	if question == "" || app.runner.busy(rev, walk.ID) {
		return
	}
	cfg := app.cfg.Agent
	ex := review.FollowupExchange(rev, walk, cfg, app.prompts(rev.Repo), app.diffstatOf(rev), question)
	app.runner.start(ex, "Follow-up: "+question, "", nil)
}

// diffstatOf is the diffstat of rev's changes. The app only reads it for
// the review on screen, so it is empty for any other review.
func (app *app) diffstatOf(rev *review.Review) string {
	if rev != app.rev {
		return ""
	}
	return app.diffstat
}

// deleteWalk deletes a walk and the walks under it, and goes to its
// parent.
func (app *app) deleteWalk(walk *review.Walk) {
	walks := slices.Clone(app.rev.Walks)
	app.rev.DeleteWalk(walk.ID)
	var gone []string
	for _, x := range walks {
		if app.rev.Walk(x.ID) == nil {
			gone = append(gone, x.ID)
		}
	}
	app.runner.stop(app.rev, gone...)
	app.closeGoneQuestion()
	if walk.Parent != "" {
		app.nav.replace(page{kindWalk, walk.Parent})
	}
	app.leaveGone()
	app.saveReview()
}

// comment adds the reviewer's words to a thread without asking anyone.
func (app *app) comment(thread *review.Thread, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	thread.Messages = append(thread.Messages, review.Message{Role: review.RoleUser, Text: text, Time: time.Now()})
	app.saveReview()
}

// ask sends a thread's question to the agent, in the repository and the
// scope of rev.
func (app *app) ask(rev *review.Review, thread *review.Thread, text string) {
	text = strings.TrimSpace(text)
	if text == "" || app.runner.busy(rev, thread.ID) {
		return
	}
	cfg := app.cfg.Agent
	ex := review.ThreadExchange(rev, thread, cfg, app.prompts(rev.Repo), app.diffstatOf(rev), text)
	app.runner.start(ex, thread.Anchor.Label(), excerpt(text, 160), nil)
}

// release forgets rev: it stops every run of rev and the walks queued
// for it without writing into rev, and drops its run log from memory.
func (app *app) release(rev *review.Review) {
	app.runner.release(rev)
	delete(app.logs, rev)
	app.targets.forget(rev)
}

// state is the state of target of the review on screen.
func (app *app) state(target string) targetState { return app.targets.state(app.rev, target) }

// busy reports whether a run writes into the review on screen for
// target, or waits in the queue to.
func (app *app) busy(target string) bool { return app.state(target).busy() }
