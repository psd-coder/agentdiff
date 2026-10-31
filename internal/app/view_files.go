package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
)

const (
	threadCardHeight = 58
	cardBorder       = 1
	cardRadius       = 8
	cardGap          = 24
	// pageInset is how far the content of a page is from its sides.
	pageInset = 12
	// bigFileLines is how many changed lines fold a card from the start:
	// an open card lays out all its lines in each frame.
	bigFileLines = 1000
)

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

func (a *app) groupPage(c *ui.Context, gi string) {
	t := c.Theme()
	i, ok := a.groupIndex(gi)
	if !ok {
		a.overviewPage(c)
		return
	}
	g := a.groups()[i]
	rows := a.cardRows([]pageRow{{kind: titleRow}}, g.Files)
	a.cardList(c, "group:"+gi, rows, func(pageRow) {
		pageTitle(c).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Gap(6).Grow(1).Shrink(1).Children(func() {
				ui.Text(c, fmt.Sprintf("%d. %s", i+1, g.Title)).FontSize(18).Bold()
				if g.Summary != "" {
					ui.Text(c, g.Summary).TextColor(t.TextMuted)
				}
			})
			a.cmdButton(c, "mark-group-viewed", gi, "Mark all as viewed")
			a.cmdButton(c, "mark-group-not-viewed", gi, "Mark all as not viewed")
			a.layoutSwitch(c)
		})
	})
}

// allPage shows the diff of every file of the scope, in the order the
// sidebar lists them.
func (a *app) allPage(c *ui.Context) {
	t := c.Theme()
	add, del := 0, 0
	for _, f := range a.files {
		add += f.Additions
		del += f.Deletions
	}
	rows := []pageRow{{kind: titleRow}}
	groups := a.groups()
	if a.cfg.FlatFiles {
		rows = a.cardRows(rows, a.fileNames())
	} else {
		for i, g := range groups {
			rows = a.cardRows(append(rows, pageRow{kind: groupRow, group: i}), g.Files)
		}
	}
	a.cardList(c, "all", rows, func(r pageRow) {
		if r.kind == groupRow {
			g := groups[r.group]
			ui.Text(c, fmt.Sprintf("%d. %s", r.group+1, g.Title)).FontSize(16).Bold().Padding(4, 20, cardGap/2, 20)
			return
		}
		pageTitle(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "All files diff").FontSize(18).Bold()
			ui.Text(c, fmt.Sprintf("%d files", len(a.files))).TextColor(t.TextMuted)
			ui.Text(c, fmt.Sprintf("+%d", add)).TextColor(t.Success)
			ui.Text(c, fmt.Sprintf("−%d", del)).TextColor(t.Danger)
			ui.Spacer(c)
			a.cmdButton(c, "mark-all-viewed", "", "Mark all as viewed")
			a.cmdButton(c, "mark-all-not-viewed", "", "Mark all as not viewed")
			a.layoutSwitch(c)
		})
	})
}

func pageTitle(c *ui.Context) ui.Element {
	return ui.Row(c).Gap(12).Padding(16, 20).Margin(0, 0, cardGap/2, 0).BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border)
}

type rowKind uint8

const (
	titleRow rowKind = iota
	groupRow
	headRow
	bodyRow
	gapRow
)

// pageRow is a row of a page of file cards. A card is two rows, its header
// and its diff, so that the list can pin the header while the diff
// scrolls under it. A gap row after each card is a header as well, so
// that it pushes the pinned header away at the end of its card.
type pageRow struct {
	kind  rowKind
	group int
	file  string
}

// cardRows adds the rows of the cards of the files to rows: a header, the
// diff of a file not folded, and a gap.
func (a *app) cardRows(rows []pageRow, files []string) []pageRow {
	for _, f := range files {
		if a.diffs[f] == nil {
			continue
		}
		rows = append(rows, pageRow{kind: headRow, file: f})
		if !a.isFolded(f) {
			rows = append(rows, pageRow{kind: bodyRow, file: f})
		}
		rows = append(rows, pageRow{kind: gapRow, file: f})
	}
	return rows
}

