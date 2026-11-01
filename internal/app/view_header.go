package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/psd-coder/agentdiff/internal/review"
)

func (app *app) toolbar(ctx *ui.Context) {
	theme := ctx.Theme()
	ui.Row(ctx).Gap(8).Padding(8, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
		app.projectPicker(ctx)
		app.branchPicker(ctx)
		ui.Spacer(ctx)
		cfg := app.cfg.Agent
		label := cfg.Provider.Name()
		if cfg.Model != "" {
			label += " · " + cfg.Model
		}
		ui.Text(ctx, label).FontSize(12).TextColor(theme.TextMuted).SingleLine()
		app.cmdButton(ctx, "agent-log", "", "Agent log")
		app.cmdButton(ctx, "refresh", "", "Refresh")
		app.cmdButton(ctx, "settings", "", "Settings")
		feedbackCount := len(review.ExportedThreads(app.rev, review.ExportOptions{}))
		app.cmdPrimaryButton(ctx, "go-feedback", "", fmt.Sprintf("Feedback (%d)", feedbackCount))
	})
}

func (app *app) projectPicker(ctx *ui.Context) {
	theme := ctx.Theme()
	anchor := pickerButton(ctx, folderIcon, app.repo.Name(), "", app.projectsOpen).Tooltip(app.repo.Root + "\n" + app.tooltip(app.cmd("switch-project"))).Label("Project")
	if anchor.Clicked() || anchor.Shortcut(0, ui.KeyDown) && !app.projectsOpen {
		app.runOn(ctx, "switch-project", "")
	}
	app.takeFocus(anchor, "project")
	ui.Popover(ctx, anchor, &app.projectsOpen, func() {
		recent := app.cfg.Recent
		choose := func(i int) {
			app.projectsOpen, app.refocus = false, "project"
			switch {
			case i == len(recent):
				app.chooseRepo()
			case recent[i] != app.repo.Root:
				app.openRepo(recent[i])
			}
		}
		ui.Column(ctx).MinWidth(360).MaxWidth(480).FillWidth().Gap(2).Children(func() {
			ui.Text(ctx, "Projects").FontSize(11).Bold().TextColor(theme.TextMuted).Padding(4, 8)
			app.projectList(ctx, &app.projectNav, app.repo.Root, true, choose)
		})
	})
}

// projectList lists the recent projects, open marked as open, and with
// add, a last row to add one. Up, Down, Home and End move the highlight,
// Return or Space calls choose with its index, len(cfg.Recent) for the
// add row, and Cmd+Backspace or Delete removes the highlighted project.
func (app *app) projectList(ctx *ui.Context, nav *pickList, open string, add bool, choose func(index int)) {
	theme := ctx.Theme()
	recent := app.cfg.Recent
	rowCount := len(recent)
	if add {
		rowCount++
	}
	list, rows := nav.list(ctx, "Projects", func(addRow pickRow) {
		for i, p := range recent {
			row := addRow(ctx.Key(p), func() {
				ui.Icon(ctx, folderIcon).Size(menuIcon, menuIcon).TextColor(theme.TextMuted)
				ui.Column(ctx).Grow(1).Shrink(1).Children(func() {
					ui.Text(ctx, filepath.Base(p)).Bold().SingleLine()
					ui.Text(ctx, p).FontSize(11).TextColor(theme.TextMuted).SingleLine()
				})
				if p == open {
					ui.Icon(ctx, checkIcon).TextColor(theme.Accent).Label("Open")
				}
			}, func() {
				iconButton(ctx, trashIcon, "Remove from the list", "Remove from the list (Cmd+Backspace)").OnClick(func() { app.confirmRemoveProject(p) })
			}).Label(filepath.Base(p) + ", " + p)
			if row.Clicked() {
				choose(i)
			}
		}
		if add {
			ui.Divider(ctx).Margin(4, 0)
			row := addRow(ctx, func() {
				ui.Icon(ctx, plusIcon).Size(menuIcon, menuIcon).TextColor(theme.TextMuted)
				ui.Text(ctx, "Add project…")
			}, nil).Label("Add project…")
			if row.Clicked() {
				choose(len(recent))
			}
		}
	})
	list.Focusable().AutoFocus()
	app.takeFocus(list, "switch-project")
	for _, k := range []ui.Key{ui.KeyDown, ui.KeyUp, ui.KeyPageDown, ui.KeyPageUp} {
		if list.Shortcut(0, k) {
			nav.key(k, rowCount)
		}
	}
	switch {
	case list.Shortcut(0, ui.KeyHome):
		nav.open(0)
	case list.Shortcut(0, ui.KeyEnd):
		nav.open(rowCount - 1)
	case list.Shortcut(0, ui.KeyEnter) || list.Shortcut(0, ui.KeySpace):
		if nav.hi < rowCount {
			choose(nav.hi)
		}
	case list.Shortcut(ui.Cmd, ui.KeyBackspace) || list.Shortcut(0, ui.KeyDelete):
		if nav.hi < len(recent) {
			app.confirmRemoveProject(recent[nav.hi])
		}
	}
	if row, ok := nav.active(rows); ok {
		list.ActiveDescendant(row)
	}
}

