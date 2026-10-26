package main

import (
	"context"
	"log"
	"path/filepath"

	"github.com/egoist/mygo/ui"

	"github.com/psdcoder/agentdiff/internal/gitx"
)

type compareForm struct {
	open      bool
	branch    string
	base      string
	worktrees []gitx.Worktree
}

func (a *app) openCompare() {
	f := &compareForm{open: true, branch: a.headBranch(), base: a.base}
	a.compareForm = f
	r := a.repo
	a.async(func() {
		trees, err := r.Worktrees(context.Background())
		if err != nil {
			log.Printf("worktrees: %v", err)
		}
		a.update(func() { f.worktrees = trees })
	})
}

// compare reviews branch against base. The checked-out branch is kept
// as an empty head, so its scope stays the one the sidebar always shows.
func (a *app) compare(branch, base string) {
	a.head, a.base = branch, base
	if branch == a.branch {
		a.head = ""
	}
	a.loadCommits()
	if s, ok := a.branchScope(); ok {
		a.setScope(s)
	}
}

func (a *app) compareModal(c *ui.Context) {
	f := a.compareForm
	if f == nil {
		return
	}
	t := c.Theme()
	apply := func() {
		if f.branch == "" || f.base == "" || f.branch == f.base {
			return
		}
		f.open = false
		a.compare(f.branch, f.base)
	}
	ui.Modal(c, &f.open, func() {
		ui.Column(c).Width(460).Gap(12).Children(func() {
			ui.Text(c, "Compare branches").FontSize(18).Bold()
			ui.Text(c, "Review the commits of a branch since it left its base. The branch does not have to be checked out.").FontSize(12).TextColor(t.TextMuted)
			ui.Text(c, "Branch").FontSize(12).Bold()
			ui.Combobox(c, &f.branch, a.branches).Label("Branch")
			ui.Text(c, "Base").FontSize(12).Bold()
			ui.Combobox(c, &f.base, a.branches).Label("Base")
			if f.branch != "" && f.branch == f.base {
				ui.Text(c, "Pick two different branches.").FontSize(12).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, "Cancel").OnClick(func() { f.open = false })
				ui.PrimaryButton(c, "Compare").Disabled(f.branch == "" || f.base == "" || f.branch == f.base).OnClick(apply)
			})
			if len(f.worktrees) > 1 {
				ui.Divider(c)
				ui.Text(c, "Worktrees").FontSize(12).Bold()
				ui.Text(c, "Open another working tree of this repository, with its own uncommitted changes.").FontSize(12).TextColor(t.TextMuted)
				for _, w := range f.worktrees {
					label := filepath.Base(w.Path)
					if w.Branch != "" {
						label += "  (" + w.Branch + ")"
					}
					ui.Button(c.Key(w.Path), label).Tooltip(w.Path).Disabled(w.Path == a.repo.Root).OnClick(func() {
						f.open = false
						a.openRepo(w.Path)
					})
				}
			}
		})
	})
	if !f.open {
		a.compareForm = nil
	}
}
