package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/psd-coder/agentdiff/internal/review"
)

func (a *app) toolbar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(8).Padding(8, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		a.projectPicker(c)
		a.branchPicker(c)
		ui.Spacer(c)
		cfg := a.cfg.Agent
		label := cfg.Provider.Name()
		if cfg.Model != "" {
			label += " · " + cfg.Model
		}
		ui.Text(c, label).FontSize(12).TextColor(t.TextMuted).SingleLine()
		a.cmdButton(c, "agent-log", "", "Agent log")
		a.cmdButton(c, "refresh", "", "Refresh")
		a.cmdButton(c, "settings", "", "Settings")
		n := len(review.ExportedThreads(a.rev, review.ExportOptions{}))
		a.cmdPrimaryButton(c, "go-feedback", "", fmt.Sprintf("Feedback (%d)", n))
	})
}

func (a *app) projectPicker(c *ui.Context) {
	t := c.Theme()
	anchor := pickerButton(c, folderIcon, a.repo.Name(), "", a.projectsOpen).Tooltip(a.repo.Root + "\n" + a.tooltip(a.cmd("switch-project"))).Label("Project")
	if anchor.Clicked() || anchor.Shortcut(0, ui.KeyDown) && !a.projectsOpen {
		a.runOn(c, "switch-project", "")
	}
	a.takeFocus(anchor, "project")
	ui.Popover(c, anchor, &a.projectsOpen, func() {
		recent := a.cfg.Recent
		choose := func(i int) {
			a.projectsOpen, a.refocus = false, "project"
			switch {
			case i == len(recent):
				a.chooseRepo()
			case recent[i] != a.repo.Root:
				a.openRepo(recent[i])
			}
		}
		ui.Column(c).MinWidth(360).MaxWidth(480).FillWidth().Gap(2).Children(func() {
			ui.Text(c, "Projects").FontSize(11).Bold().TextColor(t.TextMuted).Padding(4, 8)
			a.projectList(c, &a.projectNav, a.repo.Root, true, choose)
		})
	})
}

// projectList lists the recent projects, open marked as open, and with
// add, a last row to add one. Up, Down, Home and End move the highlight,
// Return or Space calls choose with its index, len(cfg.Recent) for the
// add row, and Cmd+Backspace or Delete removes the highlighted project.
func (a *app) projectList(c *ui.Context, nav *pickList, open string, add bool, choose func(i int)) {
	t := c.Theme()
	recent := a.cfg.Recent
	n := len(recent)
	if add {
		n++
	}
	l, rows := nav.list(c, "Projects", func(addRow pickRow) {
		for i, p := range recent {
			row := addRow(c.Key(p), func() {
				ui.Icon(c, folderIcon).Size(menuIcon, menuIcon).TextColor(t.TextMuted)
				ui.Column(c).Grow(1).Shrink(1).Children(func() {
					ui.Text(c, filepath.Base(p)).Bold().SingleLine()
					ui.Text(c, p).FontSize(11).TextColor(t.TextMuted).SingleLine()
				})
				if p == open {
					ui.Icon(c, checkIcon).TextColor(t.Accent).Label("Open")
				}
			}, func() {
				iconButton(c, trashIcon, "Remove from the list", "Remove from the list (Cmd+Backspace)").OnClick(func() { a.confirmRemoveProject(p) })
			}).Label(filepath.Base(p) + ", " + p)
			if row.Clicked() {
				choose(i)
			}
		}
		if add {
			ui.Divider(c).Margin(4, 0)
			row := addRow(c, func() {
				ui.Icon(c, plusIcon).Size(menuIcon, menuIcon).TextColor(t.TextMuted)
				ui.Text(c, "Add project…")
			}, nil).Label("Add project…")
			if row.Clicked() {
				choose(len(recent))
			}
		}
	})
	l.Focusable().AutoFocus()
	a.takeFocus(l, "switch-project")
	for _, k := range []ui.Key{ui.KeyDown, ui.KeyUp, ui.KeyPageDown, ui.KeyPageUp} {
		if l.Shortcut(0, k) {
			nav.key(k, n)
		}
	}
	switch {
	case l.Shortcut(0, ui.KeyHome):
		nav.open(0)
	case l.Shortcut(0, ui.KeyEnd):
		nav.open(n - 1)
	case l.Shortcut(0, ui.KeyEnter) || l.Shortcut(0, ui.KeySpace):
		if nav.hi < n {
			choose(nav.hi)
		}
	case l.Shortcut(ui.Cmd, ui.KeyBackspace) || l.Shortcut(0, ui.KeyDelete):
		if nav.hi < len(recent) {
			a.confirmRemoveProject(recent[nav.hi])
		}
	}
	if row, ok := nav.active(rows); ok {
		l.ActiveDescendant(row)
	}
}

