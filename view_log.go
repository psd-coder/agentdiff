package main

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/psdcoder/agentdiff/internal/agent"
	"github.com/psdcoder/agentdiff/internal/store"
)

const (
	logDetailLines = 6
	// logDetailMax keeps a huge tool output from slowing every frame; the
	// file holds all of it.
	logDetailMax = 20000
)

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
				ui.Text(c, agent.Count(total.Total())+" tokens").FontSize(12).TextColor(t.TextMuted).Tooltip(total.String())
			}
			if a.logTarget != "" {
				ui.Button(c, "Show all").OnClick(func() { a.logTarget = "" })
			}
			ui.Button(c, "Close").OnClick(func() { a.logOpen = false })
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

// logUsage is what the runs in the log panel spent, as far as known.
func (a *app) logUsage() (agent.Usage, bool) {
	var total agent.Usage
	known := false
	for _, r := range a.runsOf(a.rev) {
		if r.info.Usage != nil && (a.logTarget == "" || r.info.Target == a.logTarget) {
			total.Add(*r.info.Usage)
			known = true
		}
	}
	return total, known
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

func (a *app) logItemView(c *ui.Context, it *logItem) {
	t := c.Theme()
	ui.Column(c).Gap(2).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, it.time.Format("15:04:05")).Font("monospace").FontSize(11).TextColor(t.TextMuted)
			title := ui.Text(c, it.Title).FontSize(12).Bold().SingleLine().Shrink(1).Grow(1)
			if it.Failed {
				title.TextColor(t.Danger)
			}
			if long(it.Detail) {
				label := "More"
				if it.expanded {
					label = "Less"
				}
				ui.Button(c, label).OnClick(func() { it.expanded = !it.expanded })
			}
		})
		if it.Detail == "" {
			return
		}
		detail := ui.Text(c, clip(it.Detail)).Font("monospace").FontSize(11).Selectable().
			Padding(6, 8).Radius(6).Background(t.Surface)
		if !it.expanded {
			detail.MaxLines(logDetailLines)
		}
	})
}

func long(s string) bool {
	return strings.Count(s, "\n") >= logDetailLines || len(s) > 600
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
