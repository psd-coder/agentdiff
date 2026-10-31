package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/psd-coder/agentdiff/internal/continuity"
	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
	"github.com/psd-coder/agentdiff/internal/store"
)

// staleBanner warns when the review was written for other changes, and
// names the commit that holds those changes now.
func (a *app) staleBanner(c *ui.Context) {
	if a.freshness() != overviewStale {
		return
	}
	t := c.Theme()
	ui.Column(c).Gap(10).Padding(10, 14).Radius(8).Background(t.Warning.Alpha(0.15)).Border(1, t.Warning).Children(func() {
		ui.Text(c, staleText(a.rev.Overview.Drift(a.fp.Files)))
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

// earlierList lists the archived reviews of the uncommitted changes.
// Each one opens on its own page.
func (a *app) earlierList(c *ui.Context) {
	if a.scope.Kind != gitx.ScopeUncommitted || len(a.reviews.Earlier()) == 0 || a.loader.firstLoad() {
		return
	}
	t := c.Theme()
	ui.Text(c, "Earlier reviews").FontSize(18).Bold().Margin(8, 0, 0, 0)
	ui.Text(c, "Reviews of uncommitted changes that are no longer in the working tree. A review comes back here by itself when its changes do.").
		FontSize(12).TextColor(t.TextMuted)
	for _, e := range a.reviews.Earlier() {
		row := ui.Row(c.Key("earlier-"+e.ID())).Gap(8).Padding(0, 10, 0, 0).Radius(8).Border(1, t.Border).AlignItems(ui.Center)
		row.Children(func() {
			open := ui.ButtonBase(c).Grow(1).Shrink(1).Padding(10, 14).Gap(4).Column().AlignItems(ui.Stretch)
			// The whole row shows the hover of its open area, so Delete
			// keeps its own.
			if open.Hovered() {
				row.Background(t.SurfaceHover)
			}
			open.Children(func() {
				ui.Text(c, earlierTitle(e.Rev)).Bold().MaxLines(2)
				ui.Text(c, strings.Join(a.earlierMeta(e), " · ")).FontSize(12).TextColor(t.TextMuted).SingleLine()
			})
			if open.Clicked() {
				a.nav.goTo(page{kindArchived, e.ID()})
			}
			a.cmdButton(c, "delete-archived", e.ID(), "Delete")
		})
	}
}

// earlierTitle is what an archived review is about: the summary of its
// overview, or the start of an overview from before walkthroughs.
func earlierTitle(rev *review.Review) string {
	ov := rev.Overview
	if ov.Walk != nil && ov.Walk.Summary != "" {
		return ov.Walk.Summary
	}
	if ov.Markdown != "" {
		return excerpt(ov.Markdown, 160)
	}
	return "Review without an overview"
}

func (a *app) earlierMeta(e *continuity.Earlier) []string {
	ov := e.Rev.Overview
	meta := []string{ov.Generated.Format("Jan 2 15:04")}
	if ov.Walk != nil {
		meta = append(meta, plural(len(ov.Walk.Steps), "step"))
	}
	if n := len(e.Rev.Threads); n > 0 {
		meta = append(meta, plural(n, "comment"))
	}
	switch {
	case e.MovedTo != "":
		meta = append(meta, "now commit "+shortSHA(e.MovedTo))
	case e.Probably != "":
		meta = append(meta, "probably in commit "+shortSHA(e.Probably))
	}
	return meta
}

func shortSHA(sha string) string { return sha[:min(len(sha), 7)] }

// commitName names a commit by its short hash and, when the commit list
// has it, its subject.
func (a *app) commitName(sha string) string {
	for _, cm := range a.commits.rows {
		if cm.SHA == sha {
			return cm.Short + " (" + cm.Subject + ")"
		}
	}
	return shortSHA(sha)
}

func (a *app) overviewPage(c *ui.Context) {
	t := c.Theme()
	cfg := a.cfg.Agent
	run := a.runner.run(a.rev, store.TargetOverview)
	ov := a.rev.Overview
	a.pageScroll(c, overview).Grow(1).FillWidth().Children(func() {
		ui.Column(c).Padding(24, 32).Gap(16).MaxWidth(980).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Overview").FontSize(22).Bold()
				ui.Spacer(c)
				a.cmdButton(c, "stop-overview", "", "Stop")
				a.cmdButton(c, "regenerate-overview", "", "Regenerate")
				a.cmdButton(c, "overview-log", "", "Log")
				a.cmdButton(c, "start-over", "", "Start over")
			})
			a.commitMessage(c)
			a.summaryLine(c)
			if from := a.reviews.AdoptedFrom(); from != nil {
				verb := "Copied"
				if from.Kind == gitx.ScopeUncommitted || from.Kind == gitx.ScopeArchived {
					verb = "Moved"
				}
				ui.Text(c, verb+" here from "+strings.ToLower(from.Label())+", which had the same changes.").FontSize(12).TextColor(t.TextMuted)
			}
			a.staleBanner(c)
			switch {
			case a.loader.firstLoad():
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
			if s := a.state(review.TargetOverview).status; s == targetFailed || s == targetStopped {
				failureBox(c, ov.Err)
			}
			if len(a.files) > 0 && !a.loader.firstLoad() {
				a.groupCards(c)
				a.askWalk(c, "")
				a.generalThreads(c)
			}
			a.earlierList(c)
		})
	})
}

// commitMessage shows the subject and the body of the commit under review.
func (a *app) commitMessage(c *ui.Context) {
	cm := a.commit
	if cm == nil {
		return
	}
	t := c.Theme()
	ui.Column(c).Gap(6).Children(func() {
		ui.Text(c, cm.Subject).FontSize(15).Bold().Selectable()
		if cm.Body != "" {
			ui.Text(c, cm.Body).Selectable()
		}
		ui.Text(c, cm.Short+" by "+cm.Author+", "+cm.Time.Format("Jan 2 2006 15:04")).FontSize(12).TextColor(t.TextMuted).Selectable()
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

func (a *app) streamingOverview(c *ui.Context, run *agentRun) {
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
		if a.busy(th.ID) {
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
				open := ui.Button(c, "Review group").OnClick(func() { a.nav.goTo(page{kindGroup, a.groupID(gi)}) })
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
						a.nav.goTo(page{kindFile, f})
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
