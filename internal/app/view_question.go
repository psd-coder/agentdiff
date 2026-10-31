package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/psd-coder/agentdiff/internal/review"
)

// askTarget is what a new question in the side panel is about: a step of
// parent, or parent as a whole for step -1.
type askTarget struct {
	parent string
	step   int
}

func (a *app) openQuestion(id string) {
	a.closeEmptyThread("")
	a.thread, a.question, a.asking = "", id, nil
}

// askAbout opens the side panel for a new question about a step.
func (a *app) askAbout(parent string, step int) {
	a.closeEmptyThread("")
	a.thread, a.question, a.asking = "", "", &askTarget{parent, step}
}

func (a *app) closeQuestion() { a.question, a.asking = "", nil }

// closeGoneQuestion closes the side panel when the walk it asks about,
// or the question it shows, was deleted.
func (a *app) closeGoneQuestion() {
	gone := func(id string) bool { return id != "" && a.rev.Walk(id) == nil }
	if a.asking != nil && gone(a.asking.parent) || gone(a.question) {
		a.closeQuestion()
	}
}

// questionDraftKey names the unsent text of a question in drafts: a
// follow-up in the conversation of walk, or a new question about target
// in the review the window shows when walk is nil.
func (a *app) questionDraftKey(w *review.Walk, target askTarget) string {
	if w != nil {
		return "question:" + w.ID
	}
	return fmt.Sprintf("ask:%s:%s:%d", reviewKey(a.rev.Repo, a.rev.Scope), target.parent, target.step)
}

func (a *app) questionOpen() bool {
	return a.asking != nil || (a.question != "" && a.rev.Walk(a.question) != nil)
}

// targetLabel names what a question is about.
func (a *app) targetLabel(parent string, step int) string {
	if steps := a.rev.Steps(parent); step >= 0 && step < len(steps) {
		return steps[step].Title
	}
	if w := a.rev.Walk(parent); w != nil {
		return a.rev.WalkTitle(w)
	}
	return "the whole change set"
}

func (a *app) questionPanel(c *ui.Context) {
	t := c.Theme()
	rev := a.rev
	w := rev.Walk(a.question)
	target := a.asking
	if w != nil {
		target = &askTarget{w.Parent, w.Step}
	}
	ui.Column(c).Fill().Background(t.Background).BorderWidth(0, 0, 0, 1).BorderColor(t.Border).Children(func() {
		ui.Row(c).Gap(8).Padding(8, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			title := "New question"
			if w != nil {
				title = rev.WalkTitle(w)
			}
			ui.Text(c, title).Bold().SingleLine().Shrink(1).Grow(1).Tooltip(title)
			if w != nil {
				a.cmdButton(c, "question-log", w.ID, "Log")
				a.cmdButton(c, "question-page", w.ID, "Open as page")
			}
			a.cmdButton(c, "close-question", "", "Close")
		})
		ui.Text(c, "About: "+a.targetLabel(target.parent, target.step)).FontSize(12).TextColor(t.TextMuted).
			SingleLine().Padding(6, 12).BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
		ui.Scroll(c.Key("q-" + a.question)).TrackScroll(a.scrollOf("question:" + a.question)).Grow(1).FillWidth().Children(func() {
			ui.Column(c).Gap(14).Padding(12).Children(func() {
				if w == nil {
					a.earlierQuestions(c, target)
					return
				}
				turns := w.Turns()
				for i, turn := range turns {
					if i > 0 {
						ui.Divider(c.Key(fmt.Sprintf("d-%d", i)))
					}
					a.turnView(c.Key(i), w, turn, i == len(turns)-1)
				}
			})
		})
		a.questionComposer(c, w, target)
		if w != nil {
			ui.Row(c).Gap(12).Padding(6, 12).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
				ui.Text(c, writtenBy(w.Agent, w.Model, w.Generated.Format("Jan 2 15:04"))).FontSize(11).TextColor(t.TextMuted).Grow(1)
				a.cmdButton(c, "delete-question", w.ID, "Delete")
			})
		}
	})
}

// earlierQuestions lists the questions asked before about the same
// thing, for a new question.
func (a *app) earlierQuestions(c *ui.Context, target *askTarget) {
	t := c.Theme()
	var earlier []*review.Walk
	for _, w := range a.rev.WalksOn(target.parent, target.step) {
		if w.Question != "" {
			earlier = append(earlier, w)
		}
	}
	if len(earlier) == 0 {
		ui.Text(c, "Ask the review agent anything about this. The answer is a short walkthrough with the code that shows it, and you can ask follow-up questions in the same conversation.").TextColor(t.TextMuted)
		return
	}
	ui.Text(c, "Asked before").FontSize(12).Bold().TextColor(t.TextMuted)
	for _, w := range earlier {
		a.walkRow(c, w)
	}
}

