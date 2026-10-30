package app

import (
	"cmp"
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
				w = nil
			}
		}
		items := logItems(cfg.Provider, e)
		mu.Lock()
		logged = append(logged, items...)
		mu.Unlock()
	}
	events := agent.Run(ctx, cfg, req)
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
				run.items = append(run.items, items...)
				run.addUsage(items)
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
			if u := run.info.Usage; u != nil {
				u.CostUSD = a.runCost(rev, run, req.ResumeID)
				if err := a.store.SaveRunUsage(rev.Repo, rev.Scope, info.ID, *u); err != nil {
					log.Printf("run usage: %v", err)
				}
			}
		})
	}()
}

// runCost is what a run spent itself. Claude reports the cost of the
// whole session, so a run that resumed a session costs the difference to
// the run it resumed. The cost stays unknown, 0, when that run is not in
// the log anymore.
func (a *app) runCost(rev *review.Review, run *logRun, resumed string) float64 {
	u := run.info.Usage
	if u.SessionCost == 0 || resumed == "" {
		return cmp.Or(u.SessionCost, u.CostUSD)
	}
	for _, r := range a.runsOf(rev) {
		if r != run && r.info.Usage != nil && r.info.Usage.Session == resumed {
			return max(0, u.SessionCost-r.info.Usage.SessionCost)
		}
	}
	return 0
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
	files := a.fileNames()
	patch, fileIDs := a.patch, a.fileIDs
	prompt := review.OverviewPrompt(a.scope.Describe(), a.diffstat, a.scope.NewRev(), files, questions)
	session := ""
	req := agent.Request{Dir: a.repo.Root, Prompt: prompt, SystemPrompt: review.SystemPrompt}
	info := newRunInfo(store.TargetOverview, "Overview", "", cfg)
	a.stream(ctx, cfg, req, rev, info, func(ev agent.Event) {
		if a.overviews[rev] != run {
			return
		}
		switch ev.Kind {
		case agent.EventSession:
			session = ev.SessionID
		case agent.EventText:
			run.text += ev.Text
			run.activity = ""
		case agent.EventActivity:
			run.activity = ev.Text
		case agent.EventDone:
			ov := review.ReadOverview(answer(ev.Text, run.text), files)
			ov.SessionID, ov.Agent, ov.Model, ov.Generated, ov.PatchID, ov.Files = session, cfg.Provider, cfg.Model, time.Now(), patch, fileIDs
			rev.Overview = &ov
			delete(a.overviews, rev)
			a.saveReviewOf(rev)
			a.deeperAhead(rev)
		case agent.EventError:
			delete(a.overviews, rev)
			if ctx.Err() != nil {
				return
			}
			ov := &review.Overview{Agent: cfg.Provider, Model: cfg.Model, Generated: time.Now(), Err: ev.Err.Error()}
			if old := rev.Overview; old.Done() {
				ov.Walk, ov.Markdown, ov.Groups, ov.SessionID, ov.PatchID, ov.Files = old.Walk, old.Markdown, old.Groups, old.SessionID, old.PatchID, old.Files
			}
			rev.Overview = ov
			a.saveReviewOf(rev)
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

// runWalk asks the agent for a walk's walkthrough. It forks the session
// of the walkthrough above, so the agent knows what the reviewer read on
// the way down, and walks next to each other stay apart. An auto walk is
// one the app writes ahead of the reviewer.
func (a *app) runWalk(rev *review.Review, w *review.Walk, auto bool) {
	a.unqueue(w.ID)
	if run := a.walkRuns[w.ID]; run != nil {
		run.cancel()
	}
	diffstat := ""
	if rev == a.rev {
		diffstat = a.diffstat
	}
	cfg := a.cfg.Agent
	if auto {
		cfg = cfg.Auto()
	}
	parentSession, parentAgent := "", agent.Provider("")
	if p := rev.Walk(w.Parent); p != nil {
		parentSession, parentAgent = p.SessionID, p.Agent
	} else if ov := rev.Overview; w.Parent == "" && ov != nil {
		parentSession, parentAgent = ov.SessionID, ov.Agent
	}
	d := review.DeeperRequest{
		Question: w.Question,
		Known:    parentSession != "" && parentAgent == cfg.Provider,
		NewRev:   rev.Scope.NewRev(),
	}
	if s, ok := rev.StepOf(w); ok {
		d.Step = &s
	}
	req := agent.Request{Dir: rev.Repo, SystemPrompt: review.SystemPrompt}
	if d.Known {
		req.ResumeID, req.Fork = parentSession, true
	} else {
		d.Context = review.ContextPrompt(rev.Scope.Describe(), diffstat)
		path := rev.WalkPath(w.ID)
		for _, p := range path[:len(path)-1] {
			d.Path = append(d.Path, rev.WalkTitle(p))
		}
	}
	req.Prompt = review.DeeperPrompt(d)

	w.Content, w.Markdown, w.Err, w.SessionID, w.More = nil, "", "", "", nil
	title := "Deeper: " + rev.WalkTitle(w)
	if w.Question != "" {
		title = "Question: " + w.Question
	}
	a.streamWalk(rev, w, cfg, req, title)
}

// askMore asks a follow-up question in a question's conversation. It goes
// on in the conversation's own session.
func (a *app) askMore(rev *review.Review, w *review.Walk, q string) {
	q = strings.TrimSpace(q)
	if q == "" || a.walkBusy(w.ID) {
		return
	}
	cfg := a.cfg.Agent
	f := review.FollowupRequest{Question: q, Known: w.SessionID != "" && w.Agent == cfg.Provider, NewRev: rev.Scope.NewRev()}
	req := agent.Request{Dir: rev.Repo, SystemPrompt: review.SystemPrompt}
	if f.Known {
		req.ResumeID = w.SessionID
	} else {
		diffstat := ""
		if rev == a.rev {
			diffstat = a.diffstat
		}
		f.Context = review.ContextPrompt(rev.Scope.Describe(), diffstat)
		f.Earlier = w.Turns()
	}
	req.Prompt = review.FollowupPrompt(f)
	w.More = append(w.More, review.Turn{Question: q, Generated: time.Now()})
	a.streamWalk(rev, w, cfg, req, "Follow-up: "+q)
}

// streamWalk runs the agent for the latest turn of a walk.
func (a *app) streamWalk(rev *review.Review, w *review.Walk, cfg agent.Config, req agent.Request, title string) {
	w.Agent, w.Model = cfg.Provider, cfg.Model
	ctx, cancel := context.WithCancel(context.Background())
	run := &walkRun{cancel: cancel, activity: "Starting " + providerName(cfg.Provider) + "…"}
	a.walkRuns[w.ID] = run
	a.saveReviewOf(rev)
	info := newRunInfo(w.ID, title, "", cfg)
	a.stream(ctx, cfg, req, rev, info, func(ev agent.Event) {
		if a.walkRuns[w.ID] != run {
			return
		}
		switch ev.Kind {
		case agent.EventSession:
			w.SessionID = ev.SessionID
		case agent.EventText:
			run.text += ev.Text
			run.activity = "Writing the walkthrough…"
		case agent.EventActivity:
			run.activity = ev.Text
		case agent.EventDone:
			text := answer(ev.Text, run.text)
			if wt, ok := review.ParseWalkthrough(text); ok {
				w.SetAnswer(wt, "", "", time.Now())
			} else {
				w.SetAnswer(nil, text, "", time.Now())
			}
			delete(a.walkRuns, w.ID)
			a.saveReviewOf(rev)
			a.startAhead()
		case agent.EventError:
			delete(a.walkRuns, w.ID)
			msg := ev.Err.Error()
			if ctx.Err() != nil {
				msg = "Stopped"
			}
			w.SetAnswer(nil, "", msg, time.Now())
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
	a.rev.DeleteWalk(w.ID)
	for id, run := range a.walkRuns {
		if a.rev.Walk(id) == nil {
			run.cancel()
		}
	}
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

// ask sends a thread's question to the agent. A thread's first question
// forks the overview's session, so every thread starts from what the
// agent learned writing the overview, and threads stay apart.
func (a *app) ask(t *review.Thread, text string) {
	text = strings.TrimSpace(text)
	if text == "" || t.Busy() {
		return
	}
	rev := a.rev
	cfg := a.cfg.Agent
	t.Messages = append(t.Messages, review.Message{Role: review.RoleUser, Text: text, Time: time.Now()})
	req := agent.Request{Dir: a.repo.Root, SystemPrompt: review.SystemPrompt}
	known := t.SessionID != "" && t.Agent == cfg.Provider
	prompt := review.QuestionPrompt(t, text, known)
	switch {
	case known:
		req.ResumeID = t.SessionID
	case rev.Overview != nil && rev.Overview.SessionID != "" && rev.Overview.Agent == cfg.Provider:
		req.ResumeID, req.Fork = rev.Overview.SessionID, true
	default:
		prompt = review.ContextPrompt(a.scope.Describe(), a.diffstat) + prompt
	}
	req.Prompt = prompt
	t.Agent = cfg.Provider
	t.Messages = append(t.Messages, review.Message{Role: review.RoleAgent, Time: time.Now(), Pending: true, Activity: "Thinking…"})
	reply := len(t.Messages) - 1
	ctx, cancel := context.WithCancel(context.Background())
	a.threadRun[t.ID] = cancel
	a.saveReview()
	info := newRunInfo(t.ID, t.Anchor.Label(), excerpt(text, 160), cfg)
	a.stream(ctx, cfg, req, rev, info, func(ev agent.Event) {
		if reply >= len(t.Messages) {
			return
		}
		m := &t.Messages[reply]
		switch ev.Kind {
		case agent.EventSession:
			t.SessionID = ev.SessionID
		case agent.EventText:
			m.Text += ev.Text
			m.Activity = ""
		case agent.EventActivity:
			m.Activity = ev.Text
		case agent.EventDone:
			if strings.TrimSpace(ev.Text) != "" {
				m.Text = ev.Text
			}
			m.Pending, m.Activity = false, ""
			delete(a.threadRun, t.ID)
			a.saveReviewOf(rev)
		case agent.EventError:
			m.Pending, m.Activity = false, ""
			m.Err = ev.Err.Error()
			if ctx.Err() != nil {
				m.Err = "Stopped"
			}
			delete(a.threadRun, t.ID)
			a.saveReviewOf(rev)
		}
	})
}

func (a *app) stopThread(t *review.Thread) {
	if cancel := a.threadRun[t.ID]; cancel != nil {
		cancel()
	}
}

func providerName(p agent.Provider) string {
	if p == agent.Codex {
		return "Codex"
	}
	return "Claude Code"
}
