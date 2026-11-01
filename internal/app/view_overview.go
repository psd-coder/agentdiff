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
func (app *app) staleBanner(ctx *ui.Context) {
	if app.freshness() != overviewStale {
		return
	}
	theme := ctx.Theme()
	ui.Column(ctx).Gap(10).Padding(10, 14).Radius(8).Background(theme.Warning.Alpha(0.15)).Border(1, theme.Warning).Children(func() {
		ui.Text(ctx, staleText(app.rev.Overview.Drift(app.fp.Files)))
		if sha := app.movedTo; sha != "" {
			ui.Row(ctx).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(ctx, "Those changes are now commit "+shortSHA(sha)+". The review moves there when you open it.").Grow(1).Shrink(1)
				app.cmdPrimaryButton(ctx, "open-moved-commit", "", "Open commit")
			})
		}
	})
}

// promptBanner warns about the prompt files of the repository that the
// runs leave out.
func (app *app) promptBanner(ctx *ui.Context) {
	if len(app.promptProblems) == 0 {
		return
	}
	theme := ctx.Theme()
	ui.Column(ctx).Gap(6).Padding(10, 14).Radius(8).Background(theme.Warning.Alpha(0.15)).Border(1, theme.Warning).Children(func() {
		ui.Text(ctx, "The agent runs use the prompts from the settings in place of these files of the repository:")
		for _, p := range app.promptProblems {
			ui.Text(ctx, p).FontSize(12).Selectable()
		}
	})
}

// staleText warns that an overview was written for other changes, and
// says how its files differ from the changed ones.
func staleText(drift review.Drift) string {
	const tail = "Its code snippets can show the wrong lines. Regenerate the overview, or start over."
	var parts []string
	if drift.Changed > 0 {
		parts = append(parts, fmt.Sprintf("%d changed", drift.Changed))
	}
	if drift.Gone > 0 {
		parts = append(parts, fmt.Sprintf("%d %s no longer in the changes", drift.Gone, isAre(drift.Gone)))
	}
	if drift.New > 0 {
		parts = append(parts, fmt.Sprintf("%s %s not in it", plural(drift.New, "new file"), isAre(drift.New)))
	}
	if len(parts) == 0 {
		return "This overview was written for other changes than the ones here now. " + tail
	}
	list := parts[0]
	if count := len(parts); count > 1 {
		list = strings.Join(parts[:count-1], ", ") + " and " + parts[count-1]
	}
	return fmt.Sprintf("This overview was written for %s. Since then, %s. %s", plural(drift.Written, "file"), list, tail)
}

func isAre(count int) string {
	if count == 1 {
		return "is"
	}
	return "are"
}

