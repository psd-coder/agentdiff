package app

import (
	"context"
	"log"
	"slices"
	"strings"
	"sync"
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
	run := a.startRun(rev, info)
	file := &runFile{}
	var mu sync.Mutex
	var logged []logItem
	req.Log = func(e agent.LogEntry) {
		file.write(e)
		items := logItems(cfg.Provider, e)
		mu.Lock()
		logged = append(logged, items...)
		mu.Unlock()
	}
	events := a.startAgent(ctx, cfg, req)
	repo, scope := rev.Repo, rev.Scope
	go func() {
		w, err := a.store.CreateRun(repo, scope, info)
		file.opened(w)
		a.update(func() {
			if err != nil {
				run.err = err.Error()
			}
			// A Start Over or an archive that came while the file was
			// created leaves it in the folder of the review that took the
			// place of rev.
			if w != nil && a.reviews.Obsolete(rev) {
				a.async(func() {
					file.close()
					if err := a.store.RemoveRun(repo, scope, info.ID); err != nil && !store.IsNotExist(err) {
						log.Printf("run log: %v", err)
					}
				})
			}
		})
		var pending strings.Builder
		last := time.Now()
		flush := func() {
			mu.Lock()
			items := logged
			logged = nil
			mu.Unlock()
			if pending.Len() == 0 && len(items) == 0 {
				return
			}
			text := pending.String()
			pending.Reset()
			last = time.Now()
			a.update(func() {
				run.info.Usage = run.add(items)
				if text != "" {
					fn(agent.Event{Kind: agent.EventText, Text: text})
				}
			})
		}
		for ev := range events {
			if ev.Kind == agent.EventText {
				pending.WriteString(ev.Text)
				if time.Since(last) >= streamFlush {
					flush()
				}
				continue
			}
			flush()
			a.update(func() { fn(ev) })
		}
		flush()
		file.close()
		a.update(func() {
			run.running = false
			if u := run.info.Usage; u != nil && !a.reviews.Obsolete(rev) {
				u.CostUSD = runCost(a.logsOf(rev), run)
				if err := a.store.SaveRunUsage(rev.Repo, rev.Scope, info.ID, *u); err != nil {
					log.Printf("run usage: %v", err)
				}
			}
		})
	}()
}

func (a *app) generateOverview() {
	if a.rev == nil || len(a.files) == 0 {
		return
	}
	rev := a.rev
	cfg := a.cfg.Agent.Auto()
	changes := review.ChangeSet{Diffstat: a.diffstatOf(rev), Files: a.fileNames(), PatchID: a.fp.Patch, FileIDs: a.fp.Files}
	ex := review.OverviewExchange(rev, changes, cfg, a.cfg.Questions)
	a.launch(&agentRun{kind: overviewKind, ex: ex, ended: func(answered bool) {
		if rev == a.rev {
			a.leaveGoneGroup()
		}
		if answered {
			a.deeperAhead(rev)
		}
	}}, newRunInfo(store.TargetOverview, "Overview", "", cfg))
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
		a.stopRuns(rev, steps)
		rev.DeleteStepWalks(parent)
		a.closeGoneQuestion()
		if parent == "" {
			a.generateOverview()
		} else if w := rev.Walk(parent); w != nil {
			a.runWalk(rev, w, false)
		}
	}
	if !a.rev.HasStepWalks(parent) {
		run()
		return
	}
	a.confirm("Regenerate this walkthrough?", "The new walkthrough has new steps, so the deeper walkthroughs into the current steps are deleted. Questions about the page as a whole stay.", "Regenerate", run)
}

func (a *app) stopOverview() { a.stopRun(a.rev, store.TargetOverview) }

// deeper starts a walkthrough one level below parent: into one of its
// steps, or about the question for step -1.
func (a *app) deeper(parent string, step int, question string) {
	w := a.rev.AddWalk(parent, step, question)
	a.goTo("walk:" + w.ID)
	a.runWalk(a.rev, w, false)
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
	a.runWalk(a.rev, w, false)
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
		a.runAhead(rev, w.ID, func() {
			if rev.Walk(w.ID) != nil {
				a.runWalk(rev, w, true)
			}
		})
	}
}

// runWalk asks the agent for a walk's walkthrough. Walks next to each
// other stay apart, because each forks the session of the walkthrough
// above. An auto walk is one the app writes ahead of the reviewer.
func (a *app) runWalk(rev *review.Review, w *review.Walk, auto bool) {
	cfg := a.cfg.Agent
	if auto {
		cfg = cfg.Auto()
	}
	ex := review.DeeperExchange(rev, w, cfg, a.diffstatOf(rev))
	title := "Deeper: " + rev.WalkTitle(w)
	if w.Question != "" {
		title = "Question: " + w.Question
	}
	a.launch(&agentRun{kind: walkKind, ahead: auto, ex: ex}, newRunInfo(w.ID, title, "", cfg))
}

// askMore asks a follow-up question in a question's conversation.
func (a *app) askMore(rev *review.Review, w *review.Walk, q string) {
	q = strings.TrimSpace(q)
	if q == "" || a.runner.busy(rev, w.ID) {
		return
	}
	cfg := a.cfg.Agent
	ex := review.FollowupExchange(rev, w, cfg, a.diffstatOf(rev), q)
	a.launch(&agentRun{kind: walkKind, ex: ex}, newRunInfo(w.ID, "Follow-up: "+q, "", cfg))
}

// diffstatOf is the diffstat of rev's changes. The app only reads it for
// the review on screen, so it is empty for any other review.
func (a *app) diffstatOf(rev *review.Review) string {
	if rev != a.rev {
		return ""
	}
	return a.diffstat
}

func (a *app) stopWalk(id string) { a.stopRuns(a.rev, []string{id}) }

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
	a.stopRuns(a.rev, gone)
	a.closeGoneQuestion()
	a.page = "overview"
	if w.Parent != "" {
		a.page = "walk:" + w.Parent
	}
	if n := len(a.history); n > 0 && a.history[n-1] == a.page {
		a.history = a.history[:n-1]
	}
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
	a.launch(&agentRun{kind: threadKind, ex: ex}, newRunInfo(t.ID, t.Anchor.Label(), excerpt(text, 160), cfg))
}

func (a *app) stopThread(t *review.Thread) { a.stopRun(a.rev, t.ID) }

func providerName(p agent.Provider) string {
	if p == agent.Codex {
		return "Codex"
	}
	return "Claude Code"
}
