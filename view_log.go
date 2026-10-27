package main

import (
	"fmt"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/psdcoder/agentdiff/internal/agent"
	"github.com/psdcoder/agentdiff/internal/store"
)

// logDetailMax keeps a huge tool output from slowing every frame; the
// file holds all of it.
const logDetailMax = 20000

func (a *app) logTargetLabel() string {
	switch a.logTarget {
	case "":
		return "All runs"
	case store.TargetOverview:
		return "Overview"
	}
	if th := a.rev.Thread(a.logTarget); th != nil {
		return th.Anchor.Label()
	}
	if w := a.rev.Walk(a.logTarget); w != nil {
		return a.rev.WalkTitle(w)
	}
	return "Thread"
}

func (a *app) logPanel(c *ui.Context) {
	t := c.Theme()
	rev := a.rev
	ui.Column(c).Fill().Background(t.Background).BorderWidth(0, 0, 0, 1).BorderColor(t.Border).Children(func() {
		ui.Row(c).Gap(8).Padding(8, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Text(c, "Agent log").Bold()
			ui.Text(c, a.logTargetLabel()).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(1).Grow(1)
			if total, ok := a.logUsage(); ok {
				label, tip := agent.Count(total.Total())+" tokens processed", total.String()
				if n, ok := spentContext(a.runsOf(rev), a.inLog); ok {
					label = agent.Count(n) + " tokens of context"
					tip = "The context the runs built up. A run that resumes or forks an earlier session starts with its context, so it counts only what it added.\n" + tip
				}
				ui.Text(c, label).FontSize(12).TextColor(t.TextMuted).Tooltip(tip)
			}
			a.cmdButton(c, "log-show-all", "", "Show all")
			a.cmdButton(c, "close-log", "", "Close")
		})
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			ui.Column(c).Gap(6).Padding(10, 12).Children(func() {
				n := 0
				for _, r := range a.runsOf(rev) {
					if a.logTarget != "" && r.info.Target != a.logTarget {
						continue
					}
					n++
					a.runView(c, r)
				}
				if n == 0 {
					ui.Text(c, "No agent runs yet. Every overview and every question to the agent adds a run here, with the command, the prompt and all the agent's steps.").TextColor(t.TextMuted)
				}
			})
		})
	})
}

// logUsage is what the runs in the log panel spent, as far as known. The
// context sizes and sessions of separate runs do not add up, so the total
// has none.
func (a *app) logUsage() (agent.Usage, bool) {
	var total agent.Usage
	known := false
	for _, r := range a.runsOf(a.rev) {
		if r.info.Usage != nil && a.inLog(r) {
			total.Add(*r.info.Usage)
			known = true
		}
	}
	total.Context, total.Session, total.SessionCost = 0, "", 0
	return total, known
}

// inLog reports whether the log panel shows run r.
func (a *app) inLog(r *logRun) bool { return a.logTarget == "" || r.info.Target == a.logTarget }

// spentContext adds up the context the runs that keep reports built. A run
// that resumed or forked a session starts with the context of the run
// that ended in it, so it counts only what it added. It reports false when
// none of those runs knows its context size.
func spentContext(runs []*logRun, keep func(*logRun) bool) (int, bool) {
	total, known := 0, false
	for _, r := range runs {
		u := r.info.Usage
		if !keep(r) || u == nil || u.Context == 0 {
			continue
		}
		known = true
		base := 0
		if p := resumedRun(runs, r); p != nil {
			base = p.info.Usage.Context
		}
		total += max(0, u.Context-base)
	}
	return total, known
}

// resumedRun is the run that ended in the session r resumed or forked:
// the latest of them that started before r.
func resumedRun(runs []*logRun, r *logRun) *logRun {
	var found *logRun
	for _, p := range runs {
		u := p.info.Usage
		if p == r || u == nil || r.info.Resumed == "" || u.Session != r.info.Resumed || !p.info.Started.Before(r.info.Started) {
			continue
		}
		if found == nil || p.info.Started.After(found.info.Started) {
			found = p
		}
	}
	return found
}

func (a *app) runView(c *ui.Context, r *logRun) {
	t := c.Theme()
	rev := a.rev
	ui.Collapsible(c.Key("run-"+r.info.ID), r.label(), &r.open, func() {
		if !r.loaded {
			a.loadRun(rev, r)
		}
		ui.Column(c).Gap(8).Padding(4, 0, 8, 0).Children(func() {
			if r.info.Note != "" {
				ui.Text(c, r.info.Note).FontSize(12).TextColor(t.TextMuted).MaxLines(2)
			}
			if u := r.info.Usage; u != nil {
				ui.Text(c, u.String()).FontSize(12).TextColor(t.TextMuted)
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.Button(c, "Copy").Disabled(!r.loaded).OnClick(func() {
					c.WriteClipboard(r.text())
					c.Toast("Run log copied")
				})
				if a.win != nil {
					path := a.store.RunPath(rev.Repo, rev.Scope, r.info.ID)
					ui.Button(c, "Show file").Tooltip(path).OnClick(func() { mygo.Shell.ShowItemInFolder(path) })
				}
			})
			if r.err != "" {
				ui.Text(c, r.err).FontSize(12).TextColor(t.Danger).Selectable()
			}
			if r.loading {
				ui.Spinner(c).Size(12, 12)
			}
			for i := range r.items {
				a.logItemView(c.Key(i), &r.items[i])
			}
			if r.running {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(12, 12)
					ui.Text(c, "Running…").FontSize(12).TextColor(t.TextMuted)
				})
			}
		})
	})
}

// logItemView shows an entry of a run as its title, which opens its
// detail.
func (a *app) logItemView(c *ui.Context, it *logItem) {
	t := c.Theme()
	ui.Column(c).Gap(2).Children(func() {
		head := ui.ButtonBase(c).Gap(8).Padding(2, 4).Radius(4).Justify(ui.Start).Expanded(it.expanded).Label(it.Title)
		if it.Detail == "" {
			head = ui.Row(c).Gap(8).Padding(2, 4).AlignItems(ui.Center)
		} else if head.Hovered() {
			head.Background(t.SurfaceHover)
		}
		head.Children(func() {
			arrow := ui.Icon(c, chevronIcon).Size(10, 10).TextColor(t.TextMuted)
			switch {
			case it.Detail == "":
				arrow.Invisible()
			case !it.expanded:
				arrow.Rotate(-90)
			}
			ui.Text(c, it.time.Format("15:04:05")).Font("monospace").FontSize(11).TextColor(t.TextMuted)
			title := ui.Text(c, it.Title).FontSize(12).Bold().SingleLine().Shrink(1).Grow(1)
			if it.Failed {
				title.TextColor(t.Danger)
			}
		})
		if it.Detail != "" && head.Clicked() {
			it.expanded = !it.expanded
		}
		if it.Detail != "" && it.expanded {
			ui.Text(c, clip(it.Detail)).Font("monospace").FontSize(11).Selectable().
				Padding(6, 8).Radius(6).Background(t.Surface)
		}
	})
}

func clip(s string) string {
	if len(s) <= logDetailMax {
		return s
	}
	n := logDetailMax
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + fmt.Sprintf("\n… %d more characters in the log file", len(s)-n)
}
