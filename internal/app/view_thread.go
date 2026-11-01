package app

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/psd-coder/agentdiff/internal/review"
)

func (app *app) threadPanel(ctx *ui.Context) {
	theme := ctx.Theme()
	th := app.rev.Thread(app.threadID())
	if th == nil {
		return
	}
	ui.Column(ctx).Fill().Background(theme.Background).BorderWidth(0, 0, 0, 1).BorderColor(theme.Border).Children(func() {
		ui.Row(ctx).Gap(8).Padding(8, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
			target, linked := overview, true
			switch {
			case th.Anchor.File != "":
				target = page{kindFile, th.Anchor.File}
			case th.Anchor.Kind != review.AnchorSection:
				linked = false
			}
			if !linked {
				ui.Text(ctx, th.Anchor.Label()).Bold().SingleLine().Shrink(1).Grow(1)
			} else {
				title := textLink(ctx, th.Anchor.Label(), func(e ui.Element) { e.Bold().TextColor(theme.Text) }).Grow(1).Justify(ui.Start).Tooltip("Go to it")
				if title.Clicked() {
					app.nav.goTo(target)
				}
			}
			app.cmdButton(ctx, "thread-log", th.ID, "Log")
			app.cmdButton(ctx, "close-thread", th.ID, "Close")
		})
		if th.Anchor.Quote != "" && th.Anchor.Kind == review.AnchorLines {
			ui.Text(ctx, strings.TrimRight(th.Anchor.Quote, "\n")).Font("monospace").FontSize(12).MaxLines(8).
				Padding(8, 12).Background(theme.Surface).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Selectable()
		}
		ui.Scroll(ctx).Grow(1).FillWidth().Children(func() {
			ui.Column(ctx).Gap(12).Padding(12).Children(func() {
				if len(th.Messages) == 0 {
					ui.Text(ctx, "Write a comment for the coding agent, or ask the review agent a question. Each thread is a separate conversation.").TextColor(theme.TextMuted)
				}
				run := app.runner.run(app.rev, th.ID)
				for i := range th.Messages {
					if run != nil && run.ex.Reply() == i {
						app.pendingReply(ctx, th, run)
					}
					app.message(ctx, th, i)
				}
				if run != nil && run.ex.Reply() == len(th.Messages) {
					app.pendingReply(ctx, th, run)
				}
			})
		})
		app.composer(ctx, th)
		ui.Row(ctx).Gap(12).Padding(6, 12).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(theme.Border).Children(func() {
			app.cmdCheckbox(ctx, "include-thread", "exclude-thread", th.ID, "In feedback")
			app.cmdCheckbox(ctx, "resolve-thread", "reopen-thread", th.ID, "Resolved")
			ui.Spacer(ctx)
			app.cmdButton(ctx, "delete-thread", th.ID, "Delete")
		})
	})
}

func (app *app) closeThread() {
	app.closeEmptyThread("")
	app.panel = nil
}

func messageHeader(ctx *ui.Context, who string, at time.Time) {
	theme := ctx.Theme()
	ui.Row(ctx).Gap(6).Children(func() {
		ui.Text(ctx, who).FontSize(11).Bold().TextColor(theme.TextMuted)
		if !at.IsZero() {
			ui.Text(ctx, at.Format("15:04")).FontSize(11).TextColor(theme.TextMuted)
		}
	})
}

// pendingReply is the answer the agent writes in the thread now.
func (app *app) pendingReply(ctx *ui.Context, th *review.Thread, run *agentRun) {
	theme := ctx.Theme()
	ui.Column(ctx.Key("pending")).Gap(4).Children(func() {
		messageHeader(ctx, th.Agent.Name(), run.started)
		ui.Row(ctx).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Spinner(ctx).Size(12, 12)
			ui.Text(ctx, run.activity).FontSize(12).TextColor(theme.TextMuted).SingleLine().Shrink(1)
		})
		if run.text != "" {
			markdown.Stream(ctx, run.text, &app.md)
		}
	})
}

