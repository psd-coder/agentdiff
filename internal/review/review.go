package review

import (
	"crypto/rand"
	"encoding/hex"
	"maps"
	"slices"
	"time"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/gitx"
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
	Role Role      `json:"role"`
	Text string    `json:"text"`
	Time time.Time `json:"time"`
	Err  string    `json:"err,omitempty"`
}

// Thread is a note for the coding agent, a discussion with the review
// agent, or both: a thread with only user messages is a plain comment.
type Thread struct {
	ID       string         `json:"id"`
	Anchor   Anchor         `json:"anchor"`
	Messages []Message      `json:"messages"`
	Sessions Sessions       `json:"sessions,omitempty"`
	Agent    agent.Provider `json:"agent,omitempty"`
	Resolved bool           `json:"resolved,omitempty"`
	Excluded bool           `json:"excluded,omitempty"`
	Created  time.Time      `json:"created"`
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

// Overview is the agent's explanation of the whole change set: a
// walkthrough, or Markdown when the agent did not write one.
type Overview struct {
	Walk      *Walkthrough   `json:"walk,omitempty"`
	Markdown  string         `json:"markdown,omitempty"`
	Groups    []Group        `json:"groups,omitempty"`
	Sessions  Sessions       `json:"sessions,omitempty"`
	Agent     agent.Provider `json:"agent"`
	Model     string         `json:"model,omitempty"`
	Generated time.Time      `json:"generated"`
	Err       string         `json:"err,omitempty"`
	// PatchID and Files identify the changes the overview was written
	// for, see gitx.Fingerprint.
	PatchID string            `json:"patchId,omitempty"`
	Files   map[string]string `json:"files,omitempty"`
}

// Empty reports whether the review has nothing written: no overview,
// walkthroughs or threads.
func (r *Review) Empty() bool { return r.Overview == nil && len(r.Walks) == 0 && len(r.Threads) == 0 }

// Done reports whether the overview has an explanation to show.
func (o *Overview) Done() bool { return o != nil && (o.Walk != nil || o.Markdown != "") }

type Review struct {
	Repo     string          `json:"repo"`
	Scope    gitx.Scope      `json:"scope"`
	Overview *Overview       `json:"overview,omitempty"`
	Threads  []*Thread       `json:"threads"`
	Walks    []*Walk         `json:"walks,omitempty"`
	Viewed   map[string]bool `json:"viewed,omitempty"`
	// Kept is set on a review the user restored from the archive, which
	// stays even when none of its files is changed.
	Kept    bool      `json:"kept,omitempty"`
	Updated time.Time `json:"updated"`
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
		if t.Anchor.File == file && t.Anchor.onFile() {
			out = append(out, t)
		}
	}
	return out
}

// ThreadCounts is the number of threads on each file, the ones ThreadsOn
// returns for it.
func (r *Review) ThreadCounts() map[string]int {
	n := map[string]int{}
	for _, t := range r.Threads {
		if t.Anchor.onFile() {
			n[t.Anchor.File]++
		}
	}
	return n
}

// onFile tells whether the anchor is a file or lines of it.
func (a Anchor) onFile() bool {
	return a.Kind == AnchorFile || a.Kind == AnchorLines
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

// Stale reports whether the overview was written for other changes than
// the ones with patchID. An overview from before patch IDs compares the
// files of its groups instead.
func (o *Overview) Stale(patchID string, files []string) bool {
	if o == nil {
		return false
	}
	if o.PatchID != "" {
		return o.PatchID != patchID
	}
	if len(o.Groups) == 0 {
		return false
	}
	grouped := map[string]bool{}
	for _, g := range o.Groups {
		for _, f := range g.Files {
			grouped[f] = true
		}
	}
	if len(grouped) != len(files) {
		return true
	}
	for _, f := range files {
		if !grouped[f] {
			return true
		}
	}
	return false
}

// Drift counts how the changed files differ from the ones an overview was
// written for.
type Drift struct {
	// Written is how many files the overview was written for.
	Written int
	// Changed counts the files whose changes are not the same any more,
	// Gone the files that are no longer changed, and New the changed
	// files the overview does not cover.
	Changed, Gone, New int
	// Known is false for an overview from before file IDs, which knows
	// its files from its groups only and cannot tell a changed one.
	Known bool
}

// Disjoint reports whether none of the overview's files is still changed.
func (d Drift) Disjoint() bool { return d.Written > 0 && d.Gone == d.Written }

// Drift compares the files the overview was written for with files, which
// maps the changed files to the IDs of their changes.
func (o *Overview) Drift(files map[string]string) Drift {
	written, known := o.written(), o.Files != nil
	d := Drift{Written: len(written), Known: known}
	for f, id := range written {
		now, ok := files[f]
		switch {
		case !ok:
			d.Gone++
		case known && (id == "" || id != now):
			d.Changed++
		}
	}
	for f := range files {
		if _, ok := written[f]; !ok {
			d.New++
		}
	}
	return d
}

// WrittenFiles returns the files the overview was written for.
func (o *Overview) WrittenFiles() []string { return slices.Sorted(maps.Keys(o.written())) }

// written maps the files the overview was written for to the IDs of
// their changes, which an overview from before file IDs does not know.
func (o *Overview) written() map[string]string {
	if o.Files != nil {
		return o.Files
	}
	out := map[string]string{}
	for _, g := range o.Groups {
		for _, f := range g.Files {
			out[f] = ""
		}
	}
	return out
}