func (app *app) toggleProjects() {
	app.projectsOpen = !app.projectsOpen
	app.projectNav.open(slices.Index(app.cfg.Recent, app.repo.Root))
}

func (app *app) confirmRemoveProject(path string) {
	detail := "AgentDiff forgets the folder. Its saved reviews stay and come back when you open it again."
	app.confirm("Remove "+filepath.Base(path)+" from the list?", detail, "Remove", func() {
		app.cfg.RemoveRecent(path)
		app.saveConfig()
		if app.repo != nil && path == app.repo.Root {
			app.closeRepo()
		}
	})
}

func (app *app) branchPicker(ctx *ui.Context) {
	theme := ctx.Theme()
	label, detail := app.cmpr.label()
	radius := theme.Radius
	ui.Row(ctx).AlignItems(ui.Stretch).Children(func() {
		anchor := pickerButton(ctx, branchIcon, label, detail, app.branchesOpen).Radius(radius, 0, 0, radius).Label("Branch").Tooltip(app.tooltip(app.cmd("switch-branch")))
		if anchor.Clicked() || anchor.Shortcut(0, ui.KeyDown) && !app.branchesOpen {
			app.runOn(ctx, "switch-branch", "")
		}
		app.takeFocus(anchor, "branch")
		compare := app.cmd("compare")
		iconButton(ctx, compareIcon, compare.tip, app.tooltip(compare)).
			PaddingX(9).Radius(0, radius, radius, 0).MarginStart(-1).OnClick(func() { app.runOn(ctx, "compare", "") })
		ui.Popover(ctx, anchor, &app.branchesOpen, func() { app.branchList(ctx) })
	})
}

func (app *app) toggleBranches() {
	app.branchesOpen = !app.branchesOpen
	app.branchQuery = ""
	app.branchNav.open(slices.Index(app.cmpr.branches, app.cmpr.headBranch()))
}

func (app *app) branchList(ctx *ui.Context) {
	rows := app.cmpr.branchRows(app.branchQuery, app.repo.Root)
	nav := &app.branchNav
	choose := func(b string) {
		app.branchesOpen, app.refocus = false, "branch"
		app.pickBranch(b)
	}
	openTree := func(tree string) {
		app.branchesOpen = false
		app.openRepo(tree)
	}
	ui.Column(ctx).MinWidth(340).MaxWidth(560).FillWidth().Gap(4).Children(func() {
		in := searchField(ctx, &app.branchQuery, "Filter branches", func(k ui.TextKey) bool {
			switch {
			case k.Mods == 0 && nav.key(k.Key, len(rows)):
				return true
			case k.Key == ui.KeyEnter && nav.hi < len(rows):
				row := rows[nav.hi]
				if k.Mods == ui.Cmd && row.tree != "" {
					openTree(row.tree)
				} else if k.Mods == 0 {
					choose(row.name)
				}
				return true
			}
			return false
		})
		app.takeFocus(in, "switch-branch")
		if in.Changed() {
			nav.open(0)
		}
		var items []ui.Element
		ui.Scroll(ctx).MaxHeight(380).Children(func() {
			_, items = nav.list(ctx, "Branches", func(addRow pickRow) {
				for _, r := range rows {
					if branchItem(ctx, addRow, r, openTree).Clicked() {
						choose(r.name)
					}
				}
			})
			app.branchListNote(ctx, len(rows))
		})
		if item, ok := nav.active(items); ok && len(rows) > 0 {
			in.ActiveDescendant(item)
		}
	})
}

