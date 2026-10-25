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
			if a.thread == "" || a.rev.Thread(a.thread) == nil {
				a.main(c)
				return
			}
			ui.SplitPercent(c, &a.mainPct, func() { a.main(c) }, func() { a.threadPanel(c) }).Fill()
		}).Grow(1).FillWidth()
	}).Background(t.Background)
	a.settingsModal(c)
	a.rangeModal(c)
}

func (a *app) toolbar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(10).Padding(8, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Text(c, a.repo.Name()).Bold().SingleLine()
		if a.branch != "" {
			ui.Badge(c, a.branch)
		}
		ui.Text(c, a.scopeLabel()).TextColor(t.TextMuted).SingleLine().Shrink(1)
		ui.Spacer(c)
		cfg, _ := a.cfg.Effective(a.rev.Settings)
		label := providerName(cfg.Provider)
		if cfg.Model != "" {
			label += " · " + cfg.Model
		}
		ui.Text(c, label).FontSize(12).TextColor(t.TextMuted).SingleLine()
		ui.Button(c, "Refresh").Tooltip("Read the changes again (Cmd+R)").OnClick(a.reload)
		ui.Button(c, "Settings").OnClick(a.openSettings)
		ui.Button(c, "Open…").Tooltip("Open another repository").OnClick(a.chooseRepo)
		n := len(review.ExportedThreads(a.rev, review.ExportOptions{}))
		ui.PrimaryButton(c, fmt.Sprintf("Feedback (%d)", n)).OnClick(func() { a.page = "feedback" })
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
			if base := a.commitBase(); base != "" {
				ui.SidebarItem(c, scopeID(gitx.Scope{Kind: gitx.ScopeBranch, Base: base}), nil, "Branch vs "+base)
			}
			if a.scope.Kind == gitx.ScopeRange {
				ui.SidebarItem(c, a.scopeID, nil, a.scope.Label())
			}
			ui.SidebarItem(c, "range", nil, "Custom range…")
		})
		title := "Recent commits"
		if a.commitBase() != "" {
			title = "Commits on " + a.branch
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
	if choice == "range" {
		a.openRange()
		return
	}
	if s, ok := a.scopeFromID(choice); ok {
		a.setScope(s)
	}
}

func (a *app) scopeFromID(id string) (gitx.Scope, bool) {
	candidates := []gitx.Scope{{Kind: gitx.ScopeUncommitted}}
	if base := a.commitBase(); base != "" {
		candidates = append(candidates, gitx.Scope{Kind: gitx.ScopeBranch, Base: base})
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
			threads := len(a.rev.Threads)
			ui.SidebarItem(c, "threads", nil, "Comments").Children(func() {
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
		a.page = page
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
	ui.Column(c).Fill().Children(func() {
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