// earlierList lists the archived reviews of the uncommitted changes.
// Each one opens on its own page.
func (app *app) earlierList(ctx *ui.Context) {
	if app.scope.Kind != gitx.ScopeUncommitted || len(app.reviews.Earlier()) == 0 || app.loader.firstLoad() {
		return
	}
	theme := ctx.Theme()
	ui.Text(ctx, "Earlier reviews").FontSize(18).Bold().Margin(8, 0, 0, 0)
	ui.Text(ctx, "Reviews of uncommitted changes that are no longer in the working tree. A review comes back here by itself when its changes do.").
		FontSize(12).TextColor(theme.TextMuted)
	for _, e := range app.reviews.Earlier() {
		row := ui.Row(ctx.Key("earlier-"+e.ID())).Gap(8).Padding(0, 10, 0, 0).Radius(8).Border(1, theme.Border).AlignItems(ui.Center)
		row.Children(func() {
			open := ui.ButtonBase(ctx).Grow(1).Shrink(1).Padding(10, 14).Gap(4).Column().AlignItems(ui.Stretch)
			// The whole row shows the hover of its open area, so Delete
			// keeps its own.
			if open.Hovered() {
				row.Background(theme.SurfaceHover)
			}
			open.Children(func() {
				ui.Text(ctx, earlierTitle(e.Rev)).Bold().MaxLines(2)
				ui.Text(ctx, strings.Join(app.earlierMeta(e), " · ")).FontSize(12).TextColor(theme.TextMuted).SingleLine()
			})
			if open.Clicked() {
				app.nav.goTo(page{kindArchived, e.ID()})
			}
			app.cmdButton(ctx, "delete-archived", e.ID(), "Delete")
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

func (app *app) earlierMeta(earlier *continuity.Earlier) []string {
	ov := earlier.Rev.Overview
	meta := []string{ov.Generated.Format("Jan 2 15:04")}
	if ov.Walk != nil {
		meta = append(meta, plural(len(ov.Walk.Steps), "step"))
	}
	if count := len(earlier.Rev.Threads); count > 0 {
		meta = append(meta, plural(count, "comment"))
	}
	switch {
	case earlier.MovedTo != "":
		meta = append(meta, "now commit "+shortSHA(earlier.MovedTo))
	case earlier.Probably != "":
		meta = append(meta, "probably in commit "+shortSHA(earlier.Probably))
	}
	return meta
}

func shortSHA(sha string) string { return sha[:min(len(sha), 7)] }

// commitName names a commit by its short hash and, when the commit list
// has it, its subject.
func (app *app) commitName(sha string) string {
	for _, cm := range app.commits.rows {
		if cm.SHA == sha {
			return cm.Short + " (" + cm.Subject + ")"
		}
	}
	return shortSHA(sha)
}

func (app *app) overviewPage(ctx *ui.Context) {
	theme := ctx.Theme()
	cfg := app.cfg.Agent
	run := app.runner.run(app.rev, store.TargetOverview)
	ov := app.rev.Overview
	app.pageScroll(ctx, overview).Grow(1).FillWidth().Children(func() {
		ui.Column(ctx).Padding(24, 32).Gap(16).MaxWidth(980).Children(func() {
			ui.Row(ctx).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(ctx, "Overview").FontSize(22).Bold()
				ui.Spacer(ctx)
				app.cmdButton(ctx, "stop-overview", "", "Stop")
				app.cmdButton(ctx, "regenerate-overview", "", "Regenerate")
				app.cmdButton(ctx, "overview-log", "", "Log")
				app.cmdButton(ctx, "start-over", "", "Start over")
			})
			app.commitMessage(ctx)
			app.summaryLine(ctx)
			if from := app.reviews.AdoptedFrom(); from != nil {
				verb := "Copied"
				if from.Kind == gitx.ScopeUncommitted || from.Kind == gitx.ScopeArchived {
					verb = "Moved"
				}
				ui.Text(ctx, verb+" here from "+strings.ToLower(from.Label())+", which had the same changes.").FontSize(12).TextColor(theme.TextMuted)
			}
			app.staleBanner(ctx)
			app.promptBanner(ctx)
			switch {
			case app.loader.firstLoad():
				ui.Row(ctx).Gap(8).Children(func() {
					ui.Spinner(ctx).Size(14, 14)
					ui.Text(ctx, "Reading changes…").TextColor(theme.TextMuted)
				})
			case len(app.files) == 0:
				app.emptyState(ctx, "No changes", "There is nothing to review in "+strings.ToLower(app.scope.Label())+". Pick another scope on the left.")
			case run != nil:
				app.streamingOverview(ctx, run)
			case !ov.Done():
				app.overviewIntro(ctx, cfg.Provider.Name())
			case ov.Walk != nil:
				ui.Text(ctx, writtenBy(ov.Agent, ov.Model, ov.Generated.Format("Jan 2 15:04"))).FontSize(12).TextColor(theme.TextMuted)
				app.walkthrough(ctx, "", ov.Walk)
			default:
				app.overviewSections(ctx, ov)
			}
			if status := app.state(review.TargetOverview).status; status == targetFailed || status == targetStopped {
				failureBox(ctx, ov.Err)
			}
			if len(app.files) > 0 && !app.loader.firstLoad() {
				app.groupCards(ctx)
				app.askWalk(ctx, "")
				app.generalThreads(ctx)
			}
			app.earlierList(ctx)
		})
	})
}

// commitMessage shows the subject and the body of the commit under review.
func (app *app) commitMessage(ctx *ui.Context) {
	cm := app.commit
	if cm == nil {
		return
	}
	theme := ctx.Theme()
	ui.Column(ctx).Gap(6).Children(func() {
		ui.Text(ctx, cm.Subject).FontSize(15).Bold().Selectable()
		if cm.Body != "" {
			ui.Text(ctx, cm.Body).Selectable()
		}
		ui.Text(ctx, cm.Short+" by "+cm.Author+", "+cm.Time.Format("Jan 2 2006 15:04")).FontSize(12).TextColor(theme.TextMuted).Selectable()
	})
}

func (app *app) summaryLine(ctx *ui.Context) {
	theme := ctx.Theme()
	if len(app.files) == 0 {
		return
	}
	add, del := 0, 0
	for _, f := range app.files {
		add += f.Additions
		del += f.Deletions
	}
	ui.Row(ctx).Gap(12).Children(func() {
		ui.Text(ctx, plural(len(app.files), "file")).TextColor(theme.TextMuted)
		ui.Text(ctx, fmt.Sprintf("+%d", add)).TextColor(theme.Success)
		ui.Text(ctx, fmt.Sprintf("−%d", del)).TextColor(theme.Danger)
		viewed := 0
		for _, f := range app.files {
			if app.rev.Viewed[f.Path] {
				viewed++
			}
		}
		ui.Text(ctx, fmt.Sprintf("%d of %d viewed", viewed, len(app.files))).TextColor(theme.TextMuted)
	})
}

func (app *app) overviewIntro(ctx *ui.Context, agentName string) {
	theme := ctx.Theme()
	ui.Column(ctx).Gap(12).Padding(20).Radius(10).Background(theme.Surface).Border(1, theme.Border).Children(func() {
		ui.Text(ctx, "Get a walkthrough of these changes").FontSize(16).Bold()
		ui.Text(ctx, agentName+" reads the changes in read-only mode and walks you through them in a few short steps, each with the code that shows it. Go deeper into any step, or ask about it, and you get a walkthrough one level closer to the code. It also answers your checklist and groups the files in the order to review them.").TextColor(theme.TextMuted)
		ui.Text(ctx, "It runs with your own subscription, through the CLI on this Mac. Change the agent and the prompts in Settings.").FontSize(12).TextColor(theme.TextMuted)
		ui.Row(ctx).Children(func() {
			app.cmdPrimaryButton(ctx, "explain", "", "Explain changes with "+agentName)
		})
	})
}

func (app *app) streamingOverview(ctx *ui.Context, run *agentRun) {
	theme := ctx.Theme()
	ui.Row(ctx).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Spinner(ctx).Size(14, 14)
		activity := run.activity
		if activity == "" {
			activity = "Writing…"
		}
		ui.Text(ctx, activity).FontSize(12).TextColor(theme.TextMuted).SingleLine()
	})
	if run.text != "" && !strings.HasPrefix(strings.TrimSpace(run.text), "{") && !strings.HasPrefix(strings.TrimSpace(run.text), "```") {
		ui.Column(ctx).Children(func() { markdown.Stream(ctx, run.text, &app.md) })
	}
}

