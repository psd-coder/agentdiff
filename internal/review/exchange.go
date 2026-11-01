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
func OverviewExchange(review *Review, changes ChangeSet, cfg agent.Config, prompts Prompts) *Exchange {
	prompt := overviewPrompt(prompts, review.Scope.Describe(), changes.Diffstat, review.Scope.NewRev(), changes.Files)
	return &Exchange{
		Request: agent.Request{Dir: review.Repo, Prompt: prompt, SystemPrompt: prompts.System()},
		rev:     review,
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
func DeeperExchange(review *Review, walk *Walk, cfg agent.Config, prompts Prompts, diffstat string) *Exchange {
	parentSession := ""
	if parent := review.Walk(walk.Parent); parent != nil {
		parentSession = parent.Sessions[cfg.Provider]
	} else if ov := review.Overview; walk.Parent == "" && ov != nil {
		parentSession = ov.Sessions[cfg.Provider]
	}
	deeper := deeperRequest{Question: walk.Question, Known: parentSession != "", NewRev: review.Scope.NewRev()}
	if step, ok := review.StepOf(walk); ok {
		deeper.Step = &step
	}
	req := agent.Request{Dir: review.Repo, SystemPrompt: prompts.System()}
	if deeper.Known {
		req.ResumeID, req.Fork = parentSession, true
	} else {
		deeper.Context = contextPrompt(review.Scope.Describe(), diffstat)
		path := review.WalkPath(walk.ID)
		for _, p := range path[:len(path)-1] {
			deeper.Path = append(deeper.Path, review.WalkTitle(p))
		}
	}
	req.Prompt = deeperPrompt(prompts, deeper)
	walk.Content, walk.Markdown, walk.Err, walk.More, walk.Agent, walk.Model, walk.Sessions = nil, "", "", nil, "", "", nil
	return &Exchange{Request: req, rev: review, cfg: cfg, walk: walk}
}

// FollowupExchange starts the exchange for a follow-up question in the
// conversation of w, and adds the question as the walk's latest turn. It
// goes on in the walk's own session. A walk without a session for the
// provider gets a new session that is told the change set and the earlier
// turns. diffstat is empty when the app does not know it.
func FollowupExchange(review *Review, walk *Walk, cfg agent.Config, prompts Prompts, diffstat, question string) *Exchange {
	session := walk.Sessions[cfg.Provider]
	followup := followupRequest{Question: question, Known: session != "", NewRev: review.Scope.NewRev()}
	req := agent.Request{Dir: review.Repo, SystemPrompt: prompts.System()}
	if followup.Known {
		req.ResumeID = session
	} else {
		followup.Context = contextPrompt(review.Scope.Describe(), diffstat)
		followup.Earlier = walk.Turns()
	}
	req.Prompt = followupPrompt(prompts, followup)
	walk.More = append(walk.More, Turn{Question: question, Generated: time.Now()})
	return &Exchange{Request: req, rev: review, cfg: cfg, walk: walk}
}

// ThreadExchange starts the exchange for a question in t, and adds the
// question to the thread. The thread's first question with a provider
// forks the overview's session of that provider, so every thread starts
// from what the agent learned writing the overview, and threads stay
// apart. Later questions go on in the thread's own session. Without
// either session, a new session is told the change set. diffstat is
// empty when the app does not know it.
func ThreadExchange(review *Review, thread *Thread, cfg agent.Config, prompts Prompts, diffstat, question string) *Exchange {
	thread.Messages = append(thread.Messages, Message{Role: RoleUser, Text: question, Time: time.Now()})
	thread.Agent = cfg.Provider
	session, overviewSession := thread.Sessions[cfg.Provider], ""
	if review.Overview != nil {
		overviewSession = review.Overview.Sessions[cfg.Provider]
	}
	req := agent.Request{Dir: review.Repo, SystemPrompt: prompts.System()}
	switch {
	case session != "":
		req.ResumeID = session
		req.Prompt = question
	case overviewSession != "":
		req.ResumeID, req.Fork = overviewSession, true
		req.Prompt = threadPrompt(prompts, thread, question, "")
	default:
		req.Prompt = threadPrompt(prompts, thread, question, contextPrompt(review.Scope.Describe(), diffstat))
	}
	return &Exchange{Request: req, rev: review, cfg: cfg, thread: thread, reply: len(thread.Messages)}
}

// TargetOverview is the target of the overview's exchange.
const TargetOverview = "overview"

// Kind is what an exchange writes: the overview, a walk or a thread.
type Kind int

const (
	KindOverview Kind = iota
	KindWalk
	KindThread
)

// Kind is what the exchange writes.
func (exchange *Exchange) Kind() Kind {
	switch {
	case exchange.thread != nil:
		return KindThread
	case exchange.walk != nil:
		return KindWalk
	}
	return KindOverview
}

// Target is what the exchange writes into: TargetOverview, or the ID of
// the walk or the thread.
func (exchange *Exchange) Target() string {
	switch {
	case exchange.thread != nil:
		return exchange.thread.ID
	case exchange.walk != nil:
		return exchange.walk.ID
	}
	return TargetOverview
}

// Review is the review the exchange writes into.
func (exchange *Exchange) Review() *Review { return exchange.rev }

// Reply is where the answer goes in the messages of the thread.
func (exchange *Exchange) Reply() int { return exchange.reply }

// Config is the agent and the model the exchange runs with.
func (exchange *Exchange) Config() agent.Config { return exchange.cfg }

// Started records the session the agent started for the run. A walk or a
// thread keeps it at once, so a run that fails later still leaves the
// session to go on from.
func (exchange *Exchange) Started(session string) {
	if exchange.thread != nil {
		exchange.thread.Sessions.Set(exchange.cfg.Provider, session)
		return
	}
	if exchange.walk != nil {
		exchange.walk.Sessions.Set(exchange.cfg.Provider, session)
		return
	}
	exchange.session = session
}

// Answered writes the agent's answer into the review. A thread gets the
// answer as a message. A walk's answer is a walkthrough, or the answer as
// Markdown when it holds none. The new overview keeps the sessions of the
// other providers.
func (exchange *Exchange) Answered(answer string, at time.Time) {
	if exchange.thread != nil {
		exchange.addReply(Message{Role: RoleAgent, Text: answer, Time: at})
		return
	}
	if walk := exchange.walk; walk != nil {
		if wt, ok := parseWalkthrough(answer); ok {
			walk.setAnswer(wt, "", "", at)
		} else {
			walk.setAnswer(nil, answer, "", at)
		}
		if len(walk.More) == 0 {
			walk.Agent, walk.Model = exchange.cfg.Provider, exchange.cfg.Model
		}
		return
	}
	ov := readOverview(answer, exchange.changes.Files)
	ov.Agent, ov.Model, ov.Generated = exchange.cfg.Provider, exchange.cfg.Model, at
	ov.PatchID, ov.Files = exchange.changes.PatchID, exchange.changes.FileIDs
	if old := exchange.rev.Overview; old != nil {
		ov.Sessions = maps.Clone(old.Sessions)
	}
	if exchange.session != "" {
		ov.Sessions.Set(exchange.cfg.Provider, exchange.session)
	}
	exchange.rev.Overview = &ov
}

// Failed shows msg in place of the thread's answer, in place of the walk's
// latest answer, or over the overview. An overview with an explanation
// keeps it, with its sessions and the agent that wrote it.
func (exchange *Exchange) Failed(msg string, at time.Time) {
	if exchange.thread != nil {
		exchange.addReply(Message{Role: RoleAgent, Err: msg, Time: at})
		return
	}
	if exchange.walk != nil {
		exchange.walk.setAnswer(nil, "", msg, at)
		return
	}
	ov := Overview{Generated: at}
	if old := exchange.rev.Overview; old.Done() {
		ov = *old
	}
	ov.Err = msg
	exchange.rev.Overview = &ov
}

// Stopped shows Stopped as the failure, for a run the user stopped.
func (exchange *Exchange) Stopped(at time.Time) { exchange.Failed(Stopped, at) }

// addReply puts m into the thread right after the question it answers.
func (exchange *Exchange) addReply(message Message) {
	thread := exchange.thread
	thread.Messages = slices.Insert(thread.Messages, exchange.reply, message)
}