// cardPage is the place of a page of file cards, and the file whose open
// card has its header pinned at the top.
type cardPage struct {
	list   ui.ListState
	pinned string
}

// cardList shows the cards of a page, building only the rows in view, as
// a page with thousands of changed lines lays out slowly. other builds the
// rows that are not cards.
func (a *app) cardList(c *ui.Context, page string, rows []pageRow, other func(pageRow)) {
	t := c.Theme()
	p := a.cardsOf(page)
	s := &p.list
	// A card folded under its pinned header would leave the page where
	// its diff ended: the folded card goes to the top instead.
	if f := p.pinned; f != "" && a.isFolded(f) {
		if i := slices.Index(rows, pageRow{kind: headRow, file: f}); i >= 0 {
			s.ScrollTo(i, ui.Start)
		}
	}
	p.pinned = ""
	s.Key = func(i int) any { return rows[i] }
	s.Header = func(i int) bool { return rows[i].kind == headRow || rows[i].kind == gapRow }
	list := ui.List(c.Key("page-"+page), s, len(rows)).Grow(1).FillWidth().Focusable().FocusRing(false)
	top := list.LayoutBounds().Y
	list.Rows(func(lr ui.ListRow) {
		r := rows[lr.Index]
		d := a.diffs[r.file]
		switch r.kind {
		case headRow:
			folded := a.isFolded(r.file)
			row := ui.Column(c.Key("head:"+r.file)).Padding(0, pageInset)
			// The list pins a header at its top, where it is square, and
			// pushes it up at the end of its card, where it rounds its
			// bottom as the card does.
			at := row.LayoutBounds()
			pinned := at.H > 0 && at.Y <= top
			pushed := pinned && at.Y < top
			if pinned && !folded {
				p.pinned = r.file
			}
			row.Children(func() {
				head := a.fileHeader(c, d, false).Border(cardBorder, t.Border)
				switch {
				case pinned:
					// The line under the page's top bar is its top border.
					head.BorderWidth(0, cardBorder, cardBorder, cardBorder).Padding(8+cardBorder, 12, 8, 12)
					if pushed {
						head.Radius(0, 0, cardRadius, cardRadius)
					}
				case folded:
					head.Radius(cardRadius)
				default:
					head.Radius(cardRadius, cardRadius, 0, 0)
				}
			})
		case bodyRow:
			ui.Column(c.Key("body:"+r.file)).Padding(0, pageInset).Children(func() {
				ui.Column(c).BorderWidth(0, cardBorder, cardBorder, cardBorder).BorderColor(t.Border).Radius(0, 0, cardRadius, cardRadius).Children(func() {
					a.fileThreads(c, r.file)
					a.diffBody(c, d, true)
					a.cardCorners(c)
				})
			})
		case gapRow:
			ui.Box(c.Key("gap:" + r.file)).Height(cardGap)
		default:
			ui.Column(c).Children(func() { other(r) })
		}
	})
	a.takeFocus(list, "page")
}

