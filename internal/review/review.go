package review

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/psdcoder/agentdiff/internal/agent"
	"github.com/psdcoder/agentdiff/internal/gitx"
)

type Side string

const (
	SideOld Side = "old"
	SideNew Side = "new"
)

type AnchorKind string

const (
	AnchorGeneral AnchorKind = "general"
	AnchorSection AnchorKind = "section"
	AnchorFile    AnchorKind = "file"
	AnchorLines   AnchorKind = "lines"
)

// Anchor is what a thread is about. Lines are 1-based and inclusive.
type Anchor struct {
	Kind    AnchorKind `json:"kind"`
	File    string     `json:"file,omitempty"`
	Side    Side       `json:"side,omitempty"`
	Start   int        `json:"start,omitempty"`
	End     int        `json:"end,omitempty"`
	Section string     `json:"section,omitempty"`
	Quote   string     `json:"quote,omitempty"`
}

type Role string

const (
	RoleUser  Role = "user"
	RoleAgent Role = "agent"
)

type Message struct {
	Role     Role      `json:"role"`
	Text     string    `json:"text"`
	Time     time.Time `json:"time"`
	Err      string    `json:"err,omitempty"`
	Pending  bool      `json:"-"`
	Activity string    `json:"-"`
}

// Thread is a note for the coding agent, a discussion with the review
// agent, or both: a thread with only user messages is a plain comment.
type Thread struct {
	ID        string         `json:"id"`
	Anchor    Anchor         `json:"anchor"`
	Messages  []Message      `json:"messages"`
	SessionID string         `json:"sessionId,omitempty"`
	Agent     agent.Provider `json:"agent,omitempty"`
	Resolved  bool           `json:"resolved,omitempty"`
	Excluded  bool           `json:"excluded,omitempty"`
	Created   time.Time      `json:"created"`
}

func (t *Thread) Busy() bool {
	return len(t.Messages) > 0 && t.Messages[len(t.Messages)-1].Pending
}

func (t *Thread) Discussed() bool {
	for _, m := range t.Messages {
		if m.Role == RoleAgent {
			return true
		}
	}
	return false
}

type Group struct {
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Files   []string `json:"files"`
}

type Overview struct {
	Markdown  string         `json:"markdown"`
	Groups    []Group        `json:"groups,omitempty"`
	SessionID string         `json:"sessionId,omitempty"`
	Agent     agent.Provider `json:"agent"`
	Model     string         `json:"model,omitempty"`
	Generated time.Time      `json:"generated"`
	Err       string         `json:"err,omitempty"`
}

// Settings are what a review overrides of the global configuration; nil
// fields follow it.
type Settings struct {
	Agent     *agent.Config `json:"agent,omitempty"`
	Questions *string       `json:"questions,omitempty"`
}

type Review struct {
	Repo     string          `json:"repo"`
	Scope    gitx.Scope      `json:"scope"`
	Settings Settings        `json:"settings"`
	Overview *Overview       `json:"overview,omitempty"`
	Threads  []*Thread       `json:"threads"`
	Viewed   map[string]bool `json:"viewed,omitempty"`
	Updated  time.Time       `json:"updated"`
}

func New(repo string, scope gitx.Scope) *Review {
	return &Review{Repo: repo, Scope: scope, Viewed: map[string]bool{}}
}

func (r *Review) AddThread(a Anchor) *Thread {
	t := &Thread{ID: newID(), Anchor: a, Created: time.Now()}
	r.Threads = append(r.Threads, t)
	return t
}

func (r *Review) Thread(id string) *Thread {
	for _, t := range r.Threads {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func (r *Review) DeleteThread(id string) {
	for i, t := range r.Threads {
		if t.ID == id {
			r.Threads = append(r.Threads[:i], r.Threads[i+1:]...)
			return
		}
	}
}

func (r *Review) ThreadsOn(file string) []*Thread {
	var out []*Thread
	for _, t := range r.Threads {
		if t.Anchor.File == file && (t.Anchor.Kind == AnchorFile || t.Anchor.Kind == AnchorLines) {
			out = append(out, t)
		}
	}
	return out
}

func (r *Review) ThreadsOnSection(title string) []*Thread {
	var out []*Thread
	for _, t := range r.Threads {
		if t.Anchor.Kind == AnchorSection && t.Anchor.Section == title {
			out = append(out, t)
		}
	}
	return out
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