func (app *app) overviewSections(ctx *ui.Context, ov *review.Overview) {
	theme := ctx.Theme()
	meta := fmt.Sprintf("By %s", ov.Agent.Name())
	if ov.Model != "" {
		meta += " (" + ov.Model + ")"
	}
	meta += ", " + ov.Generated.Format("Jan 2 15:04")
	ui.Text(ctx, meta).FontSize(12).TextColor(theme.TextMuted)
	for i, s := range review.Sections(ov.Markdown) {
		ui.Column(ctx.Key(fmt.Sprintf("section-%d-%s", i, s.Title))).Gap(8).Padding(16, 20).Radius(10).Background(theme.Surface).Border(1, theme.Border).Children(func() {
			ui.Row(ctx).Gap(8).AlignItems(ui.Center).Children(func() {
				title := s.Title
				if title == "" {
					title = "Summary"
				}
				ui.Text(ctx, title).FontSize(16).Bold().Grow(1)
				app.sectionThreads(ctx, title, s.Body)
			})
			markdown.View(ctx, s.Body, &app.md)
		})
	}
}

func (app *app) sectionThreads(ctx *ui.Context, title, body string) {
	threads := app.rev.ThreadsOnSection(title)
	for _, th := range threads {
		label := "Thread"
		if app.busy(th.ID) {
			label = "Answering…"
		} else if len(th.Messages) > 0 {
			label = excerpt(th.Messages[0].Text, 24)
		}
		ui.Button(ctx.Key("t-"+th.ID), label).Tooltip("Open the thread").OnClick(func() { app.openThread(th.ID) })
	}
	ui.Button(ctx.Key("discuss-"+title), "Discuss").Tooltip("Start a thread about this part").OnClick(func() {
		app.newThread(review.Anchor{Kind: review.AnchorSection, Section: title, Quote: body})
	})
}