// cardCorners paints the rounded bottom corners of a card over its diff.
// The GPU renderer rounds the innermost clip alone, and the diff clips its
// own content, so a card that clipped its content would clip it to a
// square. A ring in the color of the page covers the content outside the
// corners, and the border goes over the content.
func (a *app) cardCorners(c *ui.Context) {
	t := c.Theme()
	const ring float32 = cardBorder + cardRadius
	ui.Box(c).Absolute().Left(-ring).Top(0).Right(-ring).Bottom(-ring).
		Radius(0, 0, 2*cardRadius, 2*cardRadius).BorderWidth(0, cardRadius, cardRadius, cardRadius).BorderColor(t.Background).PassThrough()
	ui.Box(c).Absolute().Left(-cardBorder).Top(0).Right(-cardBorder).Bottom(-cardBorder).
		Radius(0, 0, cardRadius, cardRadius).BorderWidth(0, cardBorder, cardBorder, cardBorder).BorderColor(t.Border).PassThrough()
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

func (a *app) fileHeader(c *ui.Context, d *fileDiff, page bool) ui.Element {
	t := c.Theme()
	f := d.change
	head := ui.Row(c).Gap(10).Padding(8, 12).AlignItems(ui.Center).Background(t.Surface)
	if page {
		head.BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
	}
	head.Children(func() {
		if !page {
			a.foldButton(c, f.Path)
		}
		if page {
			ui.Text(c, f.Path).Font("monospace").Bold().SingleLine().Shrink(1).Selectable()
		} else if textLink(c, f.Path, func(e ui.Element) { e.Font("monospace").Bold().TextColor(t.Text) }).Tooltip("Open the file on its own page").Clicked() {
			a.goTo("file:" + f.Path)
		}
		ui.Badge(c, statusLabel(f))
		ui.Text(c, fmt.Sprintf("+%d", f.Additions)).FontSize(12).TextColor(t.Success)
		ui.Text(c, fmt.Sprintf("−%d", f.Deletions)).FontSize(12).TextColor(t.Danger)
		if d.state != nil {
			if _, ok := d.state.SelectedLines(); !ok {
				ui.Text(c, "Click a line number, then Shift+click another to select a range").FontSize(11).TextColor(t.TextMuted).SingleLine().Shrink(1)
			}
		}
		ui.Spacer(c)
		if d.state != nil {
			if r, ok := d.state.SelectedLines(); ok {
				label := fmt.Sprintf("Comment on line %d", r.Start+1)
				if r.End > r.Start {
					label = fmt.Sprintf("Comment on lines %d–%d", r.Start+1, r.End+1)
				}
				a.cmdButton(c, "comment-lines", f.Path, label)
			}
		}
		a.cmdButton(c, "comment-file", f.Path, "Comment on file")
		if page {
			a.layoutSwitch(c)
		}
		a.cmdCheckbox(c, "mark-viewed", "mark-not-viewed", f.Path, "Viewed")
	})
	return head
}

// foldButton folds the card of the file to its header, or unfolds it.
func (a *app) foldButton(c *ui.Context, file string) {
	t := c.Theme()
	folded := a.isFolded(file)
	tip, label := "Fold the diff", "Fold"
	if folded {
		tip, label = "Unfold the diff", "Unfold"
	}
	btn := ui.ButtonBase(c.Key("fold")).Padding(4).Radius(4).Expanded(!folded).Label(label).Tooltip(tip)
	if btn.Hovered() {
		btn.Background(t.SurfaceHover)
	}
	btn.Children(func() {
		arrow := ui.Icon(c, chevronIcon).Size(12, 12).TextColor(t.TextMuted)
		if folded {
			arrow.Rotate(-90)
		}
	})
	if btn.Clicked() {
		a.folded[file] = !folded
	}
}

// layoutSwitch picks split or unified diffs, for every diff in the app.
func (a *app) layoutSwitch(c *ui.Context) {
	a.cmdSegmented(c, []string{"split-diffs", "unified-diffs"}, "Split", "Unified").Label("Diff layout")
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
	if a.cfg.Unified {
		s.Layout = editor.DiffUnified
	}
	file := d.change.Path
	s.OnGutterButton = func(r editor.DiffLineRange) {
		if sel, ok := s.SelectedLines(); ok && sel.Side == r.Side && sel.Start <= r.Start && r.Start <= sel.End {
			r = sel
		}
		a.lineThread(file, r)
		s.SetSelectedLines(editor.DiffLineRange{}, false)
	}
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
	s.Theme = a.codeColors
	view := editor.DiffView(c, s).FillWidth()
	if !fit {
		a.takeFocus(view, "page")
	}
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
			ui.Text(c, a.threadMeta(th)).FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
	})
	if card.Clicked() {
		a.openThread(th.ID)
	}
}

func (a *app) threadMeta(th *review.Thread) string {
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
	case a.threadBusy(th.ID):
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
