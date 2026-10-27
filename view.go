package main

import (
	"fmt"
	"path"
	"strings"
	"time"

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
	a.keys(c)
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
	a.compareModal(c)
}

func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	ui.SplitVerticalPercent(c, &a.scopePct, func() {
		ui.Column(c).Fill().Background(t.Surface).Children(func() {
			a.scopeList(c)
		})
	}, func() {
		ui.Column(c).Fill().Background(t.Surface).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
			a.scopeHeader(c)
			a.reviewNav(c)
		})
	}).Fill()
}

// sidebarHeading names a part of a sidebar, above its sections.
func sidebarHeading(c *ui.Context, title, detail string) {
	t := c.Theme()
	ui.Row(c).Gap(6).Padding(10, 8, 4, 8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, strings.ToUpper(title)).FontSize(10).Bold().LetterSpacing(0.6).TextColor(t.TextMuted).Grow(1).SingleLine()
		if detail != "" {
			ui.Text(c, detail).FontSize(11).TextColor(t.TextMuted).SingleLine()
		}
	})
}

func (a *app) scopeList(c *ui.Context) {
	t := c.Theme()
	choice := a.scopeID
	changed := ui.Sidebar(c, &choice, func() {
		sidebarHeading(c, "Changes", "")
		if a.uncommittedShown() {
			ui.SidebarItem(c, scopeID(gitx.Scope{Kind: gitx.ScopeUncommitted}), editIcon, "Uncommitted changes")
		}
		ui.SidebarSection(c, a.commitsTitle(), nil, func() {
			if s, ok := a.allScope(); ok && len(a.commits) > 0 {
				ui.SidebarItem(c, scopeID(s), layersIcon, "All changes").Tooltip(s.Label()).Children(func() {
					ui.Badge(c, fmt.Sprint(len(a.commits)))
				})
			}
			for _, cm := range a.commits {
				ui.SidebarItem(c, scopeID(gitx.Scope{Kind: gitx.ScopeCommit, Commit: cm.SHA}), commitIcon, cm.Subject).
					Tooltip(cm.Short + "  " + cm.Subject + "\n" + cm.Author + ", " + cm.Time.Format("2006-01-02 15:04")).
					Children(func() {
						ui.Text(c, age(cm.Time)).FontSize(11).TextColor(t.TextMuted).SingleLine()
					})
			}
			if len(a.commits) == 0 {
				ui.Text(c, "No commits").FontSize(12).TextColor(t.TextMuted).Padding(4, 8)
			}
		})
	}).Fill().Label("Changes").Changed()
	if !changed || choice == a.scopeID {
		return
	}
	if s, ok := a.scopeFromID(choice); ok {
		a.setScope(s)
	}
}

// commitsTitle names the commit list.
func (a *app) commitsTitle() string {
	switch {
	case a.rng != nil:
		return "Commits in " + a.rng.Label()
	case a.commitBase() != "":
		return "Commits on " + a.headBranch()
	}
	return "Recent commits on " + a.headBranch()
}

func (a *app) scopeFromID(id string) (gitx.Scope, bool) {
	candidates := []gitx.Scope{{Kind: gitx.ScopeUncommitted}}
	if s, ok := a.allScope(); ok {
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

// scopeHeader names the changes under review, above their review.
func (a *app) scopeHeader(c *ui.Context) {
	t := c.Theme()
	add, del := 0, 0
	for _, f := range a.files {
		add += f.Additions
		del += f.Deletions
	}
	ui.Column(c).Gap(2).Padding(10, 16).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Text(c, a.scopeLabel()).Bold().SingleLine().Tooltip(a.scopeLabel())
		if len(a.files) == 0 {
			return
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, plural(len(a.files), "file")).FontSize(11).TextColor(t.TextMuted)
			ui.Text(c, fmt.Sprintf("+%d", add)).FontSize(11).TextColor(t.Success)
			ui.Text(c, fmt.Sprintf("−%d", del)).FontSize(11).TextColor(t.Danger)
		})
	})
}

func (a *app) reviewNav(c *ui.Context) {
	t := c.Theme()
	page := a.page
	groups := a.groups()
	changed := ui.Sidebar(c, &page, func() {
		sidebarHeading(c, "Review", "")
		ov := ui.SidebarItem(c, "overview", overviewIcon, "Overview")
		if a.overviewRunning() != nil {
			ov.Children(func() { ui.Spinner(c).Size(12, 12) })
		}
		a.walkItems(c, "", 1)
		threads := len(a.rev.Threads)
		ui.SidebarItem(c, "threads", messageIcon, "Questions & comments").Children(func() {
			if threads > 0 {
				ui.Badge(c, fmt.Sprint(threads))
			}
		})
		feedback := len(review.ExportedThreads(a.rev, review.ExportOptions{}))
		ui.SidebarItem(c, "feedback", sendIcon, "Feedback").Children(func() {
			if feedback > 0 {
				ui.Badge(c, fmt.Sprint(feedback))
			}
		})
		viewed := 0
		for _, f := range a.files {
			if a.rev.Viewed[f.Path] {
				viewed++
			}
		}
		detail := ""
		if len(a.files) > 0 {
			detail = fmt.Sprintf("%d of %d viewed", viewed, len(a.files))
		}
		sidebarHeading(c, "Files", detail)
		for gi, g := range groups {
			ui.SidebarSection(c, fmt.Sprintf("%s · %d", g.Title, len(g.Files)), a.groupOpen(g.Title), func() {
				if len(g.Files) > 1 {
					ui.SidebarItem(c, fmt.Sprintf("group:%d", gi), layersIcon, "All files in group")
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
	viewed := a.rev.Viewed[file]
	item := ui.SidebarItem(c, "file:"+file, nil, path.Base(file)).Tooltip(file)
	if viewed && a.page != "file:"+file {
		item.TextColor(t.TextMuted)
	}
	d := a.diffs[file]
	n := len(a.rev.ThreadsOn(file))
	item.Children(func() {
		if n > 0 {
			ui.Badge(c, fmt.Sprint(n))
		}
		if d != nil {
			ui.Row(c).Gap(4).Children(func() {
				if d.change.Additions > 0 {
					ui.Text(c, fmt.Sprintf("+%d", d.change.Additions)).FontSize(11).TextColor(t.Success)
				}
				if d.change.Deletions > 0 {
					ui.Text(c, fmt.Sprintf("−%d", d.change.Deletions)).FontSize(11).TextColor(t.Danger)
				}
			})
		}
		if viewed {
			ui.Icon(c, checkIcon).FontSize(12).TextColor(t.Success).Label("Viewed")
		}
	})
}

func (a *app) main(c *ui.Context) {
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

// age is how long ago t was, in a few characters.
func age(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2006")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
