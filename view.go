package main

import (
	"fmt"
	"path"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/psdcoder/agentdiff/internal/gitx"
	"github.com/psdcoder/agentdiff/internal/review"
)

func (a *app) view(c *ui.Context) {
	if a.repo == nil {
		a.welcome(c)
		return
	}
	t := c.Theme()
	ui.Column(c).Fill().Children(func() {
		a.toolbar(c)
		ui.Split(c, &a.sidebarW, func() { a.sidebar(c) }, func() {
			var panel func()
			switch {
			case a.thread != "" && a.rev.Thread(a.thread) != nil:
				panel = func() { a.threadPanel(c) }
			case a.questionOpen():
				panel = func() { a.questionPanel(c) }
			}
			var side func()
			switch {
			case panel != nil && a.logOpen:
				side = func() {
					ui.SplitVerticalPercent(c, &a.logPct, panel, func() { a.logPanel(c) }).Fill()
				}
			case panel != nil:
				side = panel
			case a.logOpen:
				side = func() { a.logPanel(c) }
			default:
				a.main(c)
				return
			}
			ui.SplitPercent(c, &a.mainPct, func() { a.main(c) }, side).Fill()
		}).Grow(1).FillWidth()
	}).Background(t.Background)
	a.settingsModal(c)
	a.rangeModal(c)
	a.compareModal(c)
}

func (a *app) toolbar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(10).Padding(8, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Text(c, a.repo.Name()).Bold().SingleLine()
		if head := a.headBranch(); head != "" {
			ui.Button(c, head).Tooltip("Compare another branch").OnClick(a.openCompare)
		}
		ui.Text(c, a.scopeLabel()).TextColor(t.TextMuted).SingleLine().Shrink(1)
		ui.Spacer(c)
		cfg, _ := a.cfg.Effective(a.rev.Settings)
		label := providerName(cfg.Provider)
		if cfg.Model != "" {
			label += " · " + cfg.Model
		}
		ui.Text(c, label).FontSize(12).TextColor(t.TextMuted).SingleLine()
		ui.Button(c, "Agent log").Tooltip("Every call to the agent: command, prompt, tool calls, output").OnClick(func() {
			if a.logOpen && a.logTarget == "" {
				a.logOpen = false
				return
			}
			a.showLog("")
		})
		ui.Button(c, "Refresh").Tooltip("Read the changes again (Cmd+R)").OnClick(a.reload)
		ui.Button(c, "Settings").OnClick(a.openSettings)
		ui.Button(c, "Open…").Tooltip("Open another repository").OnClick(a.chooseRepo)
		n := len(review.ExportedThreads(a.rev, review.ExportOptions{}))
		ui.PrimaryButton(c, fmt.Sprintf("Feedback (%d)", n)).OnClick(func() { a.goTo("feedback") })
	})
}

func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	ui.SplitVerticalPercent(c, &a.scopePct, func() {
		ui.Column(c).Fill().Background(t.Surface).Children(func() {
			a.scopeList(c)
		})
	}, func() {
		ui.Column(c).Fill().Background(t.Surface).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
			a.reviewNav(c)
		})
	}).Fill()
}

func (a *app) scopeList(c *ui.Context) {
	choice := a.scopeID
	commitsOpen := true
	changed := ui.Sidebar(c, &choice, func() {
		ui.SidebarSection(c, "Changes", nil, func() {
			ui.SidebarItem(c, scopeID(gitx.Scope{Kind: gitx.ScopeUncommitted}), nil, "Uncommitted")
			if s, ok := a.branchScope(); ok {
				ui.SidebarItem(c, scopeID(s), nil, s.Label())
			}
			if a.scope.Kind == gitx.ScopeRange {
				ui.SidebarItem(c, a.scopeID, nil, a.scope.Label())
			}
			ui.SidebarItem(c, "compare", nil, "Compare branches…")
			ui.SidebarItem(c, "range", nil, "Custom range…")
		})
		title := "Recent commits"
		if a.commitBase() != "" {
			title = "Commits on " + a.headBranch()
		}
		ui.SidebarSection(c, title, &commitsOpen, func() {
			for _, cm := range a.commits {
				ui.SidebarItem(c, scopeID(gitx.Scope{Kind: gitx.ScopeCommit, Commit: cm.SHA}), nil, cm.Short+"  "+cm.Subject).
					Tooltip(cm.Subject + "\n" + cm.Author + ", " + cm.Time.Format("2006-01-02 15:04"))
			}
			if len(a.commits) == 0 {
				ui.Text(c, "No commits").FontSize(12).TextColor(c.Theme().TextMuted).Padding(4, 12)
			}
		})
	}).Fill().Label("Scope").Changed()
	if !changed || choice == a.scopeID {
		return
	}
	switch choice {
	case "range":
		a.openRange()
		return
	case "compare":
		a.openCompare()
		return
	}
	if s, ok := a.scopeFromID(choice); ok {
		a.setScope(s)
	}
}

func (a *app) scopeFromID(id string) (gitx.Scope, bool) {
	candidates := []gitx.Scope{{Kind: gitx.ScopeUncommitted}}
	if s, ok := a.branchScope(); ok {
		candidates = append(candidates, s)
	}
	for _, cm := range a.commits {
		candidates = append(candidates, gitx.Scope{Kind: gitx.ScopeCommit, Commit: cm.SHA})
	}
	for _, s := range candidates {
		if scopeID(s) == id {
			return s, true
		}
	}
	return gitx.Scope{}, false
}

