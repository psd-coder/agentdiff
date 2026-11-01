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

func (app *app) openQuestion(id string) {
	app.closeEmptyThread("")
	app.panel = panelQuestion(id)
}

// askAbout opens the side panel for a new question about a step.
func (app *app) askAbout(parent string, step int) {
	app.closeEmptyThread("")
	app.panel = panelAsk{parent, step}
}

// closeQuestion closes the side panel while it shows a question or asks
// one.
func (app *app) closeQuestion() {
	switch app.panel.(type) {
	case panelQuestion, panelAsk:
		app.panel = nil
	}
}

// closeGoneQuestion closes the side panel when the walk it asks about,
// or the question it shows, was deleted.
func (app *app) closeGoneQuestion() {
	gone := func(id string) bool { return id != "" && app.rev.Walk(id) == nil }
	if target := app.asking(); target != nil && gone(target.parent) || gone(app.questionID()) {
		app.closeQuestion()
	}
}

// questionDraftKey names the unsent text of a question in drafts: a
// follow-up in the conversation of walk, or a new question about target
// in the review the window shows when walk is nil.
func (app *app) questionDraftKey(walk *review.Walk, target askTarget) string {
	if walk != nil {
		return "question:" + walk.ID
	}
	return fmt.Sprintf("ask:%s\x00%s:%s:%d", app.rev.Repo, app.rev.Scope.Key(), target.parent, target.step)
}

func (app *app) questionOpen() bool {
	id := app.questionID()
	return app.asking() != nil || (id != "" && app.rev.Walk(id) != nil)
}

// targetLabel names what a question is about.
func (app *app) targetLabel(parent string, step int) string {
	if steps := app.rev.Steps(parent); step >= 0 && step < len(steps) {
		return steps[step].Title
	}
	if walk := app.rev.Walk(parent); walk != nil {
		return app.rev.WalkTitle(walk)
	}
	return "the whole change set"
}

func (app *app) questionPanel(ctx *ui.Context) {
	theme := ctx.Theme()
	rev := app.rev
	id := app.questionID()
	walk := rev.Walk(id)
	target := app.asking()
	if walk != nil {
		target = &askTarget{walk.Parent, walk.Step}
	}
	ui.Column(ctx).Fill().Background(theme.Background).BorderWidth(0, 0, 0, 1).BorderColor(theme.Border).Children(func() {
		ui.Row(ctx).Gap(8).Padding(8, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
			title := "New question"
			if walk != nil {
				title = rev.WalkTitle(walk)
			}
			ui.Text(ctx, title).Bold().SingleLine().Shrink(1).Grow(1).Tooltip(title)
			if walk != nil {
				app.cmdButton(ctx, "question-log", walk.ID, "Log")
				app.cmdButton(ctx, "question-page", walk.ID, "Open as page")
			}
			app.cmdButton(ctx, "close-question", "", "Close")
		})
		ui.Text(ctx, "About: "+app.targetLabel(target.parent, target.step)).FontSize(12).TextColor(theme.TextMuted).
			SingleLine().Padding(6, 12).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border)
		ui.Scroll(ctx.Key("q-" + id)).TrackScroll(app.scrollOf("question:" + id)).Grow(1).FillWidth().Children(func() {
			ui.Column(ctx).Gap(14).Padding(12).Children(func() {
				if walk == nil {
					app.earlierQuestions(ctx, target)
					return
				}
				turns := walk.Turns()
				for i, turn := range turns {
					if i > 0 {
						ui.Divider(ctx.Key(fmt.Sprintf("d-%d", i)))
					}
					app.turnView(ctx.Key(i), walk, turn, i == len(turns)-1)
				}
			})
		})
		app.questionComposer(ctx, walk, target)
		if walk != nil {
			ui.Row(ctx).Gap(12).Padding(6, 12).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(theme.Border).Children(func() {
				ui.Text(ctx, writtenBy(walk.Agent, walk.Model, walk.Generated.Format("Jan 2 15:04"))).FontSize(11).TextColor(theme.TextMuted).Grow(1)
				app.cmdButton(ctx, "delete-question", walk.ID, "Delete")
			})
		}
	})
}

// earlierQuestions lists the questions asked before about the same
// thing, for a new question.
func (app *app) earlierQuestions(ctx *ui.Context, target *askTarget) {
	theme := ctx.Theme()
	var earlier []*review.Walk
	for _, w := range app.rev.WalksOn(target.parent, target.step) {
		if w.Question != "" {
			earlier = append(earlier, w)
		}
	}
	if len(earlier) == 0 {
		ui.Text(ctx, "Ask the review agent anything about this. The answer is a short walkthrough with the code that shows it, and you can ask follow-up questions in the same conversation.").TextColor(theme.TextMuted)
		return
	}
	ui.Text(ctx, "Asked before").FontSize(12).Bold().TextColor(theme.TextMuted)
	for _, w := range earlier {
		app.walkRow(ctx, w)
	}
}

