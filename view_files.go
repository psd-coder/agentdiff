package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/psdcoder/agentdiff/internal/gitx"
	"github.com/psdcoder/agentdiff/internal/review"
)

const threadCardHeight = 58

func (a *app) filePage(c *ui.Context, file string) {
	d := a.diffs[file]
	if d == nil {
		a.emptyState(c, "File not in this scope", file)
		return
	}
	ui.Column(c).Fill().Children(func() {
		a.fileHeader(c, d, true)
		a.fileThreads(c, file)
		a.diffBody(c, d, false)
	})
	if r := a.reveal; r != nil && r.file == file && d.state != nil {
		a.reveal = nil
		d.state.GoToHunk(r.hunk)
	}
}

func (a *app) groupPage(c *ui.Context, i int) {
	t := c.Theme()
	groups := a.groups()
	if i < 0 || i >= len(groups) {
		a.overviewPage(c)
		return
	}
	g := groups[i]
	ui.Scroll(c.Key(fmt.Sprintf("group-%d", i))).TrackScroll(a.scrollOf(fmt.Sprintf("group:%d", i))).Grow(1).FillWidth().Children(func() {
		ui.Column(c).Gap(6).Padding(16, 20).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Text(c, fmt.Sprintf("%d. %s", i+1, g.Title)).FontSize(18).Bold()
			if g.Summary != "" {
				ui.Text(c, g.Summary).TextColor(t.TextMuted)
			}
		})
		for _, f := range g.Files {
			d := a.diffs[f]
			if d == nil {
				continue
			}
			ui.Column(c.Key(f)).Margin(12, 16).Radius(8).Border(1, t.Border).Clip().Children(func() {
				a.fileHeader(c, d, false)
				a.fileThreads(c, f)
				a.diffBody(c, d, true)
			})
		}
	})
}

func statusLabel(f gitx.FileChange) string {
	switch {
	case f.Untracked:
		return "new, untracked"
	case f.Status == gitx.StatusRenamed:
		return "renamed from " + f.OldPath
	case f.Status == gitx.StatusCopied:
		return "copied from " + f.OldPath
	}
	return string(f.Status)
}

func (a *app) fileHeader(c *ui.Context, d *fileDiff, page bool) {
	t := c.Theme()
	f := d.change
	ui.Row(c).Gap(10).Padding(8, 12).AlignItems(ui.Center).Background(t.Surface).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		if page {
			ui.Text(c, f.Path).Font("monospace").Bold().SingleLine().Shrink(1).Selectable()
		} else if textLink(c, f.Path, func(e ui.Element) { e.Font("monospace").Bold().TextColor(t.Text) }).Tooltip("Open the file on its own page").Clicked() {
			a.goTo("file:" + f.Path)
		}
		ui.Badge(c, statusLabel(f))
		ui.Text(c, fmt.Sprintf("+%d", f.Additions)).FontSize(12).TextColor(t.Success)
		ui.Text(c, fmt.Sprintf("−%d", f.Deletions)).FontSize(12).TextColor(t.Danger)
		ui.Spacer(c)
		if d.state != nil {
			if r, ok := d.state.SelectedLines(); ok {
				label := fmt.Sprintf("Comment on line %d", r.Start+1)
				if r.End > r.Start {
					label = fmt.Sprintf("Comment on lines %d–%d", r.Start+1, r.End+1)
				}
				ui.Button(c.Key("sel"), label).OnClick(func() {
					a.lineThread(f.Path, r)
					d.state.SetSelectedLines(editor.DiffLineRange{}, false)
				})
			}
		}
		ui.Button(c.Key("file-comment"), "Comment on file").OnClick(func() {
			a.newThread(review.Anchor{Kind: review.AnchorFile, File: f.Path})
		})
		if page {
			layout := 0
			if a.cfg.Unified {
				layout = 1
			}
			if ui.Segmented(c, &layout, "Split", "Unified").Label("Layout").Changed() {
				a.cfg.Unified = layout == 1
				a.saveConfig()
			}
		}
		viewed := a.rev.Viewed[f.Path]
		if ui.Checkbox(c.Key("viewed"), &viewed, "Viewed").Changed() {
			a.rev.Viewed[f.Path] = viewed
			a.saveReview()
		}
	})
}

