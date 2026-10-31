package app

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/psd-coder/agentdiff/internal/review"
)

func (a *app) threadPanel(c *ui.Context) {
	t := c.Theme()
	th := a.rev.Thread(a.thread)
	if th == nil {
		return
	}
	ui.Column(c).Fill().Background(t.Background).BorderWidth(0, 0, 0, 1).BorderColor(t.Border).Children(func() {
		ui.Row(c).Gap(8).Padding(8, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			target := ""
			switch {
			case th.Anchor.File != "":
				target = "file:" + th.Anchor.File
			case th.Anchor.Kind == review.AnchorSection:
				target = "overview"
			}
			if target == "" {
				ui.Text(c, th.Anchor.Label()).Bold().SingleLine().Shrink(1).Grow(1)
			} else {
				title := textLink(c, th.Anchor.Label(), func(e ui.Element) { e.Bold().TextColor(t.Text) }).Grow(1).Justify(ui.Start).Tooltip("Go to it")
				if title.Clicked() {
					a.goTo(target)
				}
			}
			a.cmdButton(c, "thread-log", th.ID, "Log")
			a.cmdButton(c, "close-thread", th.ID, "Close")
		})
		if th.Anchor.Quote != "" && th.Anchor.Kind == review.AnchorLines {
			ui.Text(c, strings.TrimRight(th.Anchor.Quote, "\n")).Font("monospace").FontSize(12).MaxLines(8).
				Padding(8, 12).Background(t.Surface).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Selectable()
		}
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			ui.Column(c).Gap(12).Padding(12).Children(func() {
				if len(th.Messages) == 0 {
					ui.Text(c, "Write a comment for the coding agent, or ask the review agent a question. Each thread is a separate conversation.").TextColor(t.TextMuted)
				}
				run := a.threadRuns[th.ID]
				for i := range th.Messages {
					if run != nil && run.reply == i {
						a.pendingReply(c, th, run)
					}
					a.message(c, th, i)
				}
				if run != nil && run.reply == len(th.Messages) {
					a.pendingReply(c, th, run)
				}
			})
		})
		a.composer(c, th)
		ui.Row(c).Gap(12).Padding(6, 12).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
			a.cmdCheckbox(c, "include-thread", "exclude-thread", th.ID, "In feedback")
			a.cmdCheckbox(c, "resolve-thread", "reopen-thread", th.ID, "Resolved")
			ui.Spacer(c)
			a.cmdButton(c, "delete-thread", th.ID, "Delete")
		})
	})
}

func (a *app) closeThread() {
	a.closeEmptyThread("")
	a.thread = ""
}

func messageHeader(c *ui.Context, who string, at time.Time) {
	t := c.Theme()
	ui.Row(c).Gap(6).Children(func() {
		ui.Text(c, who).FontSize(11).Bold().TextColor(t.TextMuted)
		if !at.IsZero() {
			ui.Text(c, at.Format("15:04")).FontSize(11).TextColor(t.TextMuted)
		}
	})
}

// pendingReply is the answer the agent writes in the thread now.
func (a *app) pendingReply(c *ui.Context, th *review.Thread, run *threadRun) {
	t := c.Theme()
	ui.Column(c.Key("pending")).Gap(4).Children(func() {
		messageHeader(c, providerName(th.Agent), run.started)
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(12, 12)
			ui.Text(c, run.activity).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(1)
		})
		if run.text != "" {
			markdown.Stream(c, run.text, &a.md)
		}
	})
}

func (a *app) message(c *ui.Context, th *review.Thread, i int) {
	t := c.Theme()
	m := th.Messages[i]
	ui.Column(c.Key(i)).Gap(4).Children(func() {
		who := "You"
		if m.Role == review.RoleAgent {
			who = providerName(th.Agent)
		}
		messageHeader(c, who, m.Time)
		if m.Role == review.RoleUser {
			ui.Text(c, m.Text).Selectable().Padding(8, 10).Radius(8).Background(t.Surface)
			return
		}
		if m.Text != "" {
			markdown.View(c, m.Text, &a.md)
		}
		if m.Err != "" {
			ui.Text(c, m.Err).FontSize(12).TextColor(t.Danger).Selectable()
		}
	})
}

func (a *app) composer(c *ui.Context, th *review.Thread) {
	t := c.Theme()
	draft := a.drafts[th.ID]
	busy := a.threadBusy(th.ID)
	ui.Column(c).Gap(8).Padding(10, 12).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		placeholder := "Comment, or question for the agent"
		if len(th.Messages) > 0 {
			placeholder = "Reply"
		}
		in := ui.TextArea(c.Key("draft-"+th.ID), &draft).Lines(3, 10).Placeholder(placeholder).AutoFocus().
			OnTextKey(func(k ui.TextKey) bool {
				if k.Key == ui.KeyEnter && k.Mods == ui.Cmd && !a.threadBusy(th.ID) {
					a.ask(a.rev, th, k.Text())
					k.SetText("")
					a.drafts[th.ID] = ""
					return true
				}
				return false
			})
		if in.Changed() {
			a.drafts[th.ID] = draft
		}
		empty := strings.TrimSpace(draft) == ""
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Cmd+Enter asks the agent").FontSize(11).TextColor(t.TextMuted).Grow(1)
			a.cmdButton(c, "stop-thread", th.ID, "Stop")
			ui.Button(c, "Comment").Disabled(empty).Tooltip("Add to the feedback without asking the agent").OnClick(func() {
				a.comment(th, draft)
				a.drafts[th.ID] = ""
			})
			ui.PrimaryButton(c, "Ask agent").Disabled(empty || busy).OnClick(func() {
				a.ask(a.rev, th, draft)
				a.drafts[th.ID] = ""
			})
		})
	})
}

func (a *app) threadsPage(c *ui.Context) {
	t := c.Theme()
	a.pageScroll(c, "threads").Grow(1).FillWidth().Children(func() {
		ui.Column(c).Padding(24, 32).Gap(10).MaxWidth(980).Children(func() {
			ui.Text(c, "Questions").FontSize(22).Bold()
			a.questionsList(c)
			ui.Text(c, "Comments").FontSize(22).Bold().Margin(16, 0, 0, 0)
			n := 0
			for _, th := range a.rev.Threads {
				if len(th.Messages) > 0 {
					a.threadRow(c, th)
					n++
				}
			}
			if n == 0 {
				ui.Text(c, "No comments yet. Click + next to a line in a diff, select line numbers, or use Discuss on the overview.").TextColor(t.TextMuted)
			}
		})
	})
}

func (a *app) threadRow(c *ui.Context, th *review.Thread) {
	t := c.Theme()
	row := ui.ButtonBase(c.Key("row-"+th.ID)).Padding(10, 12).Gap(4).Radius(8).Border(1, t.Border).Column().AlignItems(ui.Stretch)
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		ui.Text(c, th.Anchor.Label()).Font("monospace").FontSize(12).TextColor(t.Accent).SingleLine()
		first := ""
		if len(th.Messages) > 0 {
			first = th.Messages[0].Text
		}
		ui.Text(c, excerpt(first, 300)).MaxLines(2)
		ui.Text(c, a.threadMeta(th)).FontSize(11).TextColor(t.TextMuted)
	})
	if row.Clicked() {
		a.openThread(th.ID)
		if th.Anchor.File != "" {
			a.goTo("file:" + th.Anchor.File)
		}
	}
}
