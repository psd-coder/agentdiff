package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/psd-coder/agentdiff/internal/review"
)

// archivedPage shows an archived review as it was written, read-only:
// its walkthroughs with the code they saved, its questions and comments.
func (a *app) archivedPage(c *ui.Context, id string) {
	e := a.earlierReview(id)
	if e == nil {
		a.emptyState(c, "Earlier review not found", "It was restored, moved or deleted.")
		return
	}
	t := c.Theme()
	rev, ov := e.rev, e.rev.Overview
	a.pageScroll(c, "archived:"+id).Grow(1).FillWidth().Children(func() {
		ui.Column(c).Padding(24, 32).Gap(16).MaxWidth(980).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Earlier review").FontSize(22).Bold()
				ui.Spacer(c)
				a.cmdPrimaryButton(c, "open-archived-commit", id, "Open commit")
				a.cmdPrimaryButton(c, "move-archived", id, "Move review to the commit")
				a.cmdButton(c, "restore-archived", id, "Restore")
				a.cmdButton(c, "delete-archived", id, "Delete")
			})
			ui.Text(c, writtenBy(ov.Agent, ov.Model, ov.Generated.Format("Jan 2 15:04"))).FontSize(12).TextColor(t.TextMuted)
			ui.Text(c, a.earlierStatus(e)).FontSize(12).TextColor(t.TextMuted)
			seen := map[string]bool{}
			switch {
			case ov.Walk != nil:
				a.savedWalkthrough(c, rev, "", true, ov.Walk, seen)
			case ov.Markdown != "":
				markdown.View(c, ov.Markdown, &a.md)
			}
			a.savedGroups(c, ov.Groups)
			a.savedThreads(c, rev.Threads)
		})
	})
}

// earlierStatus says where the changes of an archived review are now.
func (a *app) earlierStatus(e *earlier) string {
	text := "It was written for uncommitted changes that are no longer in the working tree. It comes back by itself when those changes do."
	switch {
	case e.movedTo != "":
		text = "It was written for changes that are now commit " + a.commitName(e.movedTo) + ". The review moves there when you open the commit."
	case e.probably != "":
		text = "Its files were probably committed in " + a.commitName(e.probably) + ", which changes all of them. The commit has other changes too, so the review does not move there by itself."
	}
	if !a.rev.Empty() {
		text += " Restore works while the uncommitted changes have no review of their own."
	}
	return text
}

// savedWalkthrough shows a walkthrough of an archived review. With
// nested, the walks under its steps show inside them. parent is the walk
// it belongs to, empty for the overview. seen keeps a walk from showing
// twice.
func (a *app) savedWalkthrough(c *ui.Context, rev *review.Review, parent string, nested bool, w *review.Walkthrough, seen map[string]bool) {
	t := c.Theme()
	if w.Summary != "" {
		ui.Text(c, w.Summary).FontSize(15).Selectable()
	}
	for i, s := range w.Steps {
		ui.Column(c.Key(fmt.Sprintf("step-%s-%d", parent, i))).Gap(10).Padding(16, 20).Radius(10).Background(t.Surface).Border(1, t.Border).Children(func() {
			ui.Text(c, fmt.Sprintf("%d. %s", i+1, s.Title)).FontSize(16).Bold()
			if s.Text != "" {
				markdown.View(c, s.Text, &a.md)
			}
			for ri, r := range s.Refs {
				a.snippetView(c.Key(fmt.Sprintf("ref-%d", ri)), r, savedSnippet(r))
			}
			if !nested {
				return
			}
			for _, sub := range rev.WalksOn(parent, i) {
				a.savedWalk(c, rev, sub, seen)
			}
		})
	}
	if len(w.Checklist) > 0 {
		ui.Text(c, "Checklist").FontSize(18).Bold().Margin(8, 0, 0, 0)
	}
	for i, it := range w.Checklist {
		ui.Column(c.Key(fmt.Sprintf("check-%s-%d", parent, i))).Gap(6).Padding(12, 16).Radius(10).Border(1, t.Border).Children(func() {
			ui.Text(c, it.Question).Bold()
			markdown.View(c, it.Answer, &a.md)
			for ri, r := range it.Refs {
				a.snippetView(c.Key(fmt.Sprintf("ref-%d", ri)), r, savedSnippet(r))
			}
		})
	}
	if !nested {
		return
	}
	for _, q := range rev.WalksOn(parent, -1) {
		a.savedWalk(c, rev, q, seen)
	}
}

