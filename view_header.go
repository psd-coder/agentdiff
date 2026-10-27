package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/psdcoder/agentdiff/internal/review"
)

func (a *app) toolbar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(8).Padding(8, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		a.projectPicker(c)
		a.branchPicker(c)
		ui.Spacer(c)
		cfg, _ := a.cfg.Effective(a.rev.Settings)
		label := providerName(cfg.Provider)
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
	anchor := pickerButton(c, folderIcon, a.repo.Name(), "", a.projectsOpen).Tooltip(a.repo.Root + " (" + keyLabel(a.cmd("switch-project").keys) + ")").Label("Project")
	if anchor.Clicked() || anchor.Shortcut(0, ui.KeyDown) && !a.projectsOpen {
		a.runOn(c, "switch-project", "")
	}
	if a.refocus == "project" {
		anchor.Focus()
		a.refocus = ""
	}
	ui.Popover(c, anchor, &a.projectsOpen, func() {
		recent := a.cfg.Recent
		n := len(recent) + 1
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
			nav := &a.projectNav
			l := nav.list(c, "Projects", func() {
				for i, p := range recent {
					current := p == a.repo.Root
					row := nav.row(c.Key(p), func() {
						ui.Icon(c, folderIcon).Size(menuIcon, menuIcon).TextColor(t.TextMuted)
						ui.Column(c).Grow(1).Shrink(1).Children(func() {
							ui.Text(c, filepath.Base(p)).Bold().SingleLine()
							ui.Text(c, p).FontSize(11).TextColor(t.TextMuted).SingleLine()
						})
						if current {
							ui.Icon(c, checkIcon).TextColor(t.Accent).Label("Open")
						}
					}, func() {
						iconButton(c, trashIcon, "Remove from the list").Tooltip("Remove from the list (Cmd+Backspace)").OnClick(func() { a.confirmRemoveProject(p) })
					}).Label(filepath.Base(p) + ", " + p)
					if row.Clicked() {
						choose(i)
					}
				}
				ui.Divider(c).Margin(4, 0)
				add := nav.row(c, func() {
					ui.Icon(c, plusIcon).Size(menuIcon, menuIcon).TextColor(t.TextMuted)
					ui.Text(c, "Add project…")
				}, nil).Label("Add project…")
				if add.Clicked() {
					choose(len(recent))
				}
			}).Focusable().AutoFocus()
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
				choose(nav.hi)
			case l.Shortcut(ui.Cmd, ui.KeyBackspace) || l.Shortcut(0, ui.KeyDelete):
				if nav.hi < len(recent) {
					a.confirmRemoveProject(recent[nav.hi])
				}
			}
			if row, ok := nav.active(); ok {
				l.ActiveDescendant(row)
			}
		})
	})
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

// branchLabel names what the commit list shows: the branch and its
// base, or the custom range.
func (a *app) branchLabel() (string, string) {
	if a.rng != nil {
		return a.rng.Label(), ""
	}
	if base := a.commitBase(); base != "" {
		return a.headBranch(), "vs " + base
	}
	return a.headBranch(), ""
}

func (a *app) branchPicker(c *ui.Context) {
	t := c.Theme()
	label, detail := a.branchLabel()
	r := t.Radius
	ui.Row(c).AlignItems(ui.Stretch).Children(func() {
		anchor := pickerButton(c, branchIcon, label, detail, a.branchesOpen).Radius(r, 0, 0, r).Label("Branch").Tooltip("Branch to review (" + keyLabel(a.cmd("switch-branch").keys) + ")")
		if anchor.Clicked() || anchor.Shortcut(0, ui.KeyDown) && !a.branchesOpen {
			a.runOn(c, "switch-branch", "")
		}
		if a.refocus == "branch" {
			anchor.Focus()
			a.refocus = ""
		}
		compare := a.cmd("compare")
		iconButton(c, compareIcon, compare.tip).Tooltip(compare.tooltip()).
			PaddingX(9).Radius(0, r, r, 0).MarginStart(-1).OnClick(func() { a.runOn(c, "compare", "") })
		ui.Popover(c, anchor, &a.branchesOpen, func() { a.branchList(c) })
	})
}

