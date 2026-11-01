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
func (a *app) stream(ctx context.Context, cfg agent.Config, req agent.Request, rev *review.Review, info store.RunInfo, fn func(agent.Event)) {
	info.Resumed = req.ResumeID
	rec := &runRecorder{run: a.startRun(rev, info), provider: cfg.Provider}
	req.Log = rec.entry
	events := a.startAgent(ctx, cfg, req)
	repo, scope := rev.Repo, rev.Scope
	a.streams.Go(func() {
		a.createRunFile(rec, rev, repo, scope, info)
		batch := textBatch{last: time.Now()}
		flush := func() {
			items := rec.take()
			if batch.empty() && len(items) == 0 {
				return
			}
			text := batch.take(time.Now())
			a.update(func() {
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
			a.update(func() { fn(ev) })
		}
		flush()
		rec.file.close()
		a.update(func() { a.endRun(rec, rev) })
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
func (b *textBatch) add(text string, now time.Time) bool {
	b.text.WriteString(text)
	return now.Sub(b.last) >= streamFlush
}

func (b *textBatch) empty() bool { return b.text.Len() == 0 }

// take returns the joined text and starts the next batch at now.
func (b *textBatch) take(now time.Time) string {
	text := b.text.String()
	b.text.Reset()
	b.last = now
	return text
}

func (a *app) generateOverview() {
	if a.rev == nil || len(a.files) == 0 {
		return
	}
	rev := a.rev
	cfg := a.cfg.Agent.Auto()
	changes := review.ChangeSet{Diffstat: a.diffstatOf(rev), Files: a.fileNames(), PatchID: a.fp.Patch, FileIDs: a.fp.Files}
	ex := review.OverviewExchange(rev, changes, cfg, a.cfg.Questions)
	a.runner.start(ex, "Overview", "", func(answered bool) {
		if rev == a.rev {
			a.leaveGone()
		}
		if answered {
			a.deeperAhead(rev)
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
func (a *app) regenerate(parent string) {
	shown, repo, scope := a.rev, a.rev.Repo, a.rev.Scope
	run := func() {
		rev := a.rev
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
		a.runner.stop(rev, steps...)
		rev.DeleteStepWalks(parent)
		a.closeGoneQuestion()
		a.leaveGone()
		if parent == "" {
			a.generateOverview()
		} else if w := rev.Walk(parent); w != nil {
			a.runWalk(rev, w)
		}
	}
	if !a.rev.HasStepWalks(parent) {
		run()
		return
	}
	a.confirm("Regenerate this walkthrough?", "The new walkthrough has new steps, so the deeper walkthroughs into the current steps are deleted. Questions about the page as a whole stay.", "Regenerate", run)
}

// deeper starts a walkthrough one level below parent: into one of its
// steps, or about the question for step -1.
func (a *app) deeper(parent string, step int, question string) {
	w := a.rev.AddWalk(parent, step, question)
	a.nav.goTo(page{kindWalk, w.ID})
	a.runWalk(a.rev, w)
}

// askQuestion starts a question's conversation about a step of parent, or about
// parent as a whole for step -1, in the side panel.
func (a *app) askQuestion(parent string, step int, question string) {
	question = strings.TrimSpace(question)
	if question == "" {
		return
	}
	w := a.rev.AddWalk(parent, step, question)
	a.openQuestion(w.ID)
	a.runWalk(a.rev, w)
}

// deeperAhead queues a deeper walkthrough of every step of the overview,
// so that Go deeper opens one that is already written.
func (a *app) deeperAhead(rev *review.Review) {
	if !a.cfg.DeeperAhead || rev.Overview == nil || rev.Overview.Walk == nil {
		return
	}
	var walks []*review.Walk
	for i := range rev.Overview.Walk.Steps {
		if !slices.ContainsFunc(rev.WalksOn("", i), func(w *review.Walk) bool { return w.Question == "" }) {
			walks = append(walks, rev.AddWalk("", i, ""))
		}
	}
	a.saveReviewOf(rev)
	for _, w := range walks {
		a.runner.ahead(rev, w.ID, func() (*review.Exchange, string, bool) {
			if rev.Walk(w.ID) == nil {
				return nil, "", false
			}
			ex, title := a.walkExchange(rev, w, true)
			return ex, title, true
		})
	}
}

// runWalk asks the agent for a walk's walkthrough. Walks next to each
// other stay apart, because each forks the session of the walkthrough
// above.
func (a *app) runWalk(rev *review.Review, w *review.Walk) {
	ex, title := a.walkExchange(rev, w, false)
	a.runner.start(ex, title, "", nil)
}

// walkExchange is the exchange for a walk's walkthrough and its title in
// the run log. An auto walk is one the app writes ahead of the reviewer.
func (a *app) walkExchange(rev *review.Review, w *review.Walk, auto bool) (*review.Exchange, string) {
	cfg := a.cfg.Agent
	if auto {
		cfg = cfg.Auto()
	}
	title := "Deeper: " + rev.WalkTitle(w)
	if w.Question != "" {
		title = "Question: " + w.Question
	}
	return review.DeeperExchange(rev, w, cfg, a.diffstatOf(rev)), title
}

// askMore asks a follow-up question in a question's conversation.
func (a *app) askMore(rev *review.Review, w *review.Walk, q string) {
	q = strings.TrimSpace(q)
	if q == "" || a.runner.busy(rev, w.ID) {
		return
	}
	cfg := a.cfg.Agent
	ex := review.FollowupExchange(rev, w, cfg, a.diffstatOf(rev), q)
	a.runner.start(ex, "Follow-up: "+q, "", nil)
}

// diffstatOf is the diffstat of rev's changes. The app only reads it for
// the review on screen, so it is empty for any other review.
func (a *app) diffstatOf(rev *review.Review) string {
	if rev != a.rev {
		return ""
	}
	return a.diffstat
}

// deleteWalk deletes a walk and the walks under it, and goes to its
// parent.
func (a *app) deleteWalk(w *review.Walk) {
	walks := slices.Clone(a.rev.Walks)
	a.rev.DeleteWalk(w.ID)
	var gone []string
	for _, x := range walks {
		if a.rev.Walk(x.ID) == nil {
			gone = append(gone, x.ID)
		}
	}
	a.runner.stop(a.rev, gone...)
	a.closeGoneQuestion()
	if w.Parent != "" {
		a.nav.replace(page{kindWalk, w.Parent})
	}
	a.leaveGone()
	a.saveReview()
}

// comment adds the reviewer's words to a thread without asking anyone.
func (a *app) comment(t *review.Thread, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	t.Messages = append(t.Messages, review.Message{Role: review.RoleUser, Text: text, Time: time.Now()})
	a.saveReview()
}

// ask sends a thread's question to the agent, in the repository and the
// scope of rev.
func (a *app) ask(rev *review.Review, t *review.Thread, text string) {
	text = strings.TrimSpace(text)
	if text == "" || a.runner.busy(rev, t.ID) {
		return
	}
	cfg := a.cfg.Agent
	ex := review.ThreadExchange(rev, t, cfg, a.diffstatOf(rev), text)
	a.runner.start(ex, t.Anchor.Label(), excerpt(text, 160), nil)
}

// release forgets rev: it stops every run of rev and the walks queued
// for it without writing into rev, and drops its run log from memory.
func (a *app) release(rev *review.Review) {
	a.runner.release(rev)
	delete(a.logs, rev)
	a.targets.forget(rev)
}

// state is the state of target of the review on screen.
func (a *app) state(target string) targetState { return a.targets.state(a.rev, target) }

// busy reports whether a run writes into the review on screen for
// target, or waits in the queue to.
func (a *app) busy(target string) bool { return a.state(target).busy() }
