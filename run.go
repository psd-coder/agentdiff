package main

import (
	"context"
	"strings"
	"time"

	"github.com/psdcoder/agentdiff/internal/agent"
	"github.com/psdcoder/agentdiff/internal/review"
)

// streamFlush is how often streamed text reaches the view: tokens come
// far faster than frames are worth drawing.
const streamFlush = 40 * time.Millisecond

// stream runs the agent and hands its events to fn on the main thread,
// text deltas joined into one event per streamFlush.
func (a *app) stream(ctx context.Context, cfg agent.Config, req agent.Request, fn func(agent.Event)) {
	events := agent.Run(ctx, cfg, req)
	go func() {
		var pending strings.Builder
		last := time.Now()
		flush := func() {
			if pending.Len() == 0 {
				return
			}
			ev := agent.Event{Kind: agent.EventText, Text: pending.String()}
			pending.Reset()
			last = time.Now()
			a.update(func() { fn(ev) })
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
	cfg, questions := a.cfg.Effective(rev.Settings)
	ctx, cancel := context.WithCancel(context.Background())
	run := &overviewRun{cancel: cancel, activity: "Starting " + providerName(cfg.Provider) + "…"}
	a.overviews[rev] = run
	files := a.fileNames()
	prompt := review.OverviewPrompt(a.scope.Describe(), a.diffstat, files, questions)
	session := ""
	a.stream(ctx, cfg, agent.Request{Dir: a.repo.Root, Prompt: prompt, SystemPrompt: review.SystemPrompt}, func(ev agent.Event) {
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
			text := ev.Text
			if strings.TrimSpace(text) == "" {
				text = run.text
			}
			body, groups := review.ParseOverview(text, files)
			rev.Overview = &review.Overview{
				Markdown: body, Groups: groups, SessionID: session,
				Agent: cfg.Provider, Model: cfg.Model, Generated: time.Now(),
			}
			delete(a.overviews, rev)
			a.saveReviewOf(rev)
		case agent.EventError:
			delete(a.overviews, rev)
			if ctx.Err() != nil {
				return
			}
			ov := &review.Overview{Agent: cfg.Provider, Model: cfg.Model, Generated: time.Now(), Err: ev.Err.Error()}
			if rev.Overview != nil && rev.Overview.Markdown != "" {
				ov.Markdown, ov.Groups, ov.SessionID = rev.Overview.Markdown, rev.Overview.Groups, rev.Overview.SessionID
			}
			rev.Overview = ov
			a.saveReviewOf(rev)
		}
	})
}

func (a *app) stopOverview() {
	if run := a.overviews[a.rev]; run != nil {
		run.cancel()
		delete(a.overviews, a.rev)
	}
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
	cfg, _ := a.cfg.Effective(rev.Settings)
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
	a.stream(ctx, cfg, req, func(ev agent.Event) {
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