func (a *app) toggleBranches() {
	a.branchesOpen = !a.branchesOpen
	a.branchQuery = ""
	a.branchNav.open(slices.Index(a.branches, a.headBranch()))
}

// filteredBranches are the branches whose name holds the filter.
func (a *app) filteredBranches() []string {
	q := strings.ToLower(a.branchQuery)
	var out []string
	for _, b := range a.branches {
		if strings.Contains(strings.ToLower(b), q) {
			out = append(out, b)
		}
	}
	return out
}

func (a *app) branchList(c *ui.Context) {
	t := c.Theme()
	trees := map[string]string{}
	for _, w := range a.worktrees {
		if w.Branch != "" && w.Path != a.repo.Root {
			trees[w.Branch] = w.Path
		}
	}
	branches := a.filteredBranches()
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
			case k.Mods == 0 && nav.key(k.Key, len(branches)):
				return true
			case k.Key == ui.KeyEnter && nav.hi < len(branches):
				b := branches[nav.hi]
				if tree := trees[b]; k.Mods == ui.Cmd && tree != "" {
					openTree(tree)
				} else if k.Mods == 0 {
					choose(b)
				}
				return true
			}
			return false
		})
		if in.Changed() {
			nav.open(0)
		}
		ui.Scroll(c).MaxHeight(380).Children(func() {
			nav.list(c, "Branches", func() {
				for _, b := range branches {
					chosen := a.rng == nil && b == a.headBranch()
					tree := trees[b]
					var actions func()
					if tree != "" {
						actions = func() {
							iconButton(c, worktreeIcon, "Open the worktree "+tree).Tooltip("Open the worktree " + tree + " (Cmd+Enter)").OnClick(func() { openTree(tree) })
						}
					}
					label := b
					row := nav.row(c.Key(b), func() {
						check := ui.Icon(c, checkIcon).Size(menuIcon, menuIcon).TextColor(t.Accent)
						if !chosen {
							check.Invisible()
						}
						ui.Text(c, b).SingleLine().Shrink(1).Grow(1)
						switch {
						case b == a.branch:
							label += ", checked out"
							ui.Text(c, "checked out").FontSize(11).TextColor(t.TextMuted)
						case tree != "":
							label += ", worktree"
							ui.Text(c, "worktree").FontSize(11).TextColor(t.TextMuted).Tooltip(tree)
						}
						if chosen {
							label += ", selected"
						}
					}, actions)
					row.Label(label)
					if row.Clicked() {
						choose(b)
					}
				}
			})
			if len(branches) == 0 {
				ui.Text(c, "No branches match").FontSize(12).TextColor(t.TextMuted).Padding(6, 8)
			}
		})
		if row, ok := nav.active(); ok && len(branches) > 0 {
			in.ActiveDescendant(row)
		}
	})
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
	f := &compareForm{open: true, mode: compareBranches, branch: a.headBranch(), base: a.base, to: "HEAD"}
	if a.rng != nil {
		f.mode, f.from, f.to = compareRange, a.rng.From, a.rng.To
	} else if base := a.commitBase(); base != "" {
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
				ui.Combobox(c, &f.branch, a.branches).Label("Branch").AutoFocus()
				ui.Text(c, "Base").FontSize(12).Bold()
				ui.Combobox(c, &f.base, a.branches).Label("Base")
				if f.branch != "" && f.branch == f.base {
					ui.Text(c, "Pick two different branches.").FontSize(12).TextColor(t.Danger)
				}
			} else {
				ui.Text(c, "Review the changes between two commits, branches or tags, as git diff from..to shows them.").FontSize(12).TextColor(t.TextMuted)
				ui.TextInput(c, &f.from).Placeholder("From, e.g. main or abc1234").Label("From").AutoFocus().OnSubmit(apply)
				ui.TextInput(c, &f.to).Placeholder("To, e.g. HEAD").Label("To").OnSubmit(apply)
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
