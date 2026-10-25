package main

import (
	"fmt"
	"path"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/psdcoder/agentdiff/internal/review"
)

func (a *app) overviewPage(c *ui.Context) {
	t := c.Theme()
	cfg, _ := a.cfg.Effective(a.rev.Settings)
	run := a.overviewRunning()
	ov := a.rev.Overview
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).Padding(24, 32).Gap(16).MaxWidth(980).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Overview").FontSize(22).Bold()
				ui.Spacer(c)
				switch {
				case run != nil:
					ui.Button(c, "Stop").OnClick(a.stopOverview)
				case ov != nil && ov.Markdown != "":
					ui.Button(c, "Regenerate").Disabled(len(a.files) == 0).OnClick(a.generateOverview)
				}
				if ov != nil || len(a.rev.Threads) > 0 {
					ui.Button(c, "Start over").Tooltip("Delete the overview and comments of this scope").OnClick(a.confirmStartOver)
				}
			})
			a.summaryLine(c)
			if run == nil && !a.loading && ov.Stale(a.fileNames()) {
				ui.Text(c, "The changed files differ from the ones this overview was written for. Regenerate it, or start over.").
					Padding(10, 14).Radius(8).Background(t.Warning.Alpha(0.15)).Border(1, t.Warning)
			}
			switch {
			case a.loading:
				ui.Row(c).Gap(8).Children(func() {
					ui.Spinner(c).Size(14, 14)
					ui.Text(c, "Reading changes…").TextColor(t.TextMuted)
				})
			case len(a.files) == 0:
				a.emptyState(c, "No changes", "There is nothing to review in "+strings.ToLower(a.scope.Label())+". Pick another scope on the left.")
			case run != nil:
				a.streamingOverview(c, run)
			case ov == nil || ov.Markdown == "":
				a.overviewIntro(c, providerName(cfg.Provider))
			default:
				a.overviewSections(c, ov)
			}
			if ov != nil && ov.Err != "" && run == nil {
				ui.Column(c).Gap(4).Padding(12).Radius(8).Border(1, t.Danger).Children(func() {
					ui.Text(c, "The agent failed").Bold().TextColor(t.Danger)
					ui.Text(c, ov.Err).FontSize(12).Font("monospace").Selectable()
				})
			}
			if len(a.files) > 0 && !a.loading {
				a.groupCards(c)
				a.askBox(c)
			}
		})
	})
}

func (a *app) summaryLine(c *ui.Context) {
	t := c.Theme()
	if len(a.files) == 0 {
		return
	}
	add, del := 0, 0
	for _, f := range a.files {
		add += f.Additions
		del += f.Deletions
	}
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, fmt.Sprintf("%d files", len(a.files))).TextColor(t.TextMuted)
		ui.Text(c, fmt.Sprintf("+%d", add)).TextColor(t.Success)
		ui.Text(c, fmt.Sprintf("−%d", del)).TextColor(t.Danger)
		viewed := 0
		for _, f := range a.files {
			if a.rev.Viewed[f.Path] {
				viewed++
			}
		}
		ui.Text(c, fmt.Sprintf("%d of %d viewed", viewed, len(a.files))).TextColor(t.TextMuted)
	})
}

func (a *app) overviewIntro(c *ui.Context, agentName string) {
	t := c.Theme()
	ui.Column(c).Gap(12).Padding(20).Radius(10).Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Text(c, "Get an explanation of these changes").FontSize(16).Bold()
		ui.Text(c, agentName+" reads the changes in read-only mode and answers your review questions: what changed on a high level, data, UI, architecture and API, and the parts that are not pretty, with the reasons. It also groups the files so that you can review them in a sensible order.").TextColor(t.TextMuted)
		ui.Text(c, "It runs with your own subscription, through the CLI on this Mac. Change the agent and the questions in Settings.").FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).Children(func() {
			ui.PrimaryButton(c, "Explain changes with "+agentName).OnClick(a.generateOverview)
			ui.Button(c, "Settings").OnClick(a.openSettings)
		})
	})
}

func (a *app) streamingOverview(c *ui.Context, run *overviewRun) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Spinner(c).Size(14, 14)
		activity := run.activity
		if activity == "" {
			activity = "Writing…"
		}
		ui.Text(c, activity).FontSize(12).TextColor(t.TextMuted).SingleLine()
	})
	if run.text != "" {
		ui.Column(c).Children(func() { markdown.Stream(c, run.text, &a.md) })
	}
}

