package review

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/psd-coder/agentdiff/internal/agent"
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
	// Code is the lines of the reference as they were while the review
	// was written for the changes, saved for when those changes are gone.
	Code []CodeLine `json:"code,omitempty"`
}

type CodeLine struct {
	Text    string `json:"text"`
	Changed bool   `json:"changed,omitempty"`
}

type Step struct {
	Title string `json:"title"`
	Text  string `json:"text"`
	Refs  []Ref  `json:"refs,omitempty"`
}

// Section is a part of a walkthrough that the prompt asks for besides the
// steps, as a checklist or a list of risks. Its items are written as steps
// are, and the reviewer cannot go deeper into them.
type Section struct {
	Title string `json:"title"`
	Items []Step `json:"items"`
}

// Walkthrough explains changes as a short summary and a few steps to read
// in order, each with the code that shows it best.
type Walkthrough struct {
	Summary  string    `json:"summary"`
	Steps    []Step    `json:"steps"`
	Sections []Section `json:"sections,omitempty"`
	Groups   []Group   `json:"groups,omitempty"`
}

// checklistTitle is the title of the section that holds the checklist of
// a walkthrough written before sections.
const checklistTitle = "Checklist"

// UnmarshalJSON reads a walkthrough. The checklist of a walkthrough
// written before sections becomes its first section, with the questions
// as item titles.
func (walkthrough *Walkthrough) UnmarshalJSON(data []byte) error {
	type plain Walkthrough
	var decoded struct {
		plain
		Checklist []struct {
			Question string `json:"question"`
			Answer   string `json:"answer"`
			Refs     []Ref  `json:"refs"`
		} `json:"checklist"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*walkthrough = Walkthrough(decoded.plain)
	if len(decoded.Checklist) == 0 {
		return nil
	}
	items := make([]Step, len(decoded.Checklist))
	for i, c := range decoded.Checklist {
		items[i] = Step{Title: c.Question, Text: c.Answer, Refs: c.Refs}
	}
	walkthrough.Sections = append([]Section{{Title: checklistTitle, Items: items}}, walkthrough.Sections...)
	return nil
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
	Sessions  Sessions       `json:"sessions,omitempty"`
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
func (walk *Walk) Turns() []Turn {
	first := Turn{Question: walk.Question, Content: walk.Content, Markdown: walk.Markdown, Err: walk.Err, Generated: walk.Generated}
	return append([]Turn{first}, walk.More...)
}

// setAnswer puts an answer, or the error that came instead, into the
// latest turn.
func (walk *Walk) setAnswer(content *Walkthrough, markdown, err string, at time.Time) {
	if count := len(walk.More); count > 0 {
		turn := &walk.More[count-1]
		turn.Content, turn.Markdown, turn.Err, turn.Generated = content, markdown, err, at
		return
	}
	walk.Content, walk.Markdown, walk.Err, walk.Generated = content, markdown, err, at
}

func (review *Review) AddWalk(parent string, step int, question string) *Walk {
	walk := &Walk{ID: newID(), Parent: parent, Step: step, Question: strings.TrimSpace(question), Generated: time.Now()}
	review.Walks = append(review.Walks, walk)
	return walk
}

func (review *Review) Walk(id string) *Walk {
	for _, w := range review.Walks {
		if w.ID == id {
			return w
		}
	}
	return nil
}

// WalksOn returns the walks under a step of a parent, or under the parent
// as a whole for step -1.
func (review *Review) WalksOn(parent string, step int) []*Walk {
	var out []*Walk
	for _, w := range review.Walks {
		if w.Parent == parent && w.Step == step {
			out = append(out, w)
		}
	}
	return out
}

// EachRef calls fn with every reference of the review's walkthroughs.
func (review *Review) EachRef(fn func(*Ref)) {
	walkthrough := func(w *Walkthrough) {
		if w == nil {
			return
		}
		for i := range w.Steps {
			for j := range w.Steps[i].Refs {
				fn(&w.Steps[i].Refs[j])
			}
		}
		for i := range w.Sections {
			for j := range w.Sections[i].Items {
				for k := range w.Sections[i].Items[j].Refs {
					fn(&w.Sections[i].Items[j].Refs[k])
				}
			}
		}
	}
	if review.Overview != nil {
		walkthrough(review.Overview.Walk)
	}
	for _, w := range review.Walks {
		walkthrough(w.Content)
		for _, t := range w.More {
			walkthrough(t.Content)
		}
	}
}

// WalkPath returns a walk and its ancestors, the overview's child first.
func (review *Review) WalkPath(id string) []*Walk {
	var path []*Walk
	for w := review.Walk(id); w != nil && len(path) <= len(review.Walks); w = review.Walk(w.Parent) {
		path = append([]*Walk{w}, path...)
	}
	return path
}

// DeleteWalk deletes a walk with every walk under it.
func (review *Review) DeleteWalk(id string) {
	gone := map[string]bool{id: true}
	for changed := true; changed; {
		changed = false
		for _, w := range review.Walks {
			if !gone[w.ID] && gone[w.Parent] {
				gone[w.ID], changed = true, true
			}
		}
	}
	kept := review.Walks[:0]
	for _, w := range review.Walks {
		if !gone[w.ID] {
			kept = append(kept, w)
		}
	}
	review.Walks = kept
}

// DeleteStepWalks deletes the walks into the steps of a parent, with the
// walks under them. They point at steps by index, so they go when the
// parent gets new steps. Questions about the parent as a whole stay.
func (review *Review) DeleteStepWalks(parent string) {
	for _, w := range slices.Clone(review.Walks) {
		if w.Parent == parent && w.Step >= 0 {
			review.DeleteWalk(w.ID)
		}
	}
}

// HasStepWalks reports whether a parent has walks into its steps.
func (review *Review) HasStepWalks(parent string) bool {
	for _, w := range review.Walks {
		if w.Parent == parent && w.Step >= 0 {
			return true
		}
	}
	return false
}

// Steps returns the steps of a parent: the overview's for "".
func (review *Review) Steps(parent string) []Step {
	if parent == "" {
		if review.Overview != nil && review.Overview.Walk != nil {
			return review.Overview.Walk.Steps
		}
		return nil
	}
	if walk := review.Walk(parent); walk != nil && walk.Content != nil {
		return walk.Content.Steps
	}
	return nil
}

// StepOf returns the step a walk goes deeper into, if it is about one.
func (review *Review) StepOf(walk *Walk) (Step, bool) {
	steps := review.Steps(walk.Parent)
	if walk.Step < 0 || walk.Step >= len(steps) {
		return Step{}, false
	}
	return steps[walk.Step], true
}

// WalkTitle is what a walk is about, in a few words.
func (review *Review) WalkTitle(walk *Walk) string {
	if walk.Question != "" {
		return walk.Question
	}
	if step, ok := review.StepOf(walk); ok {
		return step.Title
	}
	return "Deeper"
}

var jsonFence = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*\\})\\s*```")

// parseWalkthrough reads the walkthrough JSON out of an agent's answer:
// a fenced block, or the outermost braces. It reports false when the
// answer holds no walkthrough, so the caller can show it as Markdown.
func parseWalkthrough(answer string) (*Walkthrough, bool) {
	raw := ""
	if matches := jsonFence.FindAllStringSubmatch(answer, -1); len(matches) > 0 {
		raw = matches[len(matches)-1][1]
	} else if start, end := strings.Index(answer, "{"), strings.LastIndex(answer, "}"); start >= 0 && end > start {
		raw = answer[start : end+1]
	}
	var walkthrough Walkthrough
	if raw == "" || json.Unmarshal([]byte(raw), &walkthrough) != nil {
		return nil, false
	}
	walkthrough.Summary = strings.TrimSpace(walkthrough.Summary)
	walkthrough.Steps = cleanSteps(walkthrough.Steps)
	var sections []Section
	for _, s := range walkthrough.Sections {
		s.Title, s.Items = strings.TrimSpace(s.Title), cleanSteps(s.Items)
		if len(s.Items) > 0 {
			sections = append(sections, s)
		}
	}
	walkthrough.Sections = sections
	if walkthrough.Summary == "" && len(walkthrough.Steps) == 0 {
		return nil, false
	}
	return &walkthrough, true
}

// cleanSteps drops the steps without a title and a text, and cleans the
// references of the others.
func cleanSteps(steps []Step) []Step {
	var out []Step
	for _, s := range steps {
		s.Title, s.Text = strings.TrimSpace(s.Title), strings.TrimSpace(s.Text)
		if s.Title == "" && s.Text == "" {
			continue
		}
		s.Refs = cleanRefs(s.Refs)
		out = append(out, s)
	}
	return out
}

// maxRefStart is the highest start line a reference keeps. A larger one
// would overflow the end line, and no diff has that many lines.
const maxRefStart = 1 << 31

// cleanRefs drops references without a file or a valid line and cuts
// long ones to maxRefLines.
func cleanRefs(refs []Ref) []Ref {
	var out []Ref
	for _, r := range refs {
		r.File = strings.TrimPrefix(strings.TrimSpace(r.File), "./")
		if r.File == "" || r.Start < 1 || r.Start > maxRefStart {
			continue
		}
		if r.Side != SideOld {
			r.Side = SideNew
		}
		r.End = min(max(r.End, r.Start), r.Start+maxRefLines-1)
		r.Code = nil
		out = append(out, r)
	}
	return out
}

// Label is the reference as a reviewer reads it: path:start-end.
func (ref Ref) Label() string {
	return Anchor{Kind: AnchorLines, File: ref.File, Side: ref.Side, Start: ref.Start, End: ref.End}.Label()
}
