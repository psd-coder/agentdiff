package app

import (
	"cmp"
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

func (app *app) filePage(ctx *ui.Context, file string) {
	diff := app.diffs[file]
	if diff == nil {
		emptyState(ctx, "File not in this scope", file)
		return
	}
	ui.Column(ctx).Fill().Children(func() {
		app.fileHeader(ctx, diff, true)
		app.fileThreads(ctx, file)
		app.diffBody(ctx, diff, false)
	})
	if request := app.reveal; request != nil && request.file == file && diff.state != nil {
		app.reveal = nil
		diff.state.GoToHunk(request.hunk)
	}
}

func (app *app) groupPage(ctx *ui.Context, gi string) {
	theme := ctx.Theme()
	index, ok := app.groupIndex(gi)
	if !ok {
		app.overviewPage(ctx, "")
		return
	}
	group := app.groups()[index]
	rows := app.cardRows([]pageRow{{kind: titleRow}}, group.Files)
	app.cardList(ctx, page{kindGroup, gi}, rows, func(pageRow) {
		diffPageHeader(ctx).AlignItems(ui.Start).Children(func() {
			ui.Column(ctx).Gap(theme.Space(1.5)).Grow(1).Shrink(1).Children(func() {
				heading(ctx, fmt.Sprintf("%d. %s", index+1, group.Title))
				if group.Summary != "" {
					ui.Text(ctx, group.Summary).TextColor(theme.TextMuted)
				}
			})
			app.cmdButton(ctx, "mark-group-viewed", gi, "Mark all as viewed")
			app.cmdButton(ctx, "mark-group-not-viewed", gi, "Mark all as not viewed")
			app.layoutSwitch(ctx)
		})
	})
}

// allPage shows the diff of every file of the scope, in the order the
// sidebar lists them.
func (app *app) allPage(ctx *ui.Context) {
	theme := ctx.Theme()
	add, del, _ := app.changeTotals()
	rows := []pageRow{{kind: titleRow}}
	groups := app.groups()
	if app.cfg.FlatFiles {
		rows = app.cardRows(rows, app.fileNames())
	} else {
		for i, g := range groups {
			rows = app.cardRows(append(rows, pageRow{kind: groupRow, group: i}), g.Files)
		}
	}
	app.cardList(ctx, page{kind: kindAll}, rows, func(r pageRow) {
		if r.kind == groupRow {
			group := groups[r.group]
			cardTitle(ctx, fmt.Sprintf("%d. %s", r.group+1, group.Title)).Padding(theme.Space(1), theme.Space(5), cardGap/2, theme.Space(5))
			return
		}
		diffPageHeader(ctx).AlignItems(ui.Center).Children(func() {
			heading(ctx, "All files diff")
			ui.Text(ctx, fmt.Sprintf("%d files", len(app.files))).TextColor(theme.TextMuted)
			diffStat(ctx, add, del)
			ui.Spacer(ctx)
			app.cmdButton(ctx, "mark-all-viewed", "", "Mark all as viewed")
			app.cmdButton(ctx, "mark-all-not-viewed", "", "Mark all as not viewed")
			app.layoutSwitch(ctx)
		})
	})
}

func diffPageHeader(ctx *ui.Context) ui.Element {
	theme := ctx.Theme()
	return ui.Row(ctx).Gap(theme.Space(3)).Padding(theme.Space(4), theme.Space(5)).Margin(0, 0, cardGap/2, 0).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border)
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
func (app *app) cardRows(rows []pageRow, files []string) []pageRow {
	for _, f := range files {
		if app.diffs[f] == nil {
			continue
		}
		rows = append(rows, pageRow{kind: headRow, file: f})
		if !app.isFolded(f) {
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
func (app *app) cardList(ctx *ui.Context, pg page, rows []pageRow, other func(pageRow)) {
	theme := ctx.Theme()
	cards := app.cardsOf(pg)
	listState := &cards.list
	// A card folded under its pinned header would leave the page where
	// its diff ended: the folded card goes to the top instead.
	if pinnedFile := cards.pinned; pinnedFile != "" && app.isFolded(pinnedFile) {
		if index := slices.Index(rows, pageRow{kind: headRow, file: pinnedFile}); index >= 0 {
			listState.ScrollTo(index, ui.Start)
		}
	}
	cards.pinned = ""
	listState.Key = func(i int) any { return rows[i] }
	listState.Header = func(i int) bool { return rows[i].kind == headRow || rows[i].kind == gapRow }
	list := ui.List(ctx.Key("page-"+pg.String()), listState, len(rows)).Grow(1).FillWidth().Focusable().FocusRing(false)
	top := list.LayoutBounds().Y
	list.Rows(func(lr ui.ListRow) {
		cardRow := rows[lr.Index]
		diff := app.diffs[cardRow.file]
		switch cardRow.kind {
		case headRow:
			folded := app.isFolded(cardRow.file)
			row := ui.Column(ctx.Key("head:"+cardRow.file)).Padding(0, pageInset)
			// The list pins a header at its top, where it is square, and
			// pushes it up at the end of its card, where it rounds its
			// bottom as the card does.
			at := row.LayoutBounds()
			pinned := at.H > 0 && at.Y <= top
			pushed := pinned && at.Y < top
			if pinned && !folded {
				cards.pinned = cardRow.file
			}
			row.Children(func() {
				head := app.fileHeader(ctx, diff, false).Border(cardBorder, theme.Border)
				switch {
				case pinned:
					// The line under the page's top bar is its top border.
					head.BorderWidth(0, cardBorder, cardBorder, cardBorder).Padding(theme.Space(2)+cardBorder, theme.Space(3), theme.Space(2), theme.Space(3))
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
			ui.Column(ctx.Key("body:"+cardRow.file)).Padding(0, pageInset).Children(func() {
				ui.Column(ctx).BorderWidth(0, cardBorder, cardBorder, cardBorder).BorderColor(theme.Border).Radius(0, 0, cardRadius, cardRadius).Children(func() {
					app.fileThreads(ctx, cardRow.file)
					app.diffBody(ctx, diff, true)
					app.cardCorners(ctx)
				})
			})
		case gapRow:
			ui.Box(ctx.Key("gap:" + cardRow.file)).Height(cardGap)
		default:
			ui.Column(ctx).Children(func() { other(cardRow) })
		}
	})
	app.takeFocus(list, "page")
}

// cardCorners paints the rounded bottom corners of a card over its diff.
// The GPU renderer rounds the innermost clip alone, and the diff clips its
// own content, so a card that clipped its content would clip it to a
// square. A ring in the color of the page covers the content outside the
// corners, and the border goes over the content.
func (app *app) cardCorners(ctx *ui.Context) {
	theme := ctx.Theme()
	const ring float32 = cardBorder + cardRadius
	ui.Box(ctx).Absolute().Left(-ring).Top(0).Right(-ring).Bottom(-ring).
		Radius(0, 0, 2*cardRadius, 2*cardRadius).BorderWidth(0, cardRadius, cardRadius, cardRadius).BorderColor(theme.Background).PassThrough()
	ui.Box(ctx).Absolute().Left(-cardBorder).Top(0).Right(-cardBorder).Bottom(-cardBorder).
		Radius(0, 0, cardRadius, cardRadius).BorderWidth(0, cardBorder, cardBorder, cardBorder).BorderColor(theme.Border).PassThrough()
}

func statusLabel(fileChange gitx.FileChange) string {
	switch {
	case fileChange.Untracked:
		return "new, untracked"
	case fileChange.Status == gitx.StatusRenamed:
		return "renamed from " + fileChange.OldPath
	case fileChange.Status == gitx.StatusCopied:
		return "copied from " + fileChange.OldPath
	}
	return string(fileChange.Status)
}

func (app *app) fileHeader(ctx *ui.Context, diff *fileDiff, onPage bool) ui.Element {
	theme := ctx.Theme()
	change := diff.change
	head := ui.Row(ctx).Gap(theme.Space(2.5)).Padding(theme.Space(2), theme.Space(3)).AlignItems(ui.Center).Background(theme.Surface)
	if onPage {
		head.BorderWidth(0, 0, 1, 0).BorderColor(theme.Border)
	}
	head.Children(func() {
		if !onPage {
			app.foldButton(ctx, change.Path)
		}
		if onPage {
			ui.Text(ctx, change.Path).Font("monospace").Bold().SingleLine().Shrink(1).Selectable()
		} else if textLink(ctx, change.Path, func(e ui.Element) { e.Font("monospace").Bold().TextColor(theme.Text) }).Tooltip("Open the file on its own page").Clicked() {
			app.nav.goTo(page{kindFile, change.Path})
		}
		ui.Badge(ctx, statusLabel(change))
		diffStat(ctx, change.Additions, change.Deletions).FontSize(12)
		if diff.state != nil {
			if _, ok := diff.state.SelectedLines(); !ok {
				smallCaption(ctx, "Click a line number, then Shift+click another to select a range").SingleLine().Shrink(1)
			}
		}
		ui.Spacer(ctx)
		if diff.state != nil {
			if selection, ok := diff.state.SelectedLines(); ok {
				label := fmt.Sprintf("Comment on line %d", selection.Start+1)
				if selection.End > selection.Start {
					label = fmt.Sprintf("Comment on lines %d–%d", selection.Start+1, selection.End+1)
				}
				app.cmdButton(ctx, "comment-lines", change.Path, label)
			}
		}
		app.cmdButton(ctx, "comment-file", change.Path, "Comment on file")
		if onPage {
			app.layoutSwitch(ctx)
		}
		app.cmdCheckbox(ctx, "mark-viewed", "mark-not-viewed", change.Path, "Viewed")
	})
	return head
}

// foldButton folds the card of the file to its header, or unfolds it.
func (app *app) foldButton(ctx *ui.Context, file string) {
	folded := app.isFolded(file)
	tip, label := "Fold the diff", "Fold"
	if folded {
		tip, label = "Unfold the diff", "Unfold"
	}
	btn := ghostButton(ctx.Key("fold")).Expanded(!folded).Label(label).Tooltip(tip)
	btn.Children(func() { disclosureArrow(ctx, !folded) })
	if btn.Clicked() {
		app.folded[file] = !folded
	}
}

// layoutSwitch picks split or unified diffs, for every diff in the app.
func (app *app) layoutSwitch(ctx *ui.Context) {
	app.cmdSegmented(ctx, []string{"split-diffs", "unified-diffs"}, "Split", "Unified").Label("Diff layout")
}

func (app *app) fileThreads(ctx *ui.Context, file string) {
	theme := ctx.Theme()
	var threads []*review.Thread
	for _, th := range app.rev.ThreadsOn(file) {
		if th.Anchor.Kind == review.AnchorFile && (len(th.Messages) > 0 || th.ID == app.threadID()) {
			threads = append(threads, th)
		}
	}
	if len(threads) == 0 {
		return
	}
	ui.Column(ctx).Gap(theme.Space(1.5)).Padding(theme.Space(2), theme.Space(3)).Children(func() {
		for _, th := range threads {
			app.threadCard(ctx, th)
		}
	})
}

func (app *app) lineThread(file string, lines editor.DiffLineRange) {
	side := sideOf(lines.Side)
	start, end := lines.Start+1, lines.End+1
	app.newThread(review.Anchor{
		Kind: review.AnchorLines, File: file, Side: side, Start: start, End: end,
		Quote: app.quoteLines(file, side, start, end),
	})
}

func (app *app) diffBody(ctx *ui.Context, diff *fileDiff, fit bool) {
	theme := ctx.Theme()
	switch {
	case diff.loading:
		activityLine(ctx, "Loading…").Padding(theme.Space(4))
		return
	case diff.err != "":
		ui.Text(ctx, diff.err).Padding(theme.Space(4)).TextColor(theme.TextMuted)
		return
	case diff.state == nil:
		return
	}
	diffState := diff.state
	diffState.FitContent = fit
	diffState.Layout = editor.DiffSplit
	if app.cfg.Unified {
		diffState.Layout = editor.DiffUnified
	}
	file := diff.change.Path
	diffState.OnGutterButton = func(r editor.DiffLineRange) {
		if sel, ok := diffState.SelectedLines(); ok && sel.Side == r.Side && sel.Start <= r.Start && r.Start <= sel.End {
			r = sel
		}
		app.lineThread(file, r)
		diffState.SetSelectedLines(editor.DiffLineRange{}, false)
	}
	diffState.Annotations = diffState.Annotations[:0]
	for _, th := range app.rev.ThreadsOn(file) {
		if th.Anchor.Kind != review.AnchorLines || (len(th.Messages) == 0 && th.ID != app.threadID()) {
			continue
		}
		diffState.Annotations = append(diffState.Annotations, editor.DiffAnnotation{
			Side: diffSide(th.Anchor.Side), Line: th.Anchor.End - 1, Height: threadCardHeight + 8, Key: th.ID,
		})
	}
	diffState.RenderAnnotation = func(c *ui.Context, an editor.DiffAnnotation) {
		th := app.rev.Thread(an.Key.(string))
		if th == nil {
			return
		}
		ui.Column(c).Padding(theme.Space(1), theme.Space(3)).Children(func() { app.threadCard(c, th) })
	}
	diffState.Theme = app.codeColors
	view := editor.DiffView(ctx, diffState).FillWidth()
	if !fit {
		app.takeFocus(view, "page")
	}
	if !fit {
		view.Grow(1)
	}
}

// threadCard is a thread in short, where it is anchored: a click opens it
// in the panel.
func (app *app) threadCard(ctx *ui.Context, th *review.Thread) {
	theme := ctx.Theme()
	card := ui.ButtonBase(ctx.Key("card-"+th.ID)).Height(threadCardHeight).Padding(theme.Space(1.5), theme.Space(2.5)).Gap(theme.Space(2.5)).Radius(radiusBox).AlignItems(ui.Center).
		Background(theme.Background).Border(1, theme.Border)
	if th.ID == app.threadID() {
		card.Border(2, theme.Accent)
	} else if card.Hovered() {
		card.Background(theme.SurfaceHover)
	}
	card.Children(func() {
		ui.Column(ctx).Grow(1).Shrink(1).Gap(theme.Space(0.5)).Children(func() {
			ui.Text(ctx, excerpt(cmp.Or(firstMessage(th), "New comment"), 160)).MaxLines(1)
			smallCaption(ctx, app.threadMeta(th)).SingleLine()
		})
		targetMark(ctx, app.state(th.ID))
	})
	if card.Clicked() {
		app.openThread(th.ID)
	}
}

// firstMessage is the text that starts the thread, or "" before it has
// one.
func firstMessage(th *review.Thread) string {
	if len(th.Messages) == 0 {
		return ""
	}
	return th.Messages[0].Text
}

func (app *app) threadMeta(th *review.Thread) string {
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
	case app.busy(th.ID):
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