// savedWalk shows a deeper walkthrough, or a question with its answers,
// inside the step or the page it belongs to.
func (a *app) savedWalk(c *ui.Context, rev *review.Review, w *review.Walk, seen map[string]bool) {
	if seen[w.ID] {
		return
	}
	seen[w.ID] = true
	t := c.Theme()
	ui.Column(c.Key("walk-"+w.ID)).Gap(10).Padding(0, 0, 0, 16).BorderWidth(0, 0, 0, 2).BorderColor(t.Border).Children(func() {
		if w.Question == "" {
			ui.Text(c, "Deeper: "+rev.WalkTitle(w)).Bold().TextColor(t.TextMuted)
		}
		for i, turn := range w.Turns() {
			ui.Column(c.Key(fmt.Sprintf("turn-%d", i))).Gap(10).Children(func() {
				if turn.Question != "" {
					ui.Text(c, "Question: "+turn.Question).Bold().Selectable()
				}
				switch {
				case turn.Content != nil:
					// Walks go deeper from the first answer only.
					a.savedWalkthrough(c, rev, w.ID, i == 0, turn.Content, seen)
				case turn.Markdown != "":
					markdown.View(c, turn.Markdown, &a.md)
				case turn.Err != "":
					ui.Text(c, turn.Err).FontSize(12).TextColor(t.Danger)
				}
			})
		}
	})
}

func (a *app) savedGroups(c *ui.Context, groups []review.Group) {
	t := c.Theme()
	if len(groups) == 0 {
		return
	}
	ui.Text(c, "Review order").FontSize(18).Bold().Margin(8, 0, 0, 0)
	for gi, g := range groups {
		ui.Column(c.Key(fmt.Sprintf("group-%d", gi))).Gap(6).Padding(14, 18).Radius(10).Border(1, t.Border).Children(func() {
			ui.Text(c, fmt.Sprintf("%d. %s", gi+1, g.Title)).Bold()
			if g.Summary != "" {
				ui.Text(c, g.Summary).TextColor(t.TextMuted)
			}
			for _, f := range g.Files {
				ui.Text(c.Key(f), f).Font("monospace").FontSize(12).Selectable()
			}
		})
	}
}

func (a *app) savedThreads(c *ui.Context, threads []*review.Thread) {
	t := c.Theme()
	var shown []*review.Thread
	for _, th := range threads {
		if len(th.Messages) > 0 {
			shown = append(shown, th)
		}
	}
	if len(shown) == 0 {
		return
	}
	ui.Text(c, "Comments").FontSize(18).Bold().Margin(8, 0, 0, 0)
	for _, th := range shown {
		ui.Column(c.Key("thread-"+th.ID)).Gap(8).Padding(12, 16).Radius(10).Border(1, t.Border).Children(func() {
			ui.Text(c, th.Anchor.Label()).Font("monospace").FontSize(12).TextColor(t.Accent).SingleLine()
			if th.Anchor.Quote != "" && th.Anchor.Kind != review.AnchorSection {
				ui.Text(c, strings.ReplaceAll(strings.TrimRight(th.Anchor.Quote, "\n"), "\t", "    ")).Font("monospace").FontSize(12).
					Padding(6, 10).Radius(6).Background(t.Surface).Selectable()
			}
			for i, m := range th.Messages {
				ui.Column(c.Key(i)).Gap(2).Children(func() {
					who := "You"
					if m.Role == review.RoleAgent {
						who = "Agent"
					}
					ui.Text(c, who).FontSize(11).Bold().TextColor(t.TextMuted)
					if m.Role == review.RoleAgent {
						markdown.View(c, m.Text, &a.md)
					} else {
						ui.Text(c, m.Text).Selectable()
					}
				})
			}
		})
	}
}
