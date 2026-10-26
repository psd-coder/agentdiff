package review

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/psdcoder/agentdiff/internal/agent"
)

// maxRefLines is the longest snippet a reference shows.
const maxRefLines = 15

// Ref points at lines of a file, 1-based and inclusive, on the old or the
// new side of the change.
type Ref struct {
	File  string `json:"file"`
	Side  Side   `json:"side,omitempty"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Why   string `json:"why,omitempty"`
}

type Step struct {
	Title string `json:"title"`
	Text  string `json:"text"`
	Refs  []Ref  `json:"refs,omitempty"`
}

type CheckItem struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
	Refs     []Ref  `json:"refs,omitempty"`
}

// Walkthrough explains changes as a short summary and a few steps to read
// in order, each with the code that shows it best.
type Walkthrough struct {
	Summary   string      `json:"summary"`
	Steps     []Step      `json:"steps"`
	Checklist []CheckItem `json:"checklist,omitempty"`
	Groups    []Group     `json:"groups,omitempty"`
}

// Walk is a walkthrough one level deeper than its parent: into one of the
// parent's steps, or answering a question about the parent as a whole
// (Step -1). Parent is empty for the overview. A walk with a question is
// a conversation: More holds the follow-up questions and their answers.
type Walk struct {
	ID        string         `json:"id"`
	Parent    string         `json:"parent,omitempty"`
	Step      int            `json:"step"`
	Question  string         `json:"question,omitempty"`
	Content   *Walkthrough   `json:"content,omitempty"`
	Markdown  string         `json:"markdown,omitempty"`
	SessionID string         `json:"sessionId,omitempty"`
	Agent     agent.Provider `json:"agent,omitempty"`
	Model     string         `json:"model,omitempty"`
	Generated time.Time      `json:"generated"`
	Err       string         `json:"err,omitempty"`
	More      []Turn         `json:"more,omitempty"`
}

// Turn is a question and the walkthrough that answers it.
type Turn struct {
	Question  string       `json:"question"`
	Content   *Walkthrough `json:"content,omitempty"`
	Markdown  string       `json:"markdown,omitempty"`
	Err       string       `json:"err,omitempty"`
	Generated time.Time    `json:"generated"`
}

// Turns returns the walk's first question and answer, then the
// follow-ups.
func (w *Walk) Turns() []Turn {
	first := Turn{Question: w.Question, Content: w.Content, Markdown: w.Markdown, Err: w.Err, Generated: w.Generated}
	return append([]Turn{first}, w.More...)
}

// SetAnswer puts an answer, or the error that came instead, into the
// latest turn.
func (w *Walk) SetAnswer(content *Walkthrough, markdown, err string, at time.Time) {
	if n := len(w.More); n > 0 {
		t := &w.More[n-1]
		t.Content, t.Markdown, t.Err, t.Generated = content, markdown, err, at
		return
	}
	w.Content, w.Markdown, w.Err, w.Generated = content, markdown, err, at
}

func (r *Review) AddWalk(parent string, step int, question string) *Walk {
	w := &Walk{ID: newID(), Parent: parent, Step: step, Question: strings.TrimSpace(question), Generated: time.Now()}
	r.Walks = append(r.Walks, w)
	return w
}

func (r *Review) Walk(id string) *Walk {
	for _, w := range r.Walks {
		if w.ID == id {
			return w
		}
	}
	return nil
}

// WalksOn returns the walks under a step of a parent, or under the parent
// as a whole for step -1.
func (r *Review) WalksOn(parent string, step int) []*Walk {
	var out []*Walk
	for _, w := range r.Walks {
		if w.Parent == parent && w.Step == step {
			out = append(out, w)
		}
	}
	return out
}

// WalkPath returns a walk and its ancestors, the overview's child first.
func (r *Review) WalkPath(id string) []*Walk {
	var path []*Walk
	for w := r.Walk(id); w != nil && len(path) <= len(r.Walks); w = r.Walk(w.Parent) {
		path = append([]*Walk{w}, path...)
	}
	return path
}

// DeleteWalk deletes a walk with every walk under it.
func (r *Review) DeleteWalk(id string) {
	gone := map[string]bool{id: true}
	for changed := true; changed; {
		changed = false
		for _, w := range r.Walks {
			if !gone[w.ID] && gone[w.Parent] {
				gone[w.ID], changed = true, true
			}
		}
	}
	kept := r.Walks[:0]
	for _, w := range r.Walks {
		if !gone[w.ID] {
			kept = append(kept, w)
		}
	}
	r.Walks = kept
}

// DeleteStepWalks deletes the walks into the steps of a parent, with the
// walks under them. They point at steps by index, so they go when the
// parent gets new steps. Questions about the parent as a whole stay.
func (r *Review) DeleteStepWalks(parent string) {
	for _, w := range slices.Clone(r.Walks) {
		if w.Parent == parent && w.Step >= 0 {
			r.DeleteWalk(w.ID)
		}
	}
}

// HasStepWalks reports whether a parent has walks into its steps.
func (r *Review) HasStepWalks(parent string) bool {
	for _, w := range r.Walks {
		if w.Parent == parent && w.Step >= 0 {
			return true
		}
	}
	return false
}

// Steps returns the steps of a parent: the overview's for "".
func (r *Review) Steps(parent string) []Step {
	if parent == "" {
		if r.Overview != nil && r.Overview.Walk != nil {
			return r.Overview.Walk.Steps
		}
		return nil
	}
	if w := r.Walk(parent); w != nil && w.Content != nil {
		return w.Content.Steps
	}
	return nil
}

// StepOf returns the step a walk goes deeper into, if it is about one.
func (r *Review) StepOf(w *Walk) (Step, bool) {
	steps := r.Steps(w.Parent)
	if w.Step < 0 || w.Step >= len(steps) {
		return Step{}, false
	}
	return steps[w.Step], true
}

// WalkTitle is what a walk is about, in a few words.
func (r *Review) WalkTitle(w *Walk) string {
	if w.Question != "" {
		return w.Question
	}
	if s, ok := r.StepOf(w); ok {
		return s.Title
	}
	return "Deeper"
}

var jsonFence = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*\\})\\s*```")

// ParseWalkthrough reads the walkthrough JSON out of an agent's answer:
// a fenced block, or the outermost braces. It reports false when the
// answer holds no walkthrough, so the caller can show it as Markdown.
func ParseWalkthrough(answer string) (*Walkthrough, bool) {
	raw := ""
	if m := jsonFence.FindAllStringSubmatch(answer, -1); len(m) > 0 {
		raw = m[len(m)-1][1]
	} else if i, j := strings.Index(answer, "{"), strings.LastIndex(answer, "}"); i >= 0 && j > i {
		raw = answer[i : j+1]
	}
	var w Walkthrough
	if raw == "" || json.Unmarshal([]byte(raw), &w) != nil {
		return nil, false
	}
	w.Summary = strings.TrimSpace(w.Summary)
	steps := w.Steps[:0]
	for _, s := range w.Steps {
		s.Title, s.Text = strings.TrimSpace(s.Title), strings.TrimSpace(s.Text)
		if s.Title == "" && s.Text == "" {
			continue
		}
		s.Refs = cleanRefs(s.Refs)
		steps = append(steps, s)
	}
	w.Steps = steps
	for i := range w.Checklist {
		w.Checklist[i].Refs = cleanRefs(w.Checklist[i].Refs)
	}
	if w.Summary == "" && len(w.Steps) == 0 {
		return nil, false
	}
	return &w, true
}

// cleanRefs drops references without a file or a line and cuts long
// ones to maxRefLines.
func cleanRefs(refs []Ref) []Ref {
	var out []Ref
	for _, r := range refs {
		r.File = strings.TrimPrefix(strings.TrimSpace(r.File), "./")
		if r.File == "" || r.Start < 1 {
			continue
		}
		if r.Side != SideOld {
			r.Side = SideNew
		}
		r.End = min(max(r.End, r.Start), r.Start+maxRefLines-1)
		out = append(out, r)
	}
	return out
}

// Label is the reference as a reviewer reads it: path:start-end.
func (r Ref) Label() string {
	return Anchor{Kind: AnchorLines, File: r.File, Side: r.Side, Start: r.Start, End: r.End}.Label()
}
