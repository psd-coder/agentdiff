package review

import (
	"encoding/json"

	"github.com/psd-coder/agentdiff/internal/agent"
)

// Sessions holds one agent session for each provider. A run resumes or
// forks only the session of its own provider, so a switch between agents
// starts a new session, and a switch back continues the earlier one.
type Sessions map[agent.Provider]string

func (sessions *Sessions) Set(provider agent.Provider, id string) {
	if *sessions == nil {
		*sessions = Sessions{}
	}
	(*sessions)[provider] = id
}

// legacySession is the one session that review files kept before they
// kept a session for each provider. It belongs to the item's agent.
type legacySession struct {
	SessionID string `json:"sessionId"`
}

func (legacy legacySession) into(sessions *Sessions, provider agent.Provider) {
	if legacy.SessionID != "" && provider != "" {
		sessions.Set(provider, legacy.SessionID)
	}
}

func (overview *Overview) UnmarshalJSON(data []byte) error {
	type plain Overview
	var old legacySession
	if err := json.Unmarshal(data, (*plain)(overview)); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &old); err != nil {
		return err
	}
	old.into(&overview.Sessions, overview.Agent)
	return nil
}

func (walk *Walk) UnmarshalJSON(data []byte) error {
	type plain Walk
	var old legacySession
	if err := json.Unmarshal(data, (*plain)(walk)); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &old); err != nil {
		return err
	}
	old.into(&walk.Sessions, walk.Agent)
	return nil
}

func (thread *Thread) UnmarshalJSON(data []byte) error {
	type plain Thread
	var old legacySession
	if err := json.Unmarshal(data, (*plain)(thread)); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &old); err != nil {
		return err
	}
	old.into(&thread.Sessions, thread.Agent)
	return nil
}
