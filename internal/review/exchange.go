package review

import (
	"maps"
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

// Exchange is one run of the agent for the overview or a walk of a
// review. It holds the request to send, and it writes what comes back into
// the review.
type Exchange struct {
	Request agent.Request
	rev     *Review
	cfg     agent.Config
	changes ChangeSet
	session string
	// walk is the walk the run writes, nil for the overview.
	walk *Walk
}

// OverviewExchange starts the exchange for a new overview of r. The
// overview always starts a new session, so it reads the changes as they
// are now.
func OverviewExchange(r *Review, changes ChangeSet, cfg agent.Config, questions string) *Exchange {
	prompt := overviewPrompt(r.Scope.Describe(), changes.Diffstat, r.Scope.NewRev(), changes.Files, questions)
	return &Exchange{
		Request: agent.Request{Dir: r.Repo, Prompt: prompt, SystemPrompt: SystemPrompt},
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
	req := agent.Request{Dir: r.Repo, SystemPrompt: SystemPrompt}
	if d.Known {
		req.ResumeID, req.Fork = parentSession, true
	} else {
		d.Context = ContextPrompt(r.Scope.Describe(), diffstat)
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
	req := agent.Request{Dir: r.Repo, SystemPrompt: SystemPrompt}
	if f.Known {
		req.ResumeID = session
	} else {
		f.Context = ContextPrompt(r.Scope.Describe(), diffstat)
		f.Earlier = w.Turns()
	}
	req.Prompt = followupPrompt(f)
	w.More = append(w.More, Turn{Question: question, Generated: time.Now()})
	return &Exchange{Request: req, rev: r, cfg: cfg, walk: w}
}

// Started records the session the agent started for the run. A walk keeps
// it at once, so a run that fails later still leaves the session to go on
// from.
func (e *Exchange) Started(session string) {
	if e.walk != nil {
		e.walk.Sessions.Set(e.cfg.Provider, session)
		return
	}
	e.session = session
}

// Answered writes the agent's answer into the review. A walk's answer is a
// walkthrough, or the answer as Markdown when it holds none. The new
// overview keeps the sessions of the other providers.
func (e *Exchange) Answered(answer string, at time.Time) {
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

// Failed shows msg in place of the walk's latest answer, or over the
// overview. An overview with an explanation keeps it, with its sessions
// and the agent that wrote it.
func (e *Exchange) Failed(msg string, at time.Time) {
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
