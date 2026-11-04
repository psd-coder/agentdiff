package app

import (
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/review"
)

// logDetailMax keeps a huge tool output from slowing every frame; the
// file holds all of it.
const logDetailMax = 20000

func (app *app) logTargetLabel() string {
	if app.logTarget == "" {
		return "All runs"
	}
	switch kind, ok := app.rev.Resolve(app.logTarget); {
	case !ok:
		return "Thread"
	case kind == review.KindOverview:
		versions := app.rev.Versions()
		if len(versions) < 2 {
			return "Overview"
		}
		index := slices.IndexFunc(versions, func(ov *review.Overview) bool { return ov.ID == app.logVersion })
		if index < 0 {
			index = len(versions) - 1
		}
		return "Overview: " + versionLabels(versions, app.rev.Overview)[index]
	case kind == review.KindWalk:
		return app.rev.WalkTitle(app.rev.Walk(app.logTarget))
	}
	return app.rev.Thread(app.logTarget).Anchor.Label()
}

func (app *app) logPanel(ctx *ui.Context) {
	theme := ctx.Theme()
	rev := app.rev
	panelFrame(ctx).Children(func() {
		panelBar(ctx).Children(func() {
			ui.Text(ctx, "Agent log").Bold()
			caption(ctx, app.logTargetLabel()).SingleLine().Shrink(1).Grow(1)
			if total, ok := app.logUsage(); ok {
				label, tip := agent.Count(total.Total())+" tokens processed", total.String()
				if spent, ok := spentContext(app.logsOf(rev), app.inLog); ok {
					label = agent.Count(spent) + " tokens of context"
					tip = "The context the runs built up. A run that resumes or forks an earlier session starts with its context, so it counts only what it added.\n" + tip
				}
				caption(ctx, label).Tooltip(tip)
			}
			app.cmdButton(ctx, "log-show-all", "", "Show all")
			app.cmdButton(ctx, "close-log", "", "Close")
		})
		ui.Scroll(ctx).Grow(1).FillWidth().Children(func() {
			ui.Column(ctx).Gap(theme.Space(1.5)).Padding(theme.Space(2.5), theme.Space(3)).Children(func() {
				shownRuns := 0
				for _, r := range app.logsOf(rev) {
					if !app.inLog(r) {
						continue
					}
					shownRuns++
					app.runView(ctx, r)
				}
				if shownRuns == 0 {
					ui.Text(ctx, "No agent runs yet. Every overview and every question to the agent adds a run here, with the command, the prompt and all the agent's steps.").TextColor(theme.TextMuted)
				}
			})
		})
	})
}

// logUsage is what the runs in the log panel spent, as far as known. The
// context sizes and sessions of separate runs do not add up, so the total
// has none.
func (app *app) logUsage() (agent.Usage, bool) {
	var total agent.Usage
	known := false
	for _, r := range app.logsOf(app.rev) {
		if r.info.Usage != nil && app.inLog(r) {
			total.Add(*r.info.Usage)
			known = true
		}
	}
	total.Context, total.Session, total.SessionCost = 0, "", 0
	return total, known
}

// inLog reports whether the log panel shows run r.
func (app *app) inLog(run *logRun) bool {
	switch {
	case app.logTarget == "":
		return true
	case run.info.Target != app.logTarget:
		return false
	case app.logTarget == review.TargetOverview:
		return app.runOfVersion(run.info.Started, app.logVersion)
	}
	return true
}

// spentContext adds up the context the runs that keep reports built. A run
// that resumed or forked a session starts with the context of the run
// that ended in it, so it counts only what it added. It reports false when
// none of those runs knows its context size.
func spentContext(runs []*logRun, keep func(*logRun) bool) (int, bool) {
	total, known := 0, false
	for _, r := range runs {
		usage := r.info.Usage
		if !keep(r) || usage == nil || usage.Context == 0 {
			continue
		}
		known = true
		base := 0
		if resumed := resumedRun(runs, r); resumed != nil {
			base = resumed.info.Usage.Context
		}
		total += max(0, usage.Context-base)
	}
	return total, known
}

