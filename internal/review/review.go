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

func (thread *Thread) Discussed() bool {
	for _, m := range thread.Messages {
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
func (review *Review) Empty() bool {
	return review.Overview == nil && len(review.Walks) == 0 && len(review.Threads) == 0
}

// Done reports whether the overview has an explanation to show.
func (overview *Overview) Done() bool {
	return overview != nil && (overview.Walk != nil || overview.Markdown != "")
}

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

func (review *Review) AddThread(anchor Anchor) *Thread {
	thread := &Thread{ID: newID(), Anchor: anchor, Created: time.Now()}
	review.Threads = append(review.Threads, thread)
	return thread
}

func (review *Review) Thread(id string) *Thread {
	for _, t := range review.Threads {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func (review *Review) DeleteThread(id string) {
	for i, t := range review.Threads {
		if t.ID == id {
			review.Threads = append(review.Threads[:i], review.Threads[i+1:]...)
			return
		}
	}
}

func (review *Review) ThreadsOn(file string) []*Thread {
	var out []*Thread
	for _, t := range review.Threads {
		if t.Anchor.File == file && t.Anchor.onFile() {
			out = append(out, t)
		}
	}
	return out
}

// ThreadCounts is the number of threads on each file, the ones ThreadsOn
// returns for it.
func (review *Review) ThreadCounts() map[string]int {
	counts := map[string]int{}
	for _, t := range review.Threads {
		if t.Anchor.onFile() {
			counts[t.Anchor.File]++
		}
	}
	return counts
}

// onFile tells whether the anchor is a file or lines of it.
func (anchor Anchor) onFile() bool {
	return anchor.Kind == AnchorFile || anchor.Kind == AnchorLines
}

func (review *Review) ThreadsOnSection(title string) []*Thread {
	var out []*Thread
	for _, t := range review.Threads {
		if t.Anchor.Kind == AnchorSection && t.Anchor.Section == title {
			out = append(out, t)
		}
	}
	return out
}

func newID() string {
	random := make([]byte, 6)
	_, _ = rand.Read(random)
	return hex.EncodeToString(random)
}

// Stale reports whether the overview was written for other changes than
// the ones with patchID. An overview from before patch IDs compares the
// files of its groups instead.
func (overview *Overview) Stale(patchID string, files []string) bool {
	if overview == nil {
		return false
	}
	if overview.PatchID != "" {
		return overview.PatchID != patchID
	}
	if len(overview.Groups) == 0 {
		return false
	}
	grouped := map[string]bool{}
	for _, g := range overview.Groups {
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
func (drift Drift) Disjoint() bool { return drift.Written > 0 && drift.Gone == drift.Written }

// Drift compares the files the overview was written for with files, which
// maps the changed files to the IDs of their changes.
func (overview *Overview) Drift(files map[string]string) Drift {
	written, known := overview.written(), overview.Files != nil
	drift := Drift{Written: len(written), Known: known}
	for f, id := range written {
		now, ok := files[f]
		switch {
		case !ok:
			drift.Gone++
		case known && (id == "" || id != now):
			drift.Changed++
		}
	}
	for f := range files {
		if _, ok := written[f]; !ok {
			drift.New++
		}
	}
	return drift
}

// WrittenFiles returns the files the overview was written for.
func (overview *Overview) WrittenFiles() []string {
	return slices.Sorted(maps.Keys(overview.written()))
}

// written maps the files the overview was written for to the IDs of
// their changes, which an overview from before file IDs does not know.
func (overview *Overview) written() map[string]string {
	if overview.Files != nil {
		return overview.Files
	}
	out := map[string]string{}
	for _, g := range overview.Groups {
		for _, f := range g.Files {
			out[f] = ""
		}
	}
	return out
}