func (a *app) fileThreads(c *ui.Context, file string) {
	var threads []*review.Thread
	for _, th := range a.rev.ThreadsOn(file) {
		if th.Anchor.Kind == review.AnchorFile && (len(th.Messages) > 0 || th.ID == a.thread) {
			threads = append(threads, th)
		}
	}
	if len(threads) == 0 {
		return
	}
	ui.Column(c).Gap(6).Padding(8, 12).Children(func() {
		for _, th := range threads {
			a.threadCard(c, th)
		}
	})
}

func (a *app) lineThread(file string, r editor.DiffLineRange) {
	side := sideOf(r.Side)
	start, end := r.Start+1, r.End+1
	a.newThread(review.Anchor{
		Kind: review.AnchorLines, File: file, Side: side, Start: start, End: end,
		Quote: a.quoteLines(file, side, start, end),
	})
}

func (a *app) diffBody(c *ui.Context, d *fileDiff, fit bool) {
	t := c.Theme()
	switch {
	case d.loading:
		ui.Row(c).Gap(8).Padding(16).Children(func() {
			ui.Spinner(c).Size(14, 14)
			ui.Text(c, "Loading…").TextColor(t.TextMuted)
		})
		return
	case d.err != "":
		ui.Text(c, d.err).Padding(16).TextColor(t.TextMuted)
		return
	case d.state == nil:
		return
	}
	s := d.state
	s.FitContent = fit
	s.Layout = editor.DiffSplit
	if a.cfg.Unified || fit {
		s.Layout = editor.DiffUnified
	}
	file := d.change.Path
	s.OnGutterButton = func(r editor.DiffLineRange) { a.lineThread(file, r) }
	s.Annotations = s.Annotations[:0]
	for _, th := range a.rev.ThreadsOn(file) {
		if th.Anchor.Kind != review.AnchorLines || (len(th.Messages) == 0 && th.ID != a.thread) {
			continue
		}
		s.Annotations = append(s.Annotations, editor.DiffAnnotation{
			Side: diffSide(th.Anchor.Side), Line: th.Anchor.End - 1, Height: threadCardHeight + 8, Key: th.ID,
		})
	}
	s.RenderAnnotation = func(c *ui.Context, an editor.DiffAnnotation) {
		th := a.rev.Thread(an.Key.(string))
		if th == nil {
			return
		}
		ui.Column(c).Padding(4, 12).Children(func() { a.threadCard(c, th) })
	}
	view := editor.DiffView(c, s).FillWidth()
	if !fit {
		view.Grow(1)
	}
}

// threadCard is a thread in short, where it is anchored: a click opens it
// in the panel.
func (a *app) threadCard(c *ui.Context, th *review.Thread) {
	t := c.Theme()
	card := ui.ButtonBase(c.Key("card-"+th.ID)).Height(threadCardHeight).Padding(6, 10).Gap(10).Radius(8).AlignItems(ui.Center).
		Background(t.Background).Border(1, t.Border)
	if th.ID == a.thread {
		card.Border(2, t.Accent)
	} else if card.Hovered() {
		card.Background(t.SurfaceHover)
	}
	card.Children(func() {
		ui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
			first := "New comment"
			if len(th.Messages) > 0 {
				first = th.Messages[0].Text
			}
			ui.Text(c, excerpt(first, 160)).MaxLines(1)
			ui.Text(c, threadMeta(th)).FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
	})
	if card.Clicked() {
		a.openThread(th.ID)
	}
}

func threadMeta(th *review.Thread) string {
	var parts []string
	if th.Anchor.Kind == review.AnchorLines {
		parts = append(parts, th.Anchor.Label())
	}
	replies := 0
	for _, m := range th.Messages {
		if m.Role == review.RoleAgent {
			replies++
		}
	}
	switch {
	case th.Busy():
		parts = append(parts, "agent is answering…")
	case replies > 0:
		parts = append(parts, fmt.Sprintf("%d agent replies", replies))
	case len(th.Messages) > 0:
		parts = append(parts, "comment")
	}
	if th.Resolved {
		parts = append(parts, "resolved")
	}
	if th.Excluded {
		parts = append(parts, "not in feedback")
	}
	return strings.Join(parts, " · ")
}
