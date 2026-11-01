package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/psd-coder/agentdiff/internal/continuity"
	"github.com/psd-coder/agentdiff/internal/review"
)

// archivedPage shows an archived review as it was written, read-only:
// its walkthroughs with the code they saved, its questions and comments.
func (app *app) archivedPage(ctx *ui.Context, id string) {
	earlier := app.reviews.Find(id)
	if earlier == nil {
		app.emptyState(ctx, "Earlier review not found", "It was restored, moved or deleted.")
		return
	}
	theme := ctx.Theme()
	rev, ov := earlier.Rev, earlier.Rev.Overview
	app.pageScroll(ctx, page{kindArchived, id}).Grow(1).FillWidth().Children(func() {
		ui.Column(ctx).Padding(24, 32).Gap(16).MaxWidth(980).Children(func() {
			ui.Row(ctx).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(ctx, "Earlier review").FontSize(22).Bold()
				ui.Spacer(ctx)
				app.cmdPrimaryButton(ctx, "open-archived-commit", id, "Open commit")
				app.cmdPrimaryButton(ctx, "move-archived", id, "Move review to the commit")
				app.cmdButton(ctx, "restore-archived", id, "Restore")
				app.cmdButton(ctx, "delete-archived", id, "Delete")
			})
			ui.Text(ctx, writtenBy(ov.Agent, ov.Model, ov.Generated.Format("Jan 2 15:04"))).FontSize(12).TextColor(theme.TextMuted)
			ui.Text(ctx, app.earlierStatus(earlier)).FontSize(12).TextColor(theme.TextMuted)
			seen := map[string]bool{}
			switch {
			case ov.Walk != nil:
				app.savedWalkthrough(ctx, rev, "", true, ov.Walk, seen)
			case ov.Markdown != "":
				markdown.View(ctx, ov.Markdown, &app.md)
			}
			app.savedGroups(ctx, ov.Groups)
			app.savedThreads(ctx, rev.Threads)
		})
	})
}

// earlierStatus says where the changes of an archived review are now.
func (app *app) earlierStatus(earlier *continuity.Earlier) string {
	text := "It was written for uncommitted changes that are no longer in the working tree. It comes back by itself when those changes do."
	switch {
	case earlier.MovedTo != "":
		text = "It was written for changes that are now commit " + app.commitName(earlier.MovedTo) + ". The review moves there when you open the commit."
	case earlier.Probably != "":
		text = "Its files were probably committed in " + app.commitName(earlier.Probably) + ", which changes all of them. The commit has other changes too, so the review does not move there by itself."
	}
	if !app.rev.Empty() {
		text += " Restore works while the uncommitted changes have no review of their own."
	}
	return text
}

// savedWalkthrough shows a walkthrough of an archived review. With
// nested, the walks under its steps show inside them. parent is the walk
// it belongs to, empty for the overview. seen keeps a walk from showing
// twice.
func (app *app) savedWalkthrough(ctx *ui.Context, rev *review.Review, parent string, nested bool, walkthrough *review.Walkthrough, seen map[string]bool) {
	theme := ctx.Theme()
	if walkthrough.Summary != "" {
		ui.Text(ctx, walkthrough.Summary).FontSize(15).Selectable()
	}
	for i, s := range walkthrough.Steps {
		ui.Column(ctx.Key(fmt.Sprintf("step-%s-%d", parent, i))).Gap(10).Padding(16, 20).Radius(10).Background(theme.Surface).Border(1, theme.Border).Children(func() {
			ui.Text(ctx, fmt.Sprintf("%d. %s", i+1, s.Title)).FontSize(16).Bold()
			if s.Text != "" {
				markdown.View(ctx, s.Text, &app.md)
			}
			for ri, r := range s.Refs {
				app.snippetView(ctx.Key(fmt.Sprintf("ref-%d", ri)), r, savedSnippet(r))
			}
			if !nested {
				return
			}
			for _, sub := range rev.WalksOn(parent, i) {
				app.savedWalk(ctx, rev, sub, seen)
			}
		})
	}
	for si, sec := range walkthrough.Sections {
		if sec.Title != "" {
			ui.Text(ctx.Key(fmt.Sprintf("part-%s-%d", parent, si)), sec.Title).FontSize(18).Bold().Margin(8, 0, 0, 0)
		}
		for i, it := range sec.Items {
			ui.Column(ctx.Key(fmt.Sprintf("part-%s-%d-%d", parent, si, i))).Gap(6).Padding(12, 16).Radius(10).Border(1, theme.Border).Children(func() {
				if it.Title != "" {
					ui.Text(ctx, it.Title).Bold()
				}
				if it.Text != "" {
					markdown.View(ctx, it.Text, &app.md)
				}
				for ri, r := range it.Refs {
					app.snippetView(ctx.Key(fmt.Sprintf("ref-%d", ri)), r, savedSnippet(r))
				}
			})
		}
	}
	if !nested {
		return
	}
	for _, q := range rev.WalksOn(parent, -1) {
		app.savedWalk(ctx, rev, q, seen)
	}
}

