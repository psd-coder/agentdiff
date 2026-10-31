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

// Exchange is one run of the agent for the overview of a review. It holds
// the request to send, and it writes what comes back into the review.
type Exchange struct {
	Request agent.Request
	rev     *Review
	cfg     agent.Config
	changes ChangeSet
	session string
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

// Started records the session the agent started for the run.
func (e *Exchange) Started(session string) { e.session = session }

// Answered makes the agent's answer the overview of the review. The new
// overview keeps the sessions of the other providers.
func (e *Exchange) Answered(answer string, at time.Time) {
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

// Failed shows msg over the overview. An overview with an explanation
// keeps it, with its sessions and the agent that wrote it.
func (e *Exchange) Failed(msg string, at time.Time) {
	ov := Overview{Generated: at}
	if old := e.rev.Overview; old.Done() {
		ov = *old
	}
	ov.Err = msg
	e.rev.Overview = &ov
}

// Stopped shows "Stopped" over the overview, for a run the user stopped.
func (e *Exchange) Stopped(at time.Time) { e.Failed("Stopped", at) }