func (app *app) groupCards(ctx *ui.Context) {
	theme := ctx.Theme()
	groups := app.groups()
	if len(groups) == 0 {
		return
	}
	title := "Files by directory"
	if app.rev.Overview != nil && len(app.rev.Overview.Groups) > 0 {
		title = "Review order"
	}
	ui.Text(ctx, title).FontSize(18).Bold().Margin(8, 0, 0, 0)
	for gi, g := range groups {
		card := ui.Column(ctx.Key(fmt.Sprintf("group-%d", gi))).Gap(6).Padding(14, 18).Radius(10).Border(1, theme.Border)
		card.Children(func() {
			ui.Row(ctx).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(ctx, fmt.Sprintf("%d. %s", gi+1, g.Title)).Bold().Grow(1)
				open := ui.Button(ctx, "Review group").OnClick(func() { app.nav.goTo(page{kindGroup, app.groupID(gi)}) })
				// Shown while the pointer is over the card, or the focus in
				// it; hidden, it keeps its room and its stop of Tab.
				if !card.Hovered() && !card.FocusWithin() {
					open.Opacity(0)
				}
			})
			if g.Summary != "" {
				ui.Text(ctx, g.Summary).TextColor(theme.TextMuted)
			}
			for _, f := range g.Files {
				diff := app.diffs[f]
				ui.Row(ctx.Key(f)).Gap(8).AlignItems(ui.Center).Children(func() {
					mark := " "
					if app.rev.Viewed[f] {
						mark = "✓"
					}
					ui.Text(ctx, mark).Width(14).TextColor(theme.Success)
					link := textLink(ctx, f, func(e ui.Element) { e.Font("monospace").FontSize(12) })
					if link.Clicked() {
						app.nav.goTo(page{kindFile, f})
					}
					if diff != nil {
						ui.Text(ctx, fmt.Sprintf("+%d −%d", diff.change.Additions, diff.change.Deletions)).FontSize(11).TextColor(theme.TextMuted)
					}
				})
			}
		})
	}
}

// generalThreads lists the questions about the whole change set from
// before the walkthrough answered them.
func (app *app) generalThreads(ctx *ui.Context) {
	ui.Column(ctx).Gap(6).Children(func() {
		for _, th := range app.rev.Threads {
			if th.Anchor.Kind == review.AnchorGeneral && len(th.Messages) > 0 {
				app.threadRow(ctx, th)
			}
		}
	})
}

func excerpt(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