// savedWalk shows a deeper walkthrough, or a question with its answers,
// inside the step or the page it belongs to.
func (app *app) savedWalk(ctx *ui.Context, rev *review.Review, walk *review.Walk, seen map[string]bool) {
	if seen[walk.ID] {
		return
	}
	seen[walk.ID] = true
	theme := ctx.Theme()
	ui.Column(ctx.Key("walk-"+walk.ID)).Gap(10).Padding(0, 0, 0, 16).BorderWidth(0, 0, 0, 2).BorderColor(theme.Border).Children(func() {
		if walk.Question == "" {
			ui.Text(ctx, "Deeper: "+rev.WalkTitle(walk)).Bold().TextColor(theme.TextMuted)
		}
		for i, turn := range walk.Turns() {
			ui.Column(ctx.Key(fmt.Sprintf("turn-%d", i))).Gap(10).Children(func() {
				if turn.Question != "" {
					ui.Text(ctx, "Question: "+turn.Question).Bold().Selectable()
				}
				switch {
				case turn.Content != nil:
					// Walks go deeper from the first answer only.
					app.savedWalkthrough(ctx, rev, walk.ID, i == 0, turn.Content, seen)
				case turn.Markdown != "":
					markdown.View(ctx, turn.Markdown, &app.md)
				case turn.Err != "":
					errorText(ctx, turn.Err)
				}
			})
		}
	})
}

func (app *app) savedGroups(ctx *ui.Context, groups []review.Group) {
	theme := ctx.Theme()
	if len(groups) == 0 {
		return
	}
	ui.Text(ctx, "Review order").FontSize(18).Bold().Margin(8, 0, 0, 0)
	for gi, g := range groups {
		ui.Column(ctx.Key(fmt.Sprintf("group-%d", gi))).Gap(6).Padding(14, 18).Radius(10).Border(1, theme.Border).Children(func() {
			ui.Text(ctx, fmt.Sprintf("%d. %s", gi+1, g.Title)).Bold()
			if g.Summary != "" {
				ui.Text(ctx, g.Summary).TextColor(theme.TextMuted)
			}
			for _, f := range g.Files {
				ui.Text(ctx.Key(f), f).Font("monospace").FontSize(12).Selectable()
			}
		})
	}
}

func (app *app) savedThreads(ctx *ui.Context, threads []*review.Thread) {
	theme := ctx.Theme()
	var shown []*review.Thread
	for _, th := range threads {
		if len(th.Messages) > 0 {
			shown = append(shown, th)
		}
	}
	if len(shown) == 0 {
		return
	}
	ui.Text(ctx, "Comments").FontSize(18).Bold().Margin(8, 0, 0, 0)
	for _, th := range shown {
		ui.Column(ctx.Key("thread-"+th.ID)).Gap(8).Padding(12, 16).Radius(10).Border(1, theme.Border).Children(func() {
			ui.Text(ctx, th.Anchor.Label()).Font("monospace").FontSize(12).TextColor(theme.Accent).SingleLine()
			if th.Anchor.Quote != "" && th.Anchor.Kind != review.AnchorSection {
				ui.Text(ctx, strings.ReplaceAll(strings.TrimRight(th.Anchor.Quote, "\n"), "\t", "    ")).Font("monospace").FontSize(12).
					Padding(6, 10).Radius(6).Background(theme.Surface).Selectable()
			}
			for i, m := range th.Messages {
				ui.Column(ctx.Key(i)).Gap(2).Children(func() {
					who := "You"
					if m.Role == review.RoleAgent {
						who = "Agent"
					}
					ui.Text(ctx, who).FontSize(11).Bold().TextColor(theme.TextMuted)
					if m.Role == review.RoleAgent {
						markdown.View(ctx, m.Text, &app.md)
					} else {
						ui.Text(ctx, m.Text).Selectable()
					}
				})
			}
		})
	}
}