func (a *app) overviewSections(c *ui.Context, ov *review.Overview) {
	t := c.Theme()
	meta := fmt.Sprintf("By %s", providerName(ov.Agent))
	if ov.Model != "" {
		meta += " (" + ov.Model + ")"
	}
	meta += ", " + ov.Generated.Format("Jan 2 15:04")
	ui.Text(c, meta).FontSize(12).TextColor(t.TextMuted)
	for i, s := range review.Sections(ov.Markdown) {
		ui.Column(c.Key(fmt.Sprintf("section-%d-%s", i, s.Title))).Gap(8).Padding(16, 20).Radius(10).Background(t.Surface).Border(1, t.Border).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				title := s.Title
				if title == "" {
					title = "Summary"
				}
				ui.Text(c, title).FontSize(16).Bold().Grow(1)
				a.sectionThreads(c, title, s.Body)
			})
			markdown.View(c, s.Body, &a.md)
		})
	}
}

func (a *app) sectionThreads(c *ui.Context, title, body string) {
	threads := a.rev.ThreadsOnSection(title)
	for _, th := range threads {
		label := "Thread"
		if th.Busy() {
			label = "Answering…"
		} else if len(th.Messages) > 0 {
			label = excerpt(th.Messages[0].Text, 24)
		}
		ui.Button(c.Key("t-"+th.ID), label).Tooltip("Open the thread").OnClick(func() { a.openThread(th.ID) })
	}
	ui.Button(c.Key("discuss-"+title), "Discuss").Tooltip("Start a thread about this part").OnClick(func() {
		a.newThread(review.Anchor{Kind: review.AnchorSection, Section: title, Quote: body})
	})
}

func (a *app) groupCards(c *ui.Context) {
	t := c.Theme()
	groups := a.groups()
	if len(groups) == 0 {
		return
	}
	title := "Files by directory"
	if a.rev.Overview != nil && len(a.rev.Overview.Groups) > 0 {
		title = "Review order"
	}
	ui.Text(c, title).FontSize(18).Bold().Margin(8, 0, 0, 0)
	for gi, g := range groups {
		ui.Column(c.Key(fmt.Sprintf("group-%d", gi))).Gap(6).Padding(14, 18).Radius(10).Border(1, t.Border).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, fmt.Sprintf("%d. %s", gi+1, g.Title)).Bold().Grow(1)
				ui.Button(c, "Review group").OnClick(func() { a.page = fmt.Sprintf("group:%d", gi) })
			})
			if g.Summary != "" {
				ui.Text(c, g.Summary).TextColor(t.TextMuted)
			}
			for _, f := range g.Files {
				d := a.diffs[f]
				ui.Row(c.Key(f)).Gap(8).AlignItems(ui.Center).Children(func() {
					mark := " "
					if a.rev.Viewed[f] {
						mark = "✓"
					}
					ui.Text(c, mark).Width(14).TextColor(t.Success)
					link := ui.Text(c, f).Font("monospace").FontSize(12).TextColor(t.Accent).Cursor(ui.CursorPointer).SingleLine().Shrink(1)
					if link.Clicked() {
						a.page = "file:" + f
					}
					if d != nil {
						ui.Text(c, fmt.Sprintf("+%d −%d", d.change.Additions, d.change.Deletions)).FontSize(11).TextColor(t.TextMuted)
					}
				})
			}
		})
	}
}

func (a *app) askBox(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Gap(8).Margin(8, 0, 0, 0).Children(func() {
		ui.Text(c, "Ask about the changes").FontSize(18).Bold()
		ui.Text(c, "Each question starts its own thread, with what the agent learned while it wrote the overview.").FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).AlignItems(ui.End).Children(func() {
			ui.TextArea(c, &a.askDraft).Lines(2, 6).Placeholder("Why did the agent change the cache key?").Grow(1).
				OnTextKey(func(k ui.TextKey) bool {
					if k.Key == ui.KeyEnter && k.Mods == ui.Cmd {
						a.askGeneral(k.Text())
						k.SetText("")
						return true
					}
					return false
				})
			ui.PrimaryButton(c, "Ask").Disabled(strings.TrimSpace(a.askDraft) == "").Tooltip("Cmd+Enter").OnClick(func() {
				a.askGeneral(a.askDraft)
				a.askDraft = ""
			})
		})
	})
	general := 0
	for _, th := range a.rev.Threads {
		if th.Anchor.Kind == review.AnchorGeneral {
			general++
		}
	}
	if general == 0 {
		return
	}
	ui.Column(c).Gap(6).Children(func() {
		for _, th := range a.rev.Threads {
			if th.Anchor.Kind == review.AnchorGeneral && len(th.Messages) > 0 {
				a.threadRow(c, th)
			}
		}
	})
}

func (a *app) askGeneral(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	th := a.rev.AddThread(review.Anchor{Kind: review.AnchorGeneral})
	a.openThread(th.ID)
	a.ask(th, text)
}

func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func fileName(p string) string { return path.Base(p) }