func (a *app) reviewNav(c *ui.Context) {
	t := c.Theme()
	page := a.page
	groups := a.groups()
	changed := ui.Sidebar(c, &page, func() {
		ui.SidebarSection(c, "Review", nil, func() {
			ov := ui.SidebarItem(c, "overview", nil, "Overview")
			if a.overviewRunning() != nil {
				ov.Children(func() { ui.Spinner(c).Size(12, 12) })
			}
			a.walkItems(c, "", 1)
			threads := len(a.rev.Threads)
			ui.SidebarItem(c, "threads", nil, "Questions & comments").Children(func() {
				if threads > 0 {
					ui.Badge(c, fmt.Sprint(threads))
				}
			})
			ui.SidebarItem(c, "feedback", nil, "Feedback")
		})
		for gi, g := range groups {
			open := a.groupOpen(g.Title)
			ui.SidebarSection(c, g.Title, open, func() {
				if len(g.Files) > 1 {
					ui.SidebarItem(c, fmt.Sprintf("group:%d", gi), nil, fmt.Sprintf("All %d files", len(g.Files)))
				}
				for _, f := range g.Files {
					a.fileItem(c, f)
				}
			})
		}
		if a.loading {
			ui.Row(c).Gap(8).Padding(8, 12).Children(func() {
				ui.Spinner(c).Size(12, 12)
				ui.Text(c, "Reading changes…").FontSize(12).TextColor(t.TextMuted)
			})
		}
	}).Fill().Label("Review").Changed()
	if changed {
		a.goTo(page)
	}
}

// walkItems lists the walks under parent in the sidebar, each indented
// under the one it goes deeper into.
func (a *app) walkItems(c *ui.Context, parent string, depth int) {
	for _, w := range a.rev.Walks {
		if w.Parent != parent || w.Question != "" {
			continue
		}
		item := ui.SidebarItem(c, "walk:"+w.ID, nil, strings.Repeat("    ", depth)+excerpt(a.rev.WalkTitle(w), 36)).Tooltip(a.rev.WalkTitle(w))
		if a.walkBusy(w.ID) {
			item.Children(func() { ui.Spinner(c).Size(12, 12) })
		}
		if depth < 8 {
			a.walkItems(c, w.ID, depth+1)
		}
	}
}

func (a *app) groupOpen(title string) *bool {
	open := a.groupsOpen[title]
	if open == nil {
		v := true
		open = &v
		a.groupsOpen[title] = open
	}
	return open
}

func (a *app) fileItem(c *ui.Context, file string) {
	t := c.Theme()
	label := path.Base(file)
	if a.rev.Viewed[file] {
		label = "✓ " + label
	}
	item := ui.SidebarItem(c, "file:"+file, nil, label).Tooltip(file)
	d := a.diffs[file]
	if d == nil {
		return
	}
	n := len(a.rev.ThreadsOn(file))
	item.Children(func() {
		if n > 0 {
			ui.Badge(c, fmt.Sprint(n))
		}
		ui.Row(c).Gap(4).Children(func() {
			if d.change.Additions > 0 {
				ui.Text(c, fmt.Sprintf("+%d", d.change.Additions)).FontSize(11).TextColor(t.Success)
			}
			if d.change.Deletions > 0 {
				ui.Text(c, fmt.Sprintf("−%d", d.change.Deletions)).FontSize(11).TextColor(t.Danger)
			}
		})
	})
}

func (a *app) main(c *ui.Context) {
	if c.Shortcut(ui.Cmd, ui.KeyBracketLeft) {
		a.goBack()
	}
	ui.Column(c).Fill().Children(func() {
		a.backBar(c)
		if a.loadErr != "" {
			a.errorState(c, "Could not read the changes", a.loadErr)
			return
		}
		switch {
		case a.page == "overview":
			a.overviewPage(c)
		case a.page == "threads":
			a.threadsPage(c)
		case a.page == "feedback":
			a.feedbackPage(c)
		case strings.HasPrefix(a.page, "file:"):
			a.filePage(c, strings.TrimPrefix(a.page, "file:"))
		case strings.HasPrefix(a.page, "walk:"):
			a.walkPage(c, strings.TrimPrefix(a.page, "walk:"))
		case strings.HasPrefix(a.page, "group:"):
			var i int
			fmt.Sscanf(a.page, "group:%d", &i)
			a.groupPage(c, i)
		default:
			a.overviewPage(c)
		}
	})
}

func (a *app) errorState(c *ui.Context, title, detail string) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Gap(8).Padding(24).Children(func() {
		ui.Text(c, title).FontSize(16).Bold()
		ui.Text(c, detail).TextColor(t.TextMuted).Selectable().MaxWidth(600)
	})
}

func (a *app) emptyState(c *ui.Context, title, detail string) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Gap(8).Padding(24).Children(func() {
		ui.Text(c, title).FontSize(16).Bold()
		ui.Text(c, detail).TextColor(t.TextMuted).MaxWidth(520).TextAlign(ui.Center)
	})
}