func (a *app) toggleProjects() {
	a.projectsOpen = !a.projectsOpen
	a.projectNav.open(slices.Index(a.cfg.Recent, a.repo.Root))
}

func (a *app) confirmRemoveProject(path string) {
	detail := "AgentDiff forgets the folder. Its saved reviews stay and come back when you open it again."
	a.confirm("Remove "+filepath.Base(path)+" from the list?", detail, "Remove", func() {
		a.cfg.RemoveRecent(path)
		a.saveConfig()
		if a.repo != nil && path == a.repo.Root {
			a.closeRepo()
		}
	})
}

func (a *app) branchPicker(c *ui.Context) {
	t := c.Theme()
	label, detail := a.cmpr.label()
	r := t.Radius
	ui.Row(c).AlignItems(ui.Stretch).Children(func() {
		anchor := pickerButton(c, branchIcon, label, detail, a.branchesOpen).Radius(r, 0, 0, r).Label("Branch").Tooltip(a.tooltip(a.cmd("switch-branch")))
		if anchor.Clicked() || anchor.Shortcut(0, ui.KeyDown) && !a.branchesOpen {
			a.runOn(c, "switch-branch", "")
		}
		a.takeFocus(anchor, "branch")
		compare := a.cmd("compare")
		iconButton(c, compareIcon, compare.tip, a.tooltip(compare)).
			PaddingX(9).Radius(0, r, r, 0).MarginStart(-1).OnClick(func() { a.runOn(c, "compare", "") })
		ui.Popover(c, anchor, &a.branchesOpen, func() { a.branchList(c) })
	})
}

func (a *app) toggleBranches() {
	a.branchesOpen = !a.branchesOpen
	a.branchQuery = ""
	a.branchNav.open(slices.Index(a.cmpr.branches, a.cmpr.headBranch()))
}

func (a *app) branchList(c *ui.Context) {
	rows := a.cmpr.branchRows(a.branchQuery, a.repo.Root)
	nav := &a.branchNav
	choose := func(b string) {
		a.branchesOpen, a.refocus = false, "branch"
		a.pickBranch(b)
	}
	openTree := func(tree string) {
		a.branchesOpen = false
		a.openRepo(tree)
	}
	ui.Column(c).MinWidth(340).MaxWidth(560).FillWidth().Gap(4).Children(func() {
		in := searchField(c, &a.branchQuery, "Filter branches", func(k ui.TextKey) bool {
			switch {
			case k.Mods == 0 && nav.key(k.Key, len(rows)):
				return true
			case k.Key == ui.KeyEnter && nav.hi < len(rows):
				r := rows[nav.hi]
				if k.Mods == ui.Cmd && r.tree != "" {
					openTree(r.tree)
				} else if k.Mods == 0 {
					choose(r.name)
				}
				return true
			}
			return false
		})
		a.takeFocus(in, "switch-branch")
		if in.Changed() {
			nav.open(0)
		}
		var items []ui.Element
		ui.Scroll(c).MaxHeight(380).Children(func() {
			_, items = nav.list(c, "Branches", func(addRow pickRow) {
				for _, r := range rows {
					if branchItem(c, addRow, r, openTree).Clicked() {
						choose(r.name)
					}
				}
			})
			a.branchListNote(c, len(rows))
		})
		if item, ok := nav.active(items); ok && len(rows) > 0 {
			in.ActiveDescendant(item)
		}
	})
}