func (app *app) message(ctx *ui.Context, th *review.Thread, index int) {
	theme := ctx.Theme()
	message := th.Messages[index]
	ui.Column(ctx.Key(index)).Gap(4).Children(func() {
		who := "You"
		if message.Role == review.RoleAgent {
			who = th.Agent.Name()
		}
		messageHeader(ctx, who, message.Time)
		if message.Role == review.RoleUser {
			ui.Text(ctx, message.Text).Selectable().Padding(8, 10).Radius(8).Background(theme.Surface)
			return
		}
		if message.Text != "" {
			markdown.View(ctx, message.Text, &app.md)
		}
		if message.Err != "" {
			errorText(ctx, message.Err)
		}
	})
}

func (app *app) composer(ctx *ui.Context, th *review.Thread) {
	theme := ctx.Theme()
	draft := app.drafts[th.ID]
	busy := app.busy(th.ID)
	ui.Column(ctx).Gap(8).Padding(10, 12).BorderWidth(1, 0, 0, 0).BorderColor(theme.Border).Children(func() {
		placeholder := "Comment, or question for the agent"
		if len(th.Messages) > 0 {
			placeholder = "Reply"
		}
		in := ui.TextArea(ctx.Key("draft-"+th.ID), &draft).Lines(3, 10).Placeholder(placeholder).AutoFocus().
			OnTextKey(func(k ui.TextKey) bool {
				if k.Key == ui.KeyEnter && k.Mods == ui.Cmd && !app.busy(th.ID) {
					app.ask(app.rev, th, k.Text())
					k.SetText("")
					app.drafts[th.ID] = ""
					return true
				}
				return false
			})
		if in.Changed() {
			app.drafts[th.ID] = draft
		}
		empty := strings.TrimSpace(draft) == ""
		ui.Row(ctx).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(ctx, "Cmd+Enter asks the agent").FontSize(11).TextColor(theme.TextMuted).Grow(1)
			app.cmdButton(ctx, "stop-thread", th.ID, "Stop")
			ui.Button(ctx, "Comment").Disabled(empty).Tooltip("Add to the feedback without asking the agent").OnClick(func() {
				app.comment(th, draft)
				app.drafts[th.ID] = ""
			})
			ui.PrimaryButton(ctx, "Ask agent").Disabled(empty || busy).OnClick(func() {
				app.ask(app.rev, th, draft)
				app.drafts[th.ID] = ""
			})
		})
	})
}

func (app *app) threadsPage(ctx *ui.Context) {
	theme := ctx.Theme()
	app.pageScroll(ctx, page{kind: kindThreads}).Grow(1).FillWidth().Children(func() {
		ui.Column(ctx).Padding(24, 32).Gap(10).MaxWidth(980).Children(func() {
			ui.Text(ctx, "Questions").FontSize(22).Bold()
			app.questionsList(ctx)
			ui.Text(ctx, "Comments").FontSize(22).Bold().Margin(16, 0, 0, 0)
			threadCount := 0
			for _, th := range app.rev.Threads {
				if len(th.Messages) > 0 {
					app.threadRow(ctx, th)
					threadCount++
				}
			}
			if threadCount == 0 {
				ui.Text(ctx, "No comments yet. Click + next to a line in a diff, select line numbers, or use Discuss on the overview.").TextColor(theme.TextMuted)
			}
		})
	})
}

func (app *app) threadRow(ctx *ui.Context, th *review.Thread) {
	theme := ctx.Theme()
	row := ui.ButtonBase(ctx.Key("row-"+th.ID)).Padding(10, 12).Gap(4).Radius(8).Border(1, theme.Border).Column().AlignItems(ui.Stretch)
	if row.Hovered() {
		row.Background(theme.SurfaceHover)
	}
	row.Children(func() {
		ui.Text(ctx, th.Anchor.Label()).Font("monospace").FontSize(12).TextColor(theme.Accent).SingleLine()
		first := ""
		if len(th.Messages) > 0 {
			first = th.Messages[0].Text
		}
		ui.Text(ctx, excerpt(first, 300)).MaxLines(2)
		ui.Row(ctx).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(ctx, app.threadMeta(th)).FontSize(11).TextColor(theme.TextMuted)
			targetMark(ctx, app.state(th.ID))
		})
	})
	if row.Clicked() {
		app.openThread(th.ID)
		if th.Anchor.File != "" {
			app.nav.goTo(page{kindFile, th.Anchor.File})
		}
	}
}
