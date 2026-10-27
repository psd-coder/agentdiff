package main

import (
	"fmt"
	"path/filepath"
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
		ui.Button(c, "Agent log").Tooltip("Every call to the agent: command, prompt, tool calls, tokens (Cmd+L)").OnClick(a.toggleLog)
		ui.Button(c, "Refresh").Tooltip("Read the changes again (Cmd+R)").OnClick(a.refresh)
		ui.Button(c, "Settings").Tooltip("Cmd+,").OnClick(a.openSettings)
		n := len(review.ExportedThreads(a.rev, review.ExportOptions{}))
		ui.PrimaryButton(c, fmt.Sprintf("Feedback (%d)", n)).OnClick(func() { a.goTo("feedback") })
	})
}

// pickerButton is the face of a button that opens a list below it.
func pickerButton(c *ui.Context, svg *ui.SVG, label, detail string) ui.Element {
	t := c.Theme()
	return ui.Button(c, "").Children(func() {
		ui.Icon(c, svg).TextColor(t.TextMuted)
		ui.Text(c, label).Bold().SingleLine()
		if detail != "" {
			ui.Text(c, detail).TextColor(t.TextMuted).SingleLine()
		}
		ui.Icon(c, chevronIcon).FontSize(12).TextColor(t.TextMuted)
	})
}

// menuRow is a row of a picker list: a button that fills the row, with
// actions after it that show while the pointer is over the row.
func menuRow(c *ui.Context, chosen bool, body func(), actions func()) ui.Element {
	t := c.Theme()
	var b ui.Element
	row := ui.Row(c).AlignItems(ui.Center).Radius(t.Radius)
	hovered := row.Hovered()
	if hovered || chosen {
		row.Background(t.Surface)
	}
	row.Children(func() {
		b = ui.ButtonBase(c).Grow(1).Shrink(1).Justify(ui.Start).Gap(8).Padding(6, 8).Children(body)
		if actions != nil {
			bar := ui.Row(c).Gap(2).PaddingEnd(4).Children(actions)
			if !hovered {
				bar.Invisible()
			}
		}
	})
	return b
}

func iconButton(c *ui.Context, svg *ui.SVG, label string) ui.Element {
	return ui.Button(c, "").Padding(4, 6).Label(label).Tooltip(label).Children(func() {
		ui.Icon(c, svg).FontSize(13)
	})
}

func (a *app) projectPicker(c *ui.Context) {
	t := c.Theme()
	anchor := pickerButton(c, folderIcon, a.repo.Name(), "").Tooltip(a.repo.Root).Label("Project").
		OnClick(func() { a.projectsOpen = !a.projectsOpen })
	ui.Popover(c, anchor, &a.projectsOpen, func() {
		ui.Column(c).Width(360).Gap(2).Children(func() {
			ui.Text(c, "Projects").FontSize(11).Bold().TextColor(t.TextMuted).Padding(4, 8)
			for _, p := range a.cfg.Recent {
				current := p == a.repo.Root
				open := menuRow(c.Key(p), current, func() {
					ui.Icon(c, folderIcon).TextColor(t.TextMuted)
					ui.Column(c).Grow(1).Shrink(1).Children(func() {
						ui.Text(c, filepath.Base(p)).Bold().SingleLine()
						ui.Text(c, p).FontSize(11).TextColor(t.TextMuted).SingleLine()
					})
					if current {
						ui.Icon(c, checkIcon).TextColor(t.Accent)
					}
				}, func() {
					iconButton(c, trashIcon, "Remove from the list").OnClick(func() { a.confirmRemoveProject(p) })
				}).Label(filepath.Base(p))
				if open.Clicked() {
					a.projectsOpen = false
					if !current {
						a.openRepo(p)
					}
				}
			}
			ui.Divider(c).Margin(4, 0)
			add := menuRow(c, false, func() {
				ui.Icon(c, plusIcon).TextColor(t.TextMuted)
				ui.Text(c, "Add project…")
			}, nil)
			if add.Clicked() {
				a.projectsOpen = false
				a.chooseRepo()
			}
		})
	})
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
		anchor := pickerButton(c, branchIcon, label, detail).Radius(r, 0, 0, r).Label("Branch").
			Tooltip("Branch to review").OnClick(func() {
			a.branchesOpen = !a.branchesOpen
			a.branchQuery = ""
		})
		iconButton(c, compareIcon, "Compare with another base, or review a custom range").
			PaddingX(9).Radius(0, r, r, 0).MarginStart(-1).OnClick(a.openCompare)
		ui.Popover(c, anchor, &a.branchesOpen, func() { a.branchList(c) })
	})
}

func (a *app) branchList(c *ui.Context) {
	t := c.Theme()
	trees := map[string]string{}
	for _, w := range a.worktrees {
		if w.Branch != "" && w.Path != a.repo.Root {
			trees[w.Branch] = w.Path
		}
	}
	ui.Column(c).Width(340).Gap(4).Children(func() {
		ui.SearchField(c, &a.branchQuery).Placeholder("Filter branches").Label("Filter branches").AutoFocus()
		ui.Scroll(c).MaxHeight(380).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				shown := 0
				for _, b := range a.branches {
					if !strings.Contains(strings.ToLower(b), strings.ToLower(a.branchQuery)) {
						continue
					}
					shown++
					chosen := a.rng == nil && b == a.headBranch()
					tree := trees[b]
					var actions func()
					if tree != "" {
						actions = func() {
							iconButton(c, worktreeIcon, "Open the worktree "+tree).OnClick(func() {
								a.branchesOpen = false
								a.openRepo(tree)
							})
						}
					}
					row := menuRow(c.Key(b), chosen, func() {
						check := ui.Icon(c, checkIcon).TextColor(t.Accent)
						if !chosen {
							check.Invisible()
						}
						ui.Text(c, b).SingleLine().Shrink(1).Grow(1)
						switch {
						case b == a.branch:
							ui.Text(c, "checked out").FontSize(11).TextColor(t.TextMuted)
						case tree != "":
							ui.Text(c, "worktree").FontSize(11).TextColor(t.TextMuted).Tooltip(tree)
						}
					}, actions).Label(b)
					if row.Clicked() {
						a.branchesOpen = false
						a.pickBranch(b)
					}
				}
				if shown == 0 {
					ui.Text(c, "No branches match").FontSize(12).TextColor(t.TextMuted).Padding(6, 8)
				}
			})
		})
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
				ui.Combobox(c, &f.branch, a.branches).Label("Branch")
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