func branchItem(ctx *ui.Context, addRow pickRow, row branchRow, openTree func(string)) ui.Element {
	theme := ctx.Theme()
	var actions func()
	if row.tree != "" {
		actions = func() {
			iconButton(ctx, worktreeIcon, "Open the worktree "+row.tree, "Open the worktree "+row.tree+" (Cmd+Enter)").OnClick(func() { openTree(row.tree) })
		}
	}
	return addRow(ctx.Key(row.name), func() {
		check := ui.Icon(ctx, checkIcon).Size(menuIcon, menuIcon).TextColor(theme.Accent)
		if !row.chosen {
			check.Invisible()
		}
		ui.Text(ctx, row.name).SingleLine().Shrink(1).Grow(1)
		if tag := row.tag(); tag != "" {
			text := ui.Text(ctx, tag).FontSize(11).TextColor(theme.TextMuted)
			if !row.checkedOut {
				text.Tooltip(row.tree)
			}
		}
	}, actions).Label(row.label())
}

// branchListNote tells why the branch list shows no branches, or that git
// could not list them.
func (app *app) branchListNote(ctx *ui.Context, shown int) {
	theme := ctx.Theme()
	switch {
	case app.cmpr.err != "":
		ui.Text(ctx, "Could not list the branches: "+app.cmpr.err).FontSize(12).TextColor(theme.Danger).Padding(6, 8).Selectable()
	case shown == 0:
		ui.Text(ctx, "No branches match").FontSize(12).TextColor(theme.TextMuted).Padding(6, 8)
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

func (app *app) openCompare() {
	form := &compareForm{open: true, mode: compareBranches, branch: app.cmpr.headBranch(), base: app.cmpr.base, to: "HEAD"}
	if app.cmpr.rng != nil {
		form.mode, form.from, form.to = compareRange, app.cmpr.rng.From, app.cmpr.rng.To
	} else if base := app.cmpr.commitBase(); base != "" {
		form.from = base
	}
	app.compareForm = form
}

func (app *app) compareModal(ctx *ui.Context) {
	form := app.compareForm
	if form == nil {
		return
	}
	theme := ctx.Theme()
	branchesOK := form.branch != "" && form.base != "" && form.branch != form.base
	from, to := strings.TrimSpace(form.from), strings.TrimSpace(form.to)
	rangeOK := from != "" && to != ""
	apply := func() {
		switch {
		case form.mode == compareBranches && branchesOK:
			form.open = false
			app.compare(form.branch, form.base)
		case form.mode == compareRange && rangeOK:
			form.open = false
			app.setRange(from, to)
		}
	}
	ui.Modal(ctx, &form.open, func() {
		ui.Column(ctx).Width(460).Gap(12).Children(func() {
			ui.Text(ctx, "Compare").FontSize(18).Bold()
			ui.Segmented(ctx, &form.mode, "Branches", "Range").Label("Compare")
			if form.mode == compareBranches {
				ui.Text(ctx, "Review the commits of a branch since it left its base. The branch does not have to be checked out.").FontSize(12).TextColor(theme.TextMuted)
				ui.Text(ctx, "Branch").FontSize(12).Bold()
				app.takeFocus(ui.Combobox(ctx.Key("branch"), &form.branch, app.cmpr.branches).Label("Branch").AutoFocus(), "compare")
				ui.Text(ctx, "Base").FontSize(12).Bold()
				ui.Combobox(ctx.Key("base"), &form.base, app.cmpr.branches).Label("Base")
				if form.branch != "" && form.branch == form.base {
					ui.Text(ctx, "Pick two different branches.").FontSize(12).TextColor(theme.Danger)
				}
			} else {
				ui.Text(ctx, "Review the changes between two commits, branches or tags, as git diff from..to shows them.").FontSize(12).TextColor(theme.TextMuted)
				app.takeFocus(ui.TextInput(ctx.Key("from"), &form.from).Placeholder("From, e.g. main or abc1234").Label("From").AutoFocus().OnSubmit(apply), "compare")
				ui.TextInput(ctx.Key("to"), &form.to).Placeholder("To, e.g. HEAD").Label("To").OnSubmit(apply)
			}
			ui.Row(ctx).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(ctx, "Cancel").OnClick(func() { form.open = false })
				ok := branchesOK
				if form.mode == compareRange {
					ok = rangeOK
				}
				ui.PrimaryButton(ctx, "Show changes").Disabled(!ok).OnClick(apply)
			})
		})
	})
	if !form.open {
		app.compareForm = nil
	}
}
