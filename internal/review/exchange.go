package review

import (
	"maps"
	"slices"
	"time"

	"github.com/psd-coder/agentdiff/internal/agent"
)

// ChangeSet is what the app read from git about the changes under review.
type ChangeSet struct {
	Diffstat string
	Files    []string
	// PatchID and FileIDs identify the changes, see gitx.Fingerprint.
	PatchID string
	FileIDs map[string]string
}

// Exchange is one run of the agent for the overview, a walk or a thread
// of a review. It holds the request to send, and it writes what comes back
// into the review.
type Exchange struct {
	Request agent.Request
	rev     *Review
	cfg     agent.Config
	changes ChangeSet
	session string
	// walk is the walk the run writes, nil for the overview.
	walk *Walk
	// thread is the thread the run answers in, and reply is where the
	// answer goes in its messages. Comments added during the run come
	// after the answer.
	thread *Thread
	reply  int
}

// OverviewExchange starts the exchange for a new overview of r. The
// overview always starts a new session, so it reads the changes as they
// are now.
func OverviewExchange(r *Review, changes ChangeSet, cfg agent.Config, questions string) *Exchange {
	prompt := overviewPrompt(r.Scope.Describe(), changes.Diffstat, r.Scope.NewRev(), changes.Files, questions)
	return &Exchange{
		Request: agent.Request{Dir: r.Repo, Prompt: prompt, SystemPrompt: systemPrompt},
		rev:     r,
		cfg:     cfg,
		changes: changes,
	}
}

// DeeperExchange starts the exchange for the walkthrough of w, one level
// below its parent. It forks the parent's session, so the agent knows what
// the reviewer read on the way down. A parent without a session for the
// provider gets a new session that is told the change set and the path.
// The walk loses its old answers and its sessions, which hold the
// conversation about those answers. diffstat is empty when the app does
// not know it.
func DeeperExchange(r *Review, w *Walk, cfg agent.Config, diffstat string) *Exchange {
	parentSession := ""
	if p := r.Walk(w.Parent); p != nil {
		parentSession = p.Sessions[cfg.Provider]
	} else if ov := r.Overview; w.Parent == "" && ov != nil {
		parentSession = ov.Sessions[cfg.Provider]
	}
	d := deeperRequest{Question: w.Question, Known: parentSession != "", NewRev: r.Scope.NewRev()}
	if s, ok := r.StepOf(w); ok {
		d.Step = &s
	}
	req := agent.Request{Dir: r.Repo, SystemPrompt: systemPrompt}
	if d.Known {
		req.ResumeID, req.Fork = parentSession, true
	} else {
		d.Context = contextPrompt(r.Scope.Describe(), diffstat)
		path := r.WalkPath(w.ID)
		for _, p := range path[:len(path)-1] {
			d.Path = append(d.Path, r.WalkTitle(p))
		}
	}
	req.Prompt = deeperPrompt(d)
	w.Content, w.Markdown, w.Err, w.More, w.Agent, w.Model, w.Sessions = nil, "", "", nil, "", "", nil
	return &Exchange{Request: req, rev: r, cfg: cfg, walk: w}
}

// FollowupExchange starts the exchange for a follow-up question in the
// conversation of w, and adds the question as the walk's latest turn. It
// goes on in the walk's own session. A walk without a session for the
// provider gets a new session that is told the change set and the earlier
// turns. diffstat is empty when the app does not know it.
func FollowupExchange(r *Review, w *Walk, cfg agent.Config, diffstat, question string) *Exchange {
	session := w.Sessions[cfg.Provider]
	f := followupRequest{Question: question, Known: session != "", NewRev: r.Scope.NewRev()}
	req := agent.Request{Dir: r.Repo, SystemPrompt: systemPrompt}
	if f.Known {
		req.ResumeID = session
	} else {
		f.Context = contextPrompt(r.Scope.Describe(), diffstat)
		f.Earlier = w.Turns()
	}
	req.Prompt = followupPrompt(f)
	w.More = append(w.More, Turn{Question: question, Generated: time.Now()})
	return &Exchange{Request: req, rev: r, cfg: cfg, walk: w}
}