func (a *app) turnView(c *ui.Context, w *review.Walk, turn review.Turn, last bool) {
	t := c.Theme()
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, turn.Question).Selectable().Padding(8, 10).Radius(8).Background(t.Surface)
		if run := a.walkRuns[w.ID]; run != nil && last {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(12, 12)
				ui.Text(c, run.activity).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(1)
			})
			return
		}
		a.answerView(c, turn)
	})
}

func (a *app) answerView(c *ui.Context, turn review.Turn) {
	t := c.Theme()
	switch {
	case turn.Content != nil:
		a.compactWalk(c, turn.Content)
	case turn.Markdown != "":
		markdown.View(c, turn.Markdown, &a.md)
	}
	if turn.Err != "" {
		ui.Text(c, turn.Err).FontSize(12).TextColor(t.Danger).Selectable()
	}
}

// compactWalk shows a walkthrough in a narrow place: its steps without
// the buttons that lead deeper.
func (a *app) compactWalk(c *ui.Context, w *review.Walkthrough) {
	if w.Summary != "" {
		ui.Text(c, w.Summary).Selectable()
	}
	for i, s := range w.Steps {
		ui.Column(c.Key(fmt.Sprintf("cs-%d", i))).Gap(6).Children(func() {
			ui.Text(c, fmt.Sprintf("%d. %s", i+1, s.Title)).Bold()
			if s.Text != "" {
				markdown.View(c, s.Text, &a.md)
			}
			for ri, r := range s.Refs {
				a.refView(c.Key(fmt.Sprintf("ref-%d", ri)), r)
			}
		})
	}
}

func (a *app) questionComposer(c *ui.Context, w *review.Walk, target *askTarget) {
	t := c.Theme()
	busy := w != nil && a.walkBusy(w.ID)
	key := a.questionDraftKey(w, *target)
	draft := a.drafts[key]
	send := func(text string) {
		if strings.TrimSpace(text) == "" || busy {
			return
		}
		a.drafts[key] = ""
		if w == nil {
			a.askQuestion(target.parent, target.step, text)
			return
		}
		a.askMore(a.rev, w, text)
	}
	ui.Column(c).Gap(8).Padding(10, 12).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		placeholder := "What do you want to know?"
		if w != nil {
			placeholder = "Follow-up question"
		}
		in := ui.TextArea(c.Key("pane-"+key), &draft).Lines(2, 8).Placeholder(placeholder).AutoFocus().
			OnTextKey(func(k ui.TextKey) bool {
				if k.Key == ui.KeyEnter && k.Mods == ui.Cmd {
					send(k.Text())
					k.SetText("")
					return true
				}
				return false
			})
		if in.Changed() {
			a.drafts[key] = draft
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Cmd+Enter asks").FontSize(11).TextColor(t.TextMuted).Grow(1)
			if busy {
				ui.Button(c, "Stop").OnClick(func() { a.stopWalk(w.ID) })
			}
			ui.PrimaryButton(c, "Ask").Disabled(strings.TrimSpace(draft) == "" || busy).OnClick(func() { send(draft) })
		})
	})
}

// questionsList lists every question of the review, for the Comments
// page.
func (a *app) questionsList(c *ui.Context) {
	t := c.Theme()
	n := 0
	for _, w := range a.rev.Walks {
		if w.Question == "" {
			continue
		}
		n++
		row := ui.ButtonBase(c.Key("q-"+w.ID)).Padding(10, 12).Gap(4).Radius(8).Border(1, t.Border).Column().AlignItems(ui.Stretch)
		if row.Hovered() {
			row.Background(t.SurfaceHover)
		}
		row.Children(func() {
			ui.Text(c, "About: "+a.targetLabel(w.Parent, w.Step)).FontSize(12).TextColor(t.Accent).SingleLine()
			ui.Text(c, w.Question).MaxLines(2)
			meta := w.Generated.Format("Jan 2 15:04")
			if len(w.More) > 0 {
				meta += fmt.Sprintf(", %d follow-ups", len(w.More))
			}
			ui.Text(c, meta).FontSize(11).TextColor(t.TextMuted)
		})
		if row.Clicked() {
			a.openQuestion(w.ID)
		}
	}
	if n == 0 {
		ui.Text(c, "No questions yet. Use Ask about this step, or the question box under a walkthrough.").TextColor(t.TextMuted)
	}
}
