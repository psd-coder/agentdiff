package main

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
)

func (a *app) view(c *ui.Context) {
	a.syncCodeTheme(c)
	a.runQueued(c)
	a.updateMenu()
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
	a.shortcutsModal(c)
	a.paletteModal(c)
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
			a.fileNav(c)
		})
	}).Fill()
}

// sidebarHeading names a part of a sidebar, above its sections, with the
// controls end builds after the title, if any.
func sidebarHeading(c *ui.Context, title string, end func()) {
	t := c.Theme()
	ui.Row(c).Gap(6).Padding(10, 8, 4, 8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, strings.ToUpper(title)).FontSize(10).Bold().LetterSpacing(0.6).TextColor(t.TextMuted).Grow(1).SingleLine()
		if end != nil {
			end()
		}
	})
}

func (a *app) scopeList(c *ui.Context) {
	t := c.Theme()
	choice := a.scopeID
	list := ui.Sidebar(c, &choice, func() {
		sidebarHeading(c, "Changes", nil)
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
	}).Fill().Label("Changes")
	a.takeFocus(list, "changes")
	if !list.Changed() || choice == a.scopeID {
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
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.scopeLabel()).Bold().SingleLine().Shrink(1).Tooltip(a.scopeLabel())
			if a.switching {
				ui.Spinner(c).Size(12, 12).Label("Reading changes")
			}
		})
		if len(a.files) == 0 {
			return
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, plural(len(a.files), "file")).FontSize(11).TextColor(t.TextMuted)
			ui.Text(c, fmt.Sprintf("+%d", add)).FontSize(11).TextColor(t.Success)
			ui.Text(c, fmt.Sprintf("−%d", del)).FontSize(11).TextColor(t.Danger)
			viewed := 0
			for _, f := range a.files {
				if a.rev.Viewed[f.Path] {
					viewed++
				}
			}
			ui.Text(c, fmt.Sprintf("%d of %d viewed", viewed, len(a.files))).FontSize(11).TextColor(t.TextMuted)
		})
	})
}

func (a *app) reviewNav(c *ui.Context) {
	page := a.page
	nav := ui.Sidebar(c, &page, func() {
		sidebarHeading(c, "Review", nil)
		ov := ui.SidebarItem(c, "overview", overviewIcon, "Overview")
		if a.overviewRunning() != nil {
			ov.Children(func() { ui.Spinner(c).Size(12, 12) })
		}
		a.walkItems(c, "", 1)
		ui.SidebarItem(c, "all", filesIcon, "All files diff")
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
	}).Shrink(0).Label("Review")
	if nav.Changed() {
		a.goTo(page)
	}
}

// fileNav lists the files, by path or in their groups, under the switches
// that choose how.
func (a *app) fileNav(c *ui.Context) {
	t := c.Theme()
	page := a.page
	groups := a.groups()
	ui.Column(c).Padding(0, 10).Children(func() {
		sidebarHeading(c, "Files", func() {
			a.cmdSegmented(c, []string{"flat-files", "grouped-files"}, "Flat", "Grouped").Label("List files").FontSize(11)
		})
		ui.Row(c).Padding(0, 8, 4, 8).Children(func() {
			a.cmdCheckbox(c, "hide-viewed", "show-viewed", "", "Hide viewed files").FontSize(12)
		})
	})
	nav := ui.Sidebar(c, &page, func() {
		if a.cfg.FlatFiles {
			for _, f := range a.files {
				if a.listed(f.Path) {
					a.fileItem(c, f.Path)
				}
			}
		}
		for gi, g := range groups {
			if a.cfg.FlatFiles {
				break
			}
			files := slices.DeleteFunc(slices.Clone(g.Files), func(f string) bool { return !a.listed(f) })
			if len(files) == 0 {
				continue
			}
			open := a.groupOpen(g.Title)
			column := ui.Column(c.Key(g.Title)).Gap(2)
			if gi > 0 {
				column.Margin(8, 0, 0, 0)
			}
			column.Children(func() {
				a.groupItem(c, gi, g.Title, len(files), open)
				if *open {
					for _, f := range files {
						a.fileItem(c, f).Level(2)
					}
				}
			})
		}
		if len(a.files) > 0 && !slices.ContainsFunc(a.files, func(f gitx.FileChange) bool { return a.listed(f.Path) }) {
			ui.Text(c, "All files are viewed.").FontSize(12).TextColor(t.TextMuted).Padding(4, 12)
		}
		if a.loading {
			ui.Row(c).Gap(8).Padding(8, 12).Children(func() {
				ui.Spinner(c).Size(12, 12)
				ui.Text(c, "Reading changes…").FontSize(12).TextColor(t.TextMuted)
			})
		}
	}).Grow(1).Shrink(1).MinHeight(0).Label("Files")
	a.takeFocus(nav, "files")
	// Left and Right close and open the group chosen, as in a tree.
	if gi, ok := strings.CutPrefix(page, "group:"); ok {
		var i int
		fmt.Sscanf(gi, "%d", &i)
		if i < len(groups) {
			open := a.groupOpen(groups[i].Title)
			switch {
			case *open && nav.Shortcut(0, ui.KeyLeft):
				*open = false
			case !*open && nav.Shortcut(0, ui.KeyRight):
				*open = true
			}
		}
	}
	if nav.Changed() {
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

// groupItem is the title of a group in the sidebar. Choosing it opens
// the group page, and its chevron shows and hides the group's files.
func (a *app) groupItem(c *ui.Context, gi int, title string, files int, open *bool) {
	t := c.Theme()
	ui.SidebarItem(c, fmt.Sprintf("group:%d", gi), nil, title).Tooltip(title).FontSize(12).Bold().Expanded(*open).Children(func() {
		ui.Text(c, fmt.Sprint(files)).FontSize(11).TextColor(t.TextMuted)
		label := "Hide files"
		if !*open {
			label = "Show files"
		}
		btn := ui.ButtonBase(c).Padding(2).Radius(4).Label(label).Tooltip(label).Children(func() {
			arrow := ui.Icon(c, chevronIcon).Size(12, 12).TextColor(t.TextMuted)
			if !*open {
				arrow.Rotate(-90)
			}
		})
		if btn.Hovered() {
			btn.Background(t.Text.Alpha(0.1))
		}
		if btn.Clicked() {
			*open = !*open
		}
	})
}

func (a *app) fileItem(c *ui.Context, file string) ui.Element {
	t := c.Theme()
	viewed := a.rev.Viewed[file]
	item := ui.SidebarItem(c, "file:"+file, nil, path.Base(file)).Tooltip(file)
	dir := path.Dir(file)
	if viewed && a.page != "file:"+file {
		item.TextColor(t.TextMuted)
	}
	d := a.diffs[file]
	n := len(a.rev.ThreadsOn(file))
	item.Children(func() {
		if a.cfg.FlatFiles && dir != "." {
			ui.Text(c, dir).FontSize(11).TextColor(t.TextMuted).SingleLine().Shrink(1)
		}
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
	return item
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
		case a.page == "all":
			a.allPage(c)
		case strings.HasPrefix(a.page, "archived:"):
			a.archivedPage(c, strings.TrimPrefix(a.page, "archived:"))
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
