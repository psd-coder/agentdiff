package app

import (
	"strings"

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
	panelFrame(ctx).Children(func() {
		panelBar(ctx).Children(func() {
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
			codeText(ctx, strings.TrimRight(th.Anchor.Quote, "\n")).MaxLines(8).
				Padding(theme.Space(2), theme.Space(3)).Background(theme.Surface).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Selectable()
		}
		ui.Scroll(ctx).Grow(1).FillWidth().Children(func() {
			ui.Column(ctx).Gap(theme.Space(3)).Padding(theme.Space(3)).Children(func() {
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
		app.threadComposer(ctx, th)
		panelFooter(ctx).Children(func() {
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

// pendingReply is the answer the agent writes in the thread now.
func (app *app) pendingReply(ctx *ui.Context, th *review.Thread, run *agentRun) {
	theme := ctx.Theme()
	ui.Column(ctx.Key("pending")).Gap(theme.Space(1)).Children(func() {
		messageHeader(ctx, th.Agent.Name(), run.started)
		activityLine(ctx, run.activity)
		if run.text != "" {
			markdown.Stream(ctx, run.text, &app.md)
		}
	})
}

func (app *app) message(ctx *ui.Context, th *review.Thread, index int) {
	theme := ctx.Theme()
	message := th.Messages[index]
	ui.Column(ctx.Key(index)).Gap(theme.Space(1)).Children(func() {
		who := "You"
		if message.Role == review.RoleAgent {
			who = th.Agent.Name()
		}
		messageHeader(ctx, who, message.Time)
		if message.Role == review.RoleUser {
			bubble(ctx, message.Text)
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

func (app *app) threadComposer(ctx *ui.Context, th *review.Thread) {
	busy := app.busy(th.ID)
	placeholder := "Comment, or question for the agent"
	if len(th.Messages) > 0 {
		placeholder = "Reply"
	}
	send := func(text string) bool {
		if app.busy(th.ID) {
			return false
		}
		app.ask(app.rev, th, text)
		return true
	}
	app.composer(ctx, th.ID, placeholder, send, func(draft string, submit func()) {
		empty := strings.TrimSpace(draft) == ""
		app.cmdButton(ctx, "stop-thread", th.ID, "Stop")
		ui.Button(ctx, "Comment").Disabled(empty).Tooltip("Add to the feedback without asking the agent").OnClick(func() {
			app.comment(th, draft)
			app.drafts[th.ID] = ""
		})
		ui.PrimaryButton(ctx, "Ask agent").Disabled(empty || busy).Tooltip("Send to the review agent (" + app.platform.keyLabel("CmdOrCtrl+Enter") + ")").OnClick(submit)
	})
}

func (app *app) threadsPage(ctx *ui.Context) {
	theme := ctx.Theme()
	app.pageScroll(ctx, page{kind: kindThreads}).Grow(1).FillWidth().Children(func() {
		pageColumn(ctx).Gap(theme.Space(2.5)).Children(func() {
			pageTitle(ctx, "Questions")
			app.questionsList(ctx)
			pageTitle(ctx, "Comments").Margin(theme.Space(4), 0, 0, 0)
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
	row := linkCard(ctx.Key("row-" + th.ID))
	row.ContextMenu(func(m *ui.Menu) {
		app.cmdMenu(ctx, m, th.ID, "resolve-thread", "reopen-thread", "include-thread", "exclude-thread",
			"", "stop-thread", "thread-log", "", "delete-thread")
	})
	row.Children(func() {
		anchorLabel(ctx, th.Anchor.Label())
		ui.Text(ctx, excerpt(firstMessage(th), 300)).MaxLines(2)
		ui.Row(ctx).Gap(theme.Space(2)).AlignItems(ui.Center).Children(func() {
			smallCaption(ctx, app.threadMeta(th))
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
