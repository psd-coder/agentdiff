package review

import (
	"encoding/json"

	"github.com/psd-coder/agentdiff/internal/agent"
)

// Sessions holds one agent session for each provider. A run resumes or
// forks only the session of its own provider, so a switch between agents
// starts a new session, and a switch back continues the earlier one.
type Sessions map[agent.Provider]string

func (s *Sessions) Set(p agent.Provider, id string) {
	if *s == nil {
		*s = Sessions{}
	}
	(*s)[p] = id
}

// legacySession is the one session that review files kept before they
// kept a session for each provider. It belongs to the item's agent.
type legacySession struct {
	SessionID string `json:"sessionId"`
}

func (l legacySession) into(s *Sessions, p agent.Provider) {
	if l.SessionID != "" && p != "" {
		s.Set(p, l.SessionID)
	}
}

func (o *Overview) UnmarshalJSON(b []byte) error {
	type plain Overview
	var old legacySession
	if err := json.Unmarshal(b, (*plain)(o)); err != nil {
		return err
	}
	if err := json.Unmarshal(b, &old); err != nil {
		return err
	}
	old.into(&o.Sessions, o.Agent)
	return nil
}

func (w *Walk) UnmarshalJSON(b []byte) error {
	type plain Walk
	var old legacySession
	if err := json.Unmarshal(b, (*plain)(w)); err != nil {
		return err
	}
	if err := json.Unmarshal(b, &old); err != nil {
		return err
	}
	old.into(&w.Sessions, w.Agent)
	return nil
}

func (t *Thread) UnmarshalJSON(b []byte) error {
	type plain Thread
	var old legacySession
	if err := json.Unmarshal(b, (*plain)(t)); err != nil {
		return err
	}
	if err := json.Unmarshal(b, &old); err != nil {
		return err
	}
	old.into(&t.Sessions, t.Agent)
	return nil
}