func (app *app) turnView(ctx *ui.Context, walk *review.Walk, turn review.Turn, last bool) {
	theme := ctx.Theme()
	ui.Column(ctx).Gap(8).Children(func() {
		ui.Text(ctx, turn.Question).Selectable().Padding(8, 10).Radius(8).Background(theme.Surface)
		if run := app.runner.run(app.rev, walk.ID); run != nil && last {
			ui.Row(ctx).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Spinner(ctx).Size(12, 12)
				ui.Text(ctx, run.activity).FontSize(12).TextColor(theme.TextMuted).SingleLine().Shrink(1)
			})
			return
		}
		app.answerView(ctx, turn)
	})
}

func (app *app) answerView(ctx *ui.Context, turn review.Turn) {
	switch {
	case turn.Content != nil:
		app.compactWalk(ctx, turn.Content)
	case turn.Markdown != "":
		markdown.View(ctx, turn.Markdown, &app.md)
	}
	if turn.Err != "" {
		errorText(ctx, turn.Err)
	}
}

// compactWalk shows a walkthrough in a narrow place: its steps and its
// sections without the buttons that lead deeper.
func (app *app) compactWalk(ctx *ui.Context, walkthrough *review.Walkthrough) {
	if walkthrough.Summary != "" {
		ui.Text(ctx, walkthrough.Summary).Selectable()
	}
	for i, s := range walkthrough.Steps {
		app.compactStep(ctx.Key(fmt.Sprintf("cs-%d", i)), fmt.Sprintf("%d. %s", i+1, s.Title), s)
	}
	for si, sec := range walkthrough.Sections {
		if sec.Title != "" {
			ui.Text(ctx.Key(fmt.Sprintf("cp-%d", si)), sec.Title).FontSize(15).Bold()
		}
		for i, it := range sec.Items {
			app.compactStep(ctx.Key(fmt.Sprintf("cp-%d-%d", si, i)), it.Title, it)
		}
	}
}

func (app *app) compactStep(ctx *ui.Context, title string, step review.Step) {
	ui.Column(ctx).Gap(6).Children(func() {
		if title != "" {
			ui.Text(ctx, title).Bold()
		}
		if step.Text != "" {
			markdown.View(ctx, step.Text, &app.md)
		}
		for ri, r := range step.Refs {
			app.refView(ctx.Key(fmt.Sprintf("ref-%d", ri)), r)
		}
	})
}

func (app *app) questionComposer(ctx *ui.Context, walk *review.Walk, target *askTarget) {
	theme := ctx.Theme()
	busy := walk != nil && app.busy(walk.ID)
	key := app.questionDraftKey(walk, *target)
	draft := app.drafts[key]
	send := func(text string) {
		if strings.TrimSpace(text) == "" || busy {
			return
		}
		app.drafts[key] = ""
		if walk == nil {
			app.askQuestion(target.parent, target.step, text)
			return
		}
		app.askMore(app.rev, walk, text)
	}
	ui.Column(ctx).Gap(8).Padding(10, 12).BorderWidth(1, 0, 0, 0).BorderColor(theme.Border).Children(func() {
		placeholder := "What do you want to know?"
		if walk != nil {
			placeholder = "Follow-up question"
		}
		in := ui.TextArea(ctx.Key("pane-"+key), &draft).Lines(2, 8).Placeholder(placeholder).AutoFocus().
			OnTextKey(func(k ui.TextKey) bool {
				if k.Key == ui.KeyEnter && k.Mods == ui.Cmd {
					send(k.Text())
					k.SetText("")
					return true
				}
				return false
			})
		if in.Changed() {
			app.drafts[key] = draft
		}
		ui.Row(ctx).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(ctx, "Cmd+Enter asks").FontSize(11).TextColor(theme.TextMuted).Grow(1)
			if busy {
				ui.Button(ctx, "Stop").OnClick(func() { app.runner.stop(app.rev, walk.ID) })
			}
			ui.PrimaryButton(ctx, "Ask").Disabled(strings.TrimSpace(draft) == "" || busy).OnClick(func() { send(draft) })
		})
	})
}

// questionsList lists every question of the review, for the Comments
// page.
func (app *app) questionsList(ctx *ui.Context) {
	theme := ctx.Theme()
	questionCount := 0
	for _, w := range app.rev.Walks {
		if w.Question == "" {
			continue
		}
		questionCount++
		row := ui.ButtonBase(ctx.Key("q-"+w.ID)).Padding(10, 12).Gap(4).Radius(8).Border(1, theme.Border).Column().AlignItems(ui.Stretch)
		if row.Hovered() {
			row.Background(theme.SurfaceHover)
		}
		row.Children(func() {
			ui.Text(ctx, "About: "+app.targetLabel(w.Parent, w.Step)).FontSize(12).TextColor(theme.Accent).SingleLine()
			ui.Text(ctx, w.Question).MaxLines(2)
			meta := w.Generated.Format("Jan 2 15:04")
			if len(w.More) > 0 {
				meta += fmt.Sprintf(", %d follow-ups", len(w.More))
			}
			ui.Text(ctx, meta).FontSize(11).TextColor(theme.TextMuted)
		})
		if row.Clicked() {
			app.openQuestion(w.ID)
		}
	}
	if questionCount == 0 {
		ui.Text(ctx, "No questions yet. Use Ask about this step, or the question box under a walkthrough.").TextColor(theme.TextMuted)
	}
}