func branchItem(c *ui.Context, addRow pickRow, r branchRow, openTree func(string)) ui.Element {
	t := c.Theme()
	var actions func()
	if r.tree != "" {
		actions = func() {
			iconButton(c, worktreeIcon, "Open the worktree "+r.tree, "Open the worktree "+r.tree+" (Cmd+Enter)").OnClick(func() { openTree(r.tree) })
		}
	}
	return addRow(c.Key(r.name), func() {
		check := ui.Icon(c, checkIcon).Size(menuIcon, menuIcon).TextColor(t.Accent)
		if !r.chosen {
			check.Invisible()
		}
		ui.Text(c, r.name).SingleLine().Shrink(1).Grow(1)
		if tag := r.tag(); tag != "" {
			text := ui.Text(c, tag).FontSize(11).TextColor(t.TextMuted)
			if !r.checkedOut {
				text.Tooltip(r.tree)
			}
		}
	}, actions).Label(r.label())
}

// branchListNote tells why the branch list shows no branches, or that git
// could not list them.
func (a *app) branchListNote(c *ui.Context, shown int) {
	t := c.Theme()
	switch {
	case a.cmpr.err != "":
		ui.Text(c, "Could not list the branches: "+a.cmpr.err).FontSize(12).TextColor(t.Danger).Padding(6, 8).Selectable()
	case shown == 0:
		ui.Text(c, "No branches match").FontSize(12).TextColor(t.TextMuted).Padding(6, 8)
	}
}

type compareForm struct {
	open         bool
	mode         int
	branch, base string
	from, to     string
}

const (
	compareBranches = iota
	compareRange
)

func (a *app) openCompare() {
	f := &compareForm{open: true, mode: compareBranches, branch: a.cmpr.headBranch(), base: a.cmpr.base, to: "HEAD"}
	if a.cmpr.rng != nil {
		f.mode, f.from, f.to = compareRange, a.cmpr.rng.From, a.cmpr.rng.To
	} else if base := a.cmpr.commitBase(); base != "" {
		f.from = base
	}
	a.compareForm = f
}

func (a *app) compareModal(c *ui.Context) {
	f := a.compareForm
	if f == nil {
		return
	}
	t := c.Theme()
	branchesOK := f.branch != "" && f.base != "" && f.branch != f.base
	from, to := strings.TrimSpace(f.from), strings.TrimSpace(f.to)
	rangeOK := from != "" && to != ""
	apply := func() {
		switch {
		case f.mode == compareBranches && branchesOK:
			f.open = false
			a.compare(f.branch, f.base)
		case f.mode == compareRange && rangeOK:
			f.open = false
			a.setRange(from, to)
		}
	}
	ui.Modal(c, &f.open, func() {
		ui.Column(c).Width(460).Gap(12).Children(func() {
			ui.Text(c, "Compare").FontSize(18).Bold()
			ui.Segmented(c, &f.mode, "Branches", "Range").Label("Compare")
			if f.mode == compareBranches {
				ui.Text(c, "Review the commits of a branch since it left its base. The branch does not have to be checked out.").FontSize(12).TextColor(t.TextMuted)
				ui.Text(c, "Branch").FontSize(12).Bold()
				a.takeFocus(ui.Combobox(c.Key("branch"), &f.branch, a.cmpr.branches).Label("Branch").AutoFocus(), "compare")
				ui.Text(c, "Base").FontSize(12).Bold()
				ui.Combobox(c.Key("base"), &f.base, a.cmpr.branches).Label("Base")
				if f.branch != "" && f.branch == f.base {
					ui.Text(c, "Pick two different branches.").FontSize(12).TextColor(t.Danger)
				}
			} else {
				ui.Text(c, "Review the changes between two commits, branches or tags, as git diff from..to shows them.").FontSize(12).TextColor(t.TextMuted)
				a.takeFocus(ui.TextInput(c.Key("from"), &f.from).Placeholder("From, e.g. main or abc1234").Label("From").AutoFocus().OnSubmit(apply), "compare")
				ui.TextInput(c.Key("to"), &f.to).Placeholder("To, e.g. HEAD").Label("To").OnSubmit(apply)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, "Cancel").OnClick(func() { f.open = false })
				ok := branchesOK
				if f.mode == compareRange {
					ok = rangeOK
				}
				ui.PrimaryButton(c, "Show changes").Disabled(!ok).OnClick(apply)
			})
		})
	})
	if !f.open {
		a.compareForm = nil
	}
}
