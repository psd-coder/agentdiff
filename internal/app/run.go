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
	run, w := a.startRun(rev, info)
	var mu sync.Mutex
	var logged []logItem
	req.Log = func(e agent.LogEntry) {
		if w != nil {
			if err := w.Write(e); err != nil {
				log.Printf("run log: %v", err)
				if err := w.Close(); err != nil {
					log.Printf("run log: %v", err)
				}
				w = nil
			}
		}
		items := logItems(cfg.Provider, e)
		mu.Lock()
		logged = append(logged, items...)
		mu.Unlock()
	}
	events := a.startAgent(ctx, cfg, req)
	go func() {
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
		if w != nil {
			if err := w.Close(); err != nil {
				log.Printf("run log: %v", err)
			}
		}
		a.update(func() {
			run.running = false
			if u := run.info.Usage; u != nil && !a.obsolete(rev) {
				u.CostUSD = runCost(a.runsOf(rev), run)
				if err := a.store.SaveRunUsage(rev.Repo, rev.Scope, info.ID, *u); err != nil {
					log.Printf("run usage: %v", err)
				}
			}
		})
	}()
}

func (a *app) overviewRunning() *overviewRun { return a.overviews[a.rev] }

func (a *app) generateOverview() {
	if a.rev == nil || len(a.files) == 0 {
		return
	}
	rev := a.rev
	if run := a.overviews[rev]; run != nil {
		run.cancel()
	}
	cfg, questions := a.cfg.Agent, a.cfg.Questions
	cfg = cfg.Auto()
	ctx, cancel := context.WithCancel(context.Background())
	run := &overviewRun{cancel: cancel, activity: "Starting " + providerName(cfg.Provider) + "…"}
	a.overviews[rev] = run
	changes := review.ChangeSet{Diffstat: a.diffstatOf(rev), Files: a.fileNames(), PatchID: a.patch, FileIDs: a.fileIDs}
	ex := review.OverviewExchange(rev, changes, cfg, questions)
	info := newRunInfo(store.TargetOverview, "Overview", "", cfg)
	a.stream(ctx, cfg, ex.Request, rev, info, func(ev agent.Event) {
		if a.overviews[rev] != run {
			return
		}
		switch ev.Kind {
		case agent.EventSession:
			ex.Started(ev.SessionID)
		case agent.EventText:
			run.text += ev.Text
			run.activity = ""
		case agent.EventActivity:
			run.activity = ev.Text
		case agent.EventDone:
			ex.Answered(answer(ev.Text, run.text), time.Now())
			cancel()
			delete(a.overviews, rev)
			a.saveReviewOf(rev)
			if rev == a.rev {
				a.leaveGoneGroup()
			}
			a.deeperAhead(rev)
		case agent.EventError:
			if ctx.Err() != nil {
				ex.Stopped(time.Now())
			} else {
				ex.Failed(ev.Err.Error(), time.Now())
			}
			cancel()
			delete(a.overviews, rev)
			a.saveReviewOf(rev)
			if rev == a.rev {
				a.leaveGoneGroup()
			}
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
// user confirms that first.
func (a *app) regenerate(parent string) {
	rev := a.rev
	run := func() {
		for _, w := range rev.Walks {
			if w.Parent == parent && w.Step >= 0 {
				a.stopWalk(w.ID)
			}
		}
		rev.DeleteStepWalks(parent)
		if rev == a.rev {
			a.closeGoneQuestion()
		}
		if parent == "" {
			a.generateOverview()
		} else if w := rev.Walk(parent); w != nil {
			a.runWalk(rev, w, false)
		}
	}
	if !rev.HasStepWalks(parent) {
		run()
		return
	}
	a.confirm("Regenerate this walkthrough?", "The new walkthrough has new steps, so the deeper walkthroughs into the current steps are deleted. Questions about the page as a whole stay.", "Regenerate", run)
}

func (a *app) stopOverview() {
	if run := a.overviews[a.rev]; run != nil {
		run.cancel()
		delete(a.overviews, a.rev)
	}
}

// walkRun is a walkthrough being written for a walk.
type walkRun struct {
	text     string
	activity string
	cancel   context.CancelFunc
}

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

// maxAhead is how many walkthroughs the app writes at once ahead of the
// reviewer.
const maxAhead = 3

type queuedWalk struct {
	rev *review.Review
	w   *review.Walk
}

// deeperAhead queues a deeper walkthrough of every step of the overview,
// so that Go deeper opens one that is already written.
func (a *app) deeperAhead(rev *review.Review) {
	if !a.cfg.DeeperAhead || rev.Overview == nil || rev.Overview.Walk == nil {
		return
	}
	for i := range rev.Overview.Walk.Steps {
		if !slices.ContainsFunc(rev.WalksOn("", i), func(w *review.Walk) bool { return w.Question == "" }) {
			a.ahead = append(a.ahead, queuedWalk{rev, rev.AddWalk("", i, "")})
		}
	}
	a.saveReviewOf(rev)
	a.startAhead()
}

func (a *app) startAhead() {
	for len(a.ahead) > 0 && len(a.walkRuns) < maxAhead {
		q := a.ahead[0]
		a.ahead = a.ahead[1:]
		if q.rev.Walk(q.w.ID) != nil {
			a.runWalk(q.rev, q.w, true)
		}
	}
}

func (a *app) queued(id string) bool {
	return slices.ContainsFunc(a.ahead, func(q queuedWalk) bool { return q.w.ID == id })
}

func (a *app) unqueue(id string) {
	a.ahead = slices.DeleteFunc(a.ahead, func(q queuedWalk) bool { return q.w.ID == id })
}

// walkBusy reports whether a walk is being written or waits for its turn.
func (a *app) walkBusy(id string) bool { return a.walkRuns[id] != nil || a.queued(id) }

// runWalk asks the agent for a walk's walkthrough. Walks next to each
// other stay apart, because each forks the session of the walkthrough
// above. An auto walk is one the app writes ahead of the reviewer.
func (a *app) runWalk(rev *review.Review, w *review.Walk, auto bool) {
	a.unqueue(w.ID)
	if run := a.walkRuns[w.ID]; run != nil {
		run.cancel()
	}
	cfg := a.cfg.Agent
	if auto {
		cfg = cfg.Auto()
	}
	ex := review.DeeperExchange(rev, w, cfg, a.diffstatOf(rev))
	title := "Deeper: " + rev.WalkTitle(w)
	if w.Question != "" {
		title = "Question: " + w.Question
	}
	a.streamWalk(rev, w, cfg, ex, title)
}

// askMore asks a follow-up question in a question's conversation.
func (a *app) askMore(rev *review.Review, w *review.Walk, q string) {
	q = strings.TrimSpace(q)
	if q == "" || a.walkBusy(w.ID) {
		return
	}
	cfg := a.cfg.Agent
	ex := review.FollowupExchange(rev, w, cfg, a.diffstatOf(rev), q)
	a.streamWalk(rev, w, cfg, ex, "Follow-up: "+q)
}

// diffstatOf is the diffstat of rev's changes. The app only reads it for
// the review on screen, so it is empty for any other review.
func (a *app) diffstatOf(rev *review.Review) string {
	if rev != a.rev {
		return ""
	}
	return a.diffstat
}

// streamWalk runs the agent for the latest turn of a walk.
func (a *app) streamWalk(rev *review.Review, w *review.Walk, cfg agent.Config, ex *review.Exchange, title string) {
	ctx, cancel := context.WithCancel(context.Background())
	run := &walkRun{cancel: cancel, activity: "Starting " + providerName(cfg.Provider) + "…"}
	a.walkRuns[w.ID] = run
	a.saveReviewOf(rev)
	info := newRunInfo(w.ID, title, "", cfg)
	a.stream(ctx, cfg, ex.Request, rev, info, func(ev agent.Event) {
		if a.walkRuns[w.ID] != run {
			return
		}
		switch ev.Kind {
		case agent.EventSession:
			ex.Started(ev.SessionID)
		case agent.EventText:
			run.text += ev.Text
			run.activity = "Writing the walkthrough…"
		case agent.EventActivity:
			run.activity = ev.Text
		case agent.EventDone:
			ex.Answered(answer(ev.Text, run.text), time.Now())
			cancel()
			delete(a.walkRuns, w.ID)
			a.saveReviewOf(rev)
			a.startAhead()
		case agent.EventError:
			if ctx.Err() != nil {
				ex.Stopped(time.Now())
			} else {
				ex.Failed(ev.Err.Error(), time.Now())
			}
			cancel()
			delete(a.walkRuns, w.ID)
			a.saveReviewOf(rev)
			a.startAhead()
		}
	})
}

func (a *app) stopWalk(id string) {
	a.unqueue(id)
	if run := a.walkRuns[id]; run != nil {
		run.cancel()
	}
}

// deleteWalk deletes a walk and the walks under it, and goes to its
// parent.
func (a *app) deleteWalk(w *review.Walk) {
	walks := slices.Clone(a.rev.Walks)
	a.rev.DeleteWalk(w.ID)
	for _, x := range walks {
		if a.rev.Walk(x.ID) == nil {
			a.stopWalk(x.ID)
		}
	}
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

// threadRun is an answer being written in a thread. reply is where the
// answer goes in the thread's messages.
type threadRun struct {
	text     string
	activity string
	started  time.Time
	reply    int
	cancel   context.CancelFunc
}

// ask sends a thread's question to the agent, in the repository and the
// scope of rev.
func (a *app) ask(rev *review.Review, t *review.Thread, text string) {
	text = strings.TrimSpace(text)
	if text == "" || a.threadBusy(t.ID) {
		return
	}
	cfg := a.cfg.Agent
	ex := review.ThreadExchange(rev, t, cfg, a.diffstatOf(rev), text)
	ctx, cancel := context.WithCancel(context.Background())
	run := &threadRun{activity: "Thinking…", started: time.Now(), reply: len(t.Messages), cancel: cancel}
	a.threadRuns[t.ID] = run
	a.saveReviewOf(rev)
	info := newRunInfo(t.ID, t.Anchor.Label(), excerpt(text, 160), cfg)
	a.stream(ctx, cfg, ex.Request, rev, info, func(ev agent.Event) {
		if a.threadRuns[t.ID] != run {
			return
		}
		switch ev.Kind {
		case agent.EventSession:
			ex.Started(ev.SessionID)
		case agent.EventText:
			run.text += ev.Text
			run.activity = ""
		case agent.EventActivity:
			run.activity = ev.Text
		case agent.EventDone:
			ex.Answered(answer(ev.Text, run.text), time.Now())
			cancel()
			delete(a.threadRuns, t.ID)
			a.saveReviewOf(rev)
		case agent.EventError:
			if ctx.Err() != nil {
				ex.Stopped(time.Now())
			} else {
				ex.Failed(ev.Err.Error(), time.Now())
			}
			cancel()
			delete(a.threadRuns, t.ID)
			a.saveReviewOf(rev)
		}
	})
}

// threadBusy reports whether the agent answers in the thread.
func (a *app) threadBusy(id string) bool { return a.threadRuns[id] != nil }

func (a *app) stopThread(t *review.Thread) {
	if run := a.threadRuns[t.ID]; run != nil {
		run.cancel()
	}
}

func providerName(p agent.Provider) string {
	if p == agent.Codex {
		return "Codex"
	}
	return "Claude Code"
}
