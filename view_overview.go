package main

import (
	"fmt"
	"path"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/psdcoder/agentdiff/internal/gitx"
	"github.com/psdcoder/agentdiff/internal/review"
)

// staleBanner warns when the review was written for other changes, and
// names the commit that holds those changes now.
func (a *app) staleBanner(c *ui.Context) {
	if !a.stale() {
		return
	}
	t := c.Theme()
	ui.Column(c).Gap(10).Padding(10, 14).Radius(8).Background(t.Warning.Alpha(0.15)).Border(1, t.Warning).Children(func() {
		ui.Text(c, staleText(a.rev.Overview.Drift(a.fileIDs)))
		if sha := a.movedTo; sha != "" {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Those changes are now commit "+shortSHA(sha)+". The review moves there when you open it.").Grow(1).Shrink(1)
				a.cmdPrimaryButton(c, "open-moved-commit", "", "Open commit")
			})
		}
	})
}

// staleText warns that an overview was written for other changes, and
// says how its files differ from the changed ones.
func staleText(d review.Drift) string {
	const tail = "Its code snippets can show the wrong lines. Regenerate the overview, or start over."
	var parts []string
	if d.Changed > 0 {
		parts = append(parts, fmt.Sprintf("%d changed", d.Changed))
	}
	if d.Gone > 0 {
		parts = append(parts, fmt.Sprintf("%d %s no longer in the changes", d.Gone, isAre(d.Gone)))
	}
	if d.New > 0 {
		parts = append(parts, fmt.Sprintf("%s %s not in it", plural(d.New, "new file"), isAre(d.New)))
	}
	if len(parts) == 0 {
		return "This overview was written for other changes than the ones here now. " + tail
	}
	list := parts[0]
	if n := len(parts); n > 1 {
		list = strings.Join(parts[:n-1], ", ") + " and " + parts[n-1]
	}
	return fmt.Sprintf("This overview was written for %s. Since then, %s. %s", plural(d.Written, "file"), list, tail)
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// archivedCard offers the latest archived review on the uncommitted
// scope, while it has no review of its own.
func (a *app) archivedShown() bool {
	return a.archived != nil && a.scope.Kind == gitx.ScopeUncommitted && a.rev.Empty() && !a.loading
}

func (a *app) archivedCard(c *ui.Context) {
	arch := a.archived
	if !a.archivedShown() {
		return
	}
	t := c.Theme()
	ov := arch.Overview
	ui.Column(c).Gap(10).Padding(14, 16).Radius(8).Background(t.Surface).Border(1, t.Border).Children(func() {
		meta := []string{ov.Generated.Format("Jan 2 15:04")}
		if ov.Walk != nil {
			meta = append(meta, plural(len(ov.Walk.Steps), "step"))
		}
		if n := len(arch.Threads); n > 0 {
			meta = append(meta, plural(n, "comment"))
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Earlier review").Bold()
			ui.Text(c, strings.Join(meta, " · ")).FontSize(12).TextColor(t.TextMuted)
		})
		text := "It was written for uncommitted changes that are no longer in the working tree. It comes back here by itself when those changes do."
		switch {
		case a.movedTo != "":
			text = "It was written for changes that are now commit " + shortSHA(a.movedTo) + ". The review moves there when you open it."
		case a.probably != "":
			text = "Its files were probably committed in " + a.commitName(a.probably) + ", which changes all of them. The commit has other changes too, so the review does not move there by itself."
		}
		ui.Text(c, text).FontSize(12).TextColor(t.TextMuted)
		if ov.Walk != nil && ov.Walk.Summary != "" {
			ui.Text(c, ov.Walk.Summary).MaxLines(2)
		}
		ui.Row(c).Gap(8).Children(func() {
			a.cmdPrimaryButton(c, "open-moved-commit", "", "Open commit")
			a.cmdPrimaryButton(c, "move-archived", "", "Move review to the commit")
			a.cmdButton(c, "restore-archived", "", "Restore")
			a.cmdButton(c, "delete-archived", "", "Delete")
		})
	})
}

func shortSHA(sha string) string { return sha[:min(len(sha), 7)] }

// commitName names a commit by its short hash and, when the commit list
// has it, its subject.
func (a *app) commitName(sha string) string {
	for _, cm := range a.commits {
		if cm.SHA == sha {
			return cm.Short + " (" + cm.Subject + ")"
		}
	}
	return shortSHA(sha)
}

func (a *app) overviewPage(c *ui.Context) {
	t := c.Theme()
	cfg := a.cfg.Agent
	run := a.overviewRunning()
	ov := a.rev.Overview
	a.pageScroll(c, "overview").Grow(1).FillWidth().Children(func() {
		ui.Column(c).Padding(24, 32).Gap(16).MaxWidth(980).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Overview").FontSize(22).Bold()
				ui.Spacer(c)
				a.cmdButton(c, "stop-overview", "", "Stop")
				a.cmdButton(c, "regenerate-overview", "", "Regenerate")
				a.cmdButton(c, "overview-log", "", "Log")
				a.cmdButton(c, "start-over", "", "Start over")
			})
			a.summaryLine(c)
			if from := a.adoptedFrom; from != nil {
				verb := "Copied"
				if from.Kind == gitx.ScopeUncommitted || from.Kind == gitx.ScopeArchived {
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
			a.archivedCard(c)
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
		ui.Text(c, plural(len(a.files), "file")).TextColor(t.TextMuted)
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
		ui.Row(c).Children(func() {
			a.cmdPrimaryButton(c, "explain", "", "Explain changes with "+agentName)
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
		card := ui.Column(c.Key(fmt.Sprintf("group-%d", gi))).Gap(6).Padding(14, 18).Radius(10).Border(1, t.Border)
		card.Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, fmt.Sprintf("%d. %s", gi+1, g.Title)).Bold().Grow(1)
				open := ui.Button(c, "Review group").OnClick(func() { a.goTo(fmt.Sprintf("group:%d", gi)) })
				// Shown while the pointer is over the card, or the focus in
				// it; hidden, it keeps its room and its stop of Tab.
				if !card.Hovered() && !card.FocusWithin() {
					open.Opacity(0)
				}
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
					link := textLink(c, f, func(e ui.Element) { e.Font("monospace").FontSize(12) })
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
