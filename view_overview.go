package main

import (
	"fmt"
	"path"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/psdcoder/agentdiff/internal/gitx"
	"github.com/psdcoder/agentdiff/internal/review"
	"github.com/psdcoder/agentdiff/internal/store"
)

// staleBanner warns when the review was written for other changes, and
// names the commit that holds those changes now.
func (a *app) staleBanner(c *ui.Context) {
	if !a.stale() {
		return
	}
	t := c.Theme()
	ui.Column(c).Gap(10).Padding(10, 14).Radius(8).Background(t.Warning.Alpha(0.15)).Border(1, t.Warning).Children(func() {
		ui.Text(c, "This review was written for other changes than the ones here now, so its code snippets can show the wrong lines. Regenerate the overview, or start over.")
		if sha := a.movedTo; sha != "" {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Those changes are now commit "+shortSHA(sha)+". The review moves there when you open it.").Grow(1).Shrink(1)
				ui.PrimaryButton(c, "Open commit").OnClick(func() { a.setScope(gitx.Scope{Kind: gitx.ScopeCommit, Commit: sha}) })
			})
		}
	})
}

func shortSHA(sha string) string { return sha[:min(len(sha), 7)] }

func (a *app) overviewPage(c *ui.Context) {
	t := c.Theme()
	cfg, _ := a.cfg.Effective(a.rev.Settings)
	run := a.overviewRunning()
	ov := a.rev.Overview
	ui.Scroll(c).TrackScroll(a.scrollOf("overview")).Grow(1).FillWidth().Children(func() {
		ui.Column(c).Padding(24, 32).Gap(16).MaxWidth(980).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Overview").FontSize(22).Bold()
				ui.Spacer(c)
				switch {
				case run != nil:
					ui.Button(c, "Stop").OnClick(a.stopOverview)
				case ov.Done():
					ui.Button(c, "Regenerate").Disabled(len(a.files) == 0).OnClick(func() { a.regenerate("") })
				}
				if run != nil || ov != nil {
					ui.Button(c, "Log").Tooltip("What the agent did to write the overview").OnClick(func() { a.showLog(store.TargetOverview) })
				}
				if ov != nil || len(a.rev.Threads) > 0 || len(a.rev.Walks) > 0 {
					ui.Button(c, "Start over").Tooltip("Delete the overview and comments of this scope").OnClick(a.confirmStartOver)
				}
			})
			a.summaryLine(c)
			if from := a.adoptedFrom; from != nil {
				verb := "Copied"
				if from.Kind == gitx.ScopeUncommitted {
					verb = "Moved"
				}
				ui.Text(c, verb+" here from "+strings.ToLower(from.Label())+", which had the same changes.").FontSize(12).TextColor(t.TextMuted)
			}
			a.staleBanner(c)
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
			case !ov.Done():
				a.overviewIntro(c, providerName(cfg.Provider))
			case ov.Walk != nil:
				ui.Text(c, writtenBy(ov.Agent, ov.Model, ov.Generated.Format("Jan 2 15:04"))).FontSize(12).TextColor(t.TextMuted)
				a.walkthrough(c, "", ov.Walk)
				a.checklist(c, ov.Walk.Checklist)
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
				a.askWalk(c, "")
				a.generalThreads(c)
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
		ui.Text(c, "Get a walkthrough of these changes").FontSize(16).Bold()
		ui.Text(c, agentName+" reads the changes in read-only mode and walks you through them in a few short steps, each with the code that shows it. Go deeper into any step, or ask about it, and you get a walkthrough one level closer to the code. It also answers your checklist and groups the files in the order to review them.").TextColor(t.TextMuted)
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
	if run.text != "" && !strings.HasPrefix(strings.TrimSpace(run.text), "{") && !strings.HasPrefix(strings.TrimSpace(run.text), "```") {
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
				ui.Button(c, "Review group").OnClick(func() { a.goTo(fmt.Sprintf("group:%d", gi)) })
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
						a.goTo("file:" + f)
					}
					if d != nil {
						ui.Text(c, fmt.Sprintf("+%d −%d", d.change.Additions, d.change.Deletions)).FontSize(11).TextColor(t.TextMuted)
					}
				})
			}
		})
	}
}

// generalThreads lists the questions about the whole change set from
// before the walkthrough answered them.
func (a *app) generalThreads(c *ui.Context) {
	ui.Column(c).Gap(6).Children(func() {
		for _, th := range a.rev.Threads {
			if th.Anchor.Kind == review.AnchorGeneral && len(th.Messages) > 0 {
				a.threadRow(c, th)
			}
		}
	})
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