// ThreadExchange starts the exchange for a question in t, and adds the
// question to the thread. The thread's first question with a provider
// forks the overview's session of that provider, so every thread starts
// from what the agent learned writing the overview, and threads stay
// apart. Later questions go on in the thread's own session. Without
// either session, a new session is told the change set. diffstat is
// empty when the app does not know it.
func ThreadExchange(r *Review, t *Thread, cfg agent.Config, diffstat, question string) *Exchange {
	t.Messages = append(t.Messages, Message{Role: RoleUser, Text: question, Time: time.Now()})
	t.Agent = cfg.Provider
	session, overviewSession := t.Sessions[cfg.Provider], ""
	if r.Overview != nil {
		overviewSession = r.Overview.Sessions[cfg.Provider]
	}
	req := agent.Request{Dir: r.Repo, SystemPrompt: systemPrompt}
	prompt := questionPrompt(t, question, session != "")
	switch {
	case session != "":
		req.ResumeID = session
	case overviewSession != "":
		req.ResumeID, req.Fork = overviewSession, true
	default:
		prompt = contextPrompt(r.Scope.Describe(), diffstat) + prompt
	}
	req.Prompt = prompt
	return &Exchange{Request: req, rev: r, cfg: cfg, thread: t, reply: len(t.Messages)}
}

// Review is the review the exchange writes into.
func (e *Exchange) Review() *Review { return e.rev }

// Reply is where the answer goes in the messages of the thread.
func (e *Exchange) Reply() int { return e.reply }

// Config is the agent and the model the exchange runs with.
func (e *Exchange) Config() agent.Config { return e.cfg }

// Started records the session the agent started for the run. A walk or a
// thread keeps it at once, so a run that fails later still leaves the
// session to go on from.
func (e *Exchange) Started(session string) {
	if e.thread != nil {
		e.thread.Sessions.Set(e.cfg.Provider, session)
		return
	}
	if e.walk != nil {
		e.walk.Sessions.Set(e.cfg.Provider, session)
		return
	}
	e.session = session
}

// Answered writes the agent's answer into the review. A thread gets the
// answer as a message. A walk's answer is a walkthrough, or the answer as
// Markdown when it holds none. The new overview keeps the sessions of the
// other providers.
func (e *Exchange) Answered(answer string, at time.Time) {
	if e.thread != nil {
		e.addReply(Message{Role: RoleAgent, Text: answer, Time: at})
		return
	}
	if w := e.walk; w != nil {
		if wt, ok := parseWalkthrough(answer); ok {
			w.setAnswer(wt, "", "", at)
		} else {
			w.setAnswer(nil, answer, "", at)
		}
		if len(w.More) == 0 {
			w.Agent, w.Model = e.cfg.Provider, e.cfg.Model
		}
		return
	}
	ov := readOverview(answer, e.changes.Files)
	ov.Agent, ov.Model, ov.Generated = e.cfg.Provider, e.cfg.Model, at
	ov.PatchID, ov.Files = e.changes.PatchID, e.changes.FileIDs
	if old := e.rev.Overview; old != nil {
		ov.Sessions = maps.Clone(old.Sessions)
	}
	if e.session != "" {
		ov.Sessions.Set(e.cfg.Provider, e.session)
	}
	e.rev.Overview = &ov
}

// Failed shows msg in place of the thread's answer, in place of the walk's
// latest answer, or over the overview. An overview with an explanation
// keeps it, with its sessions and the agent that wrote it.
func (e *Exchange) Failed(msg string, at time.Time) {
	if e.thread != nil {
		e.addReply(Message{Role: RoleAgent, Err: msg, Time: at})
		return
	}
	if e.walk != nil {
		e.walk.setAnswer(nil, "", msg, at)
		return
	}
	ov := Overview{Generated: at}
	if old := e.rev.Overview; old.Done() {
		ov = *old
	}
	ov.Err = msg
	e.rev.Overview = &ov
}

// Stopped shows "Stopped" as the failure, for a run the user stopped.
func (e *Exchange) Stopped(at time.Time) { e.Failed("Stopped", at) }

// addReply puts m into the thread right after the question it answers.
func (e *Exchange) addReply(m Message) {
	t := e.thread
	t.Messages = slices.Insert(t.Messages, e.reply, m)
}