// resumedRun is the run that ended in the session r resumed or forked:
// the latest of them that started before r.
func resumedRun(runs []*logRun, run *logRun) *logRun {
	var found *logRun
	for _, p := range runs {
		usage := p.info.Usage
		if p == run || usage == nil || run.info.Resumed == "" || usage.Session != run.info.Resumed || !p.info.Started.Before(run.info.Started) {
			continue
		}
		if found == nil || p.info.Started.After(found.info.Started) {
			found = p
		}
	}
	return found
}

func (app *app) runView(ctx *ui.Context, run *logRun) {
	theme := ctx.Theme()
	rev := app.rev
	ui.Collapsible(ctx.Key("run-"+run.info.ID), run.label(), &run.open, func() {
		if !run.loaded {
			app.loadRun(rev, run)
		}
		ui.Column(ctx).Gap(theme.Space(2)).Padding(theme.Space(1), 0, theme.Space(2), 0).Children(func() {
			if run.info.Note != "" {
				caption(ctx, run.info.Note).MaxLines(2)
			}
			if usage := run.info.Usage; usage != nil {
				caption(ctx, usage.String())
			}
			ui.Row(ctx).Gap(theme.Space(2)).Children(func() {
				ui.Button(ctx, "Copy").Disabled(!run.loaded).OnClick(func() {
					copyText(ctx, run.text(), "Run log copied")
				})
				if app.win != nil {
					path := app.store.RunPath(rev.Repo, rev.Scope, run.info.ID)
					ui.Button(ctx, "Show file").Tooltip(path).OnClick(func() { mygo.Shell.ShowItemInFolder(path) })
				}
			})
			if run.err != "" {
				errorText(ctx, run.err)
			}
			if run.loading {
				ui.Spinner(ctx).Size(12, 12)
			}
			for i := range run.items {
				app.logItemView(ctx.Key(i), &run.items[i])
			}
			if run.running {
				activityLine(ctx, "Running…")
			}
		})
	})
}

// logItemView shows an entry of a run as its title, which opens its
// detail.
func (app *app) logItemView(ctx *ui.Context, it *logItem) {
	theme := ctx.Theme()
	ui.Column(ctx).Gap(theme.Space(0.5)).Children(func() {
		head := ui.ButtonBase(ctx).Gap(theme.Space(2)).Padding(theme.Space(0.5), theme.Space(1)).Radius(radiusSmall).Justify(ui.Start).Expanded(it.expanded).Label(it.Title)
		if it.Detail == "" {
			head = ui.Row(ctx).Gap(theme.Space(2)).Padding(theme.Space(0.5), theme.Space(1)).AlignItems(ui.Center)
		} else if head.Hovered() {
			head.Background(theme.SurfaceHover)
		}
		head.Children(func() {
			arrow := disclosureArrow(ctx, it.expanded)
			if it.Detail == "" {
				arrow.Invisible()
			}
			ui.Text(ctx, it.time.Format("15:04:05")).Font(monoFont).FontSize(fontTiny).TextColor(theme.TextMuted)
			title := ui.Text(ctx, it.Title).FontSize(fontSmall).Bold().SingleLine().Shrink(1).Grow(1)
			if it.Failed {
				title.TextColor(theme.Danger)
			}
		})
		if it.Detail != "" && head.Clicked() {
			it.expanded = !it.expanded
		}
		if it.Detail != "" && it.expanded {
			codeQuote(ctx, clip(it.Detail)).FontSize(fontTiny)
		}
	})
}

func clip(text string) string {
	if len(text) <= logDetailMax {
		return text
	}
	end := logDetailMax
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + fmt.Sprintf("\n… %d more characters in the log file", len(text)-end)
}
