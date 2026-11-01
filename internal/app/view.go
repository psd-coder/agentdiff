package app

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
)

func (app *app) view(ctx *ui.Context) {
	app.syncCodeTheme(ctx)
	app.runQueued(ctx)
	app.updateMenu()
	if app.repo == nil {
		app.welcome(ctx)
		return
	}
	theme := ctx.Theme()
	app.markSeen()
	app.keys(ctx)
	ui.Column(ctx).Fill().Children(func() {
		app.toolbar(ctx)
		app.saveNotice(ctx)
		ui.Split(ctx, &app.sidebarW, func() { app.sidebar(ctx) }, func() {
			var panel func()
			switch id := app.threadID(); {
			case id != "" && app.rev.Thread(id) != nil:
				panel = func() { app.threadPanel(ctx) }
			case app.questionOpen():
				panel = func() { app.questionPanel(ctx) }
			}
			var side func()
			switch {
			case panel != nil && app.logOpen:
				side = func() {
					ui.SplitVerticalPercent(ctx, &app.logPct, panel, func() { app.logPanel(ctx) }).Fill()
				}
			case panel != nil:
				side = panel
			case app.logOpen:
				side = func() { app.logPanel(ctx) }
			default:
				app.main(ctx)
				return
			}
			ui.SplitPercent(ctx, &app.mainPct, func() { app.main(ctx) }, side).Fill()
		}).Grow(1).FillWidth()
	}).Background(theme.Background)
	app.settingsModal(ctx)
	app.compareModal(ctx)
	app.shortcutsModal(ctx)
	app.paletteModal(ctx)
}

func (app *app) sidebar(ctx *ui.Context) {
	theme := ctx.Theme()
	ui.SplitVerticalPercent(ctx, &app.scopePct, func() {
		ui.Column(ctx).Fill().Background(theme.Surface).Children(func() {
			app.scopeList(ctx)
		})
	}, func() {
		ui.Column(ctx).Fill().Background(theme.Surface).BorderWidth(1, 0, 0, 0).BorderColor(theme.Border).Children(func() {
			app.scopeHeader(ctx)
			app.reviewNav(ctx)
			app.fileNav(ctx)
		})
	}).Fill()
}

// sidebarHeading names a part of a sidebar, above its sections, with the
// controls end builds after the title, if any.
func sidebarHeading(ctx *ui.Context, title string, end func()) {
	theme := ctx.Theme()
	ui.Row(ctx).Gap(6).Padding(10, 8, 4, 8).AlignItems(ui.Center).Children(func() {
		ui.Text(ctx, strings.ToUpper(title)).FontSize(10).Bold().LetterSpacing(0.6).TextColor(theme.TextMuted).Grow(1).SingleLine()
		if end != nil {
			end()
		}
	})
}

// scopeList lists the changes to review: the uncommitted ones, all the
// changes of the branch or range, and its commits. It builds only the
// rows in view, and reads the next page of commits as the list nears the
// last one read.
func (app *app) scopeList(ctx *ui.Context) {
	theme := ctx.Theme()
	changeList := &app.changeList
	above := app.changesAbove()
	if app.commits.scrollTop() {
		changeList.ScrollTo(0, ui.Start)
	}
	last := -1
	if first, lastVisible := changeList.Visible(); first <= lastVisible {
		last = lastVisible - len(above)
	}
	if app.commits.built(last) {
		app.readCommitPage(last)
	}
	rows := &changeRows{above: above, commits: app.commits.rows, note: app.commitsNote()}
	chosen := rows.find(app.chosen)
	// The list paints its chosen row in the accent color. As in a
	// sidebar, the open scope shows so only while the list has the focus,
	// and in gray without it.
	focused := changeList.Handle.Focused(ctx)
	sel := -1
	if focused {
		sel = chosen
	}
	changeList.Selected = &sel
	changeList.Key = rows.key
	changeList.Label = rows.label
	changeList.Header = rows.header
	list := ui.List(ctx.Key("changes"), changeList, rows.len()).Fill().Padding(theme.Space(2.5), theme.Space(2.5)).Gap(theme.Space(0.5)).
		Background(theme.Surface).FocusRing(false).Label("Changes")
	ring := list.FocusVisible()
	list.Rows(func(lr ui.ListRow) {
		index := lr.Index
		look := plainItem
		switch {
		case lr.Selected() && ring:
			look = ringedItem
		case lr.Selected():
			look = accentItem
		case !focused && index == chosen:
			look = grayItem
		}
		switch kind := rows.kind(index); {
		case kind == headingRow:
			ui.Column(ctx).Background(theme.Surface).Children(func() { sidebarHeading(ctx, rows.label(index), nil) })
		case kind == sectionRow:
			ui.Row(ctx).Padding(theme.Space(4), theme.Space(2), theme.Space(1), theme.Space(2)).Background(theme.Surface).Children(func() {
				ui.Text(ctx, rows.label(index)).FontSize(theme.FontSize - 1).FontWeight(600).TextColor(theme.TextMuted).Grow(1).SingleLine()
			})
		case kind == noteRow && app.commits.err != "" && len(rows.commits) > 0:
			ui.Column(ctx).Gap(4).Padding(4, 8).Children(func() {
				ui.Text(ctx, rows.label(index)).FontSize(12).TextColor(theme.Danger).Selectable()
				textLink(ctx, "Try again", func(e ui.Element) { e.FontSize(12) }).AlignSelf(ui.Start).
					OnClick(func() { app.readCommitPage(app.commits.failedAt) })
			})
		case kind == noteRow && app.commits.err != "":
			ui.Text(ctx, rows.label(index)).FontSize(12).TextColor(theme.Danger).Padding(4, 8).Selectable()
		case kind == noteRow:
			ui.Text(ctx, rows.label(index)).FontSize(12).TextColor(theme.TextMuted).Padding(4, 8)
		case kind == commitRow:
			cm := rows.commit(index)
			changeItem(ctx, commitIcon, cm.Subject, look, func() {
				commitScope, _ := rows.scope(index)
				app.scopeMark(ctx, commitScope)
				ui.Text(ctx, age(cm.Time)).FontSize(11).TextColor(theme.TextMuted).SingleLine()
			}).Tooltip(cm.Short + "  " + cm.Subject + "\n" + cm.Author + ", " + cm.Time.Format("2006-01-02 15:04"))
		default:
			row := rows.above[index]
			item := changeItem(ctx, row.icon, row.text, look, func() {
				app.scopeMark(ctx, row.scope)
				if row.badge != "" {
					ui.Badge(ctx, row.badge)
				}
			})
			if row.tooltip != "" {
				item.Tooltip(row.tooltip)
			}
		}
	})
	app.takeFocus(list, "changes")
	if list.Changed() && sel >= 0 {
		if scope, ok := rows.scope(sel); ok && scope.Key() != app.chosen.Key() {
			app.setScope(scope)
		}
	}
}

// scopeMark shows in the row of scope s in the list of changes what its
// review has new: a spinner while the agent writes the overview, and a dot
// while a run ended that the user did not see.
func (app *app) scopeMark(ctx *ui.Context, scope gitx.Scope) {
	theme := ctx.Theme()
	rev := app.reviews.Kept(app.repo.Root, scope)
	if rev == nil {
		return
	}
	ov := app.targets.state(rev, review.TargetOverview)
	switch {
	case ov.status == targetRunning:
		ui.Spinner(ctx).Size(12, 12).Tooltip("Writing the overview")
	case ov.unseen && ov.status == targetFailed:
		unseenDot(ctx, theme.Danger, "The overview failed")
	case ov.unseen:
		unseenDot(ctx, theme.Accent, "The overview is ready")
	case app.targets.unseenIn(rev):
		unseenDot(ctx, theme.Accent, "New answers")
	}
}

func unseenDot(ctx *ui.Context, color ui.Color, tip string) {
	ui.Box(ctx).Size(8, 8).Radius(4).Background(color).Tooltip(tip)
}

// targetMark shows the state of a run target in a row that links to it:
// a spinner while the agent writes it, "failed" or "stopped" when its
// latest answer did not come, and a dot while the user did not see the end
// of its run.
func targetMark(ctx *ui.Context, state targetState) {
	theme := ctx.Theme()
	switch state.status {
	case targetQueued, targetRunning:
		ui.Spinner(ctx).Size(12, 12)
		return
	case targetFailed:
		ui.Text(ctx, "failed").FontSize(11).TextColor(theme.Danger)
	case targetStopped:
		ui.Text(ctx, "stopped").FontSize(11).TextColor(theme.TextMuted)
	case targetIdle, targetAnswered:
	}
	if state.unseen {
		unseenDot(ctx, theme.Accent, "New answer")
	}
}

// errorText shows why an answer did not come: the agent's error, or
// Stopped in gray for a run the user stopped.
func errorText(ctx *ui.Context, err string) {
	theme := ctx.Theme()
	if err == review.Stopped {
		ui.Text(ctx, err).FontSize(12).TextColor(theme.TextMuted)
		return
	}
	ui.Text(ctx, err).FontSize(12).TextColor(theme.Danger).Selectable()
}

// failureBox shows the error of a page's answer in a red box, or Stopped
// in gray for a run the user stopped.
func failureBox(ctx *ui.Context, err string) {
	theme := ctx.Theme()
	if err == review.Stopped {
		errorText(ctx, err)
		return
	}
	ui.Column(ctx).Gap(4).Padding(12).Radius(8).Border(1, theme.Danger).Children(func() {
		ui.Text(ctx, "The agent failed").Bold().TextColor(theme.Danger)
		ui.Text(ctx, err).FontSize(12).Font("monospace").Selectable()
	})
}

// itemLook is how a row of the list of changes shows that it is chosen.
type itemLook uint8

const (
	plainItem itemLook = iota
	// accentItem is the row the list chose, which the list paints.
	accentItem
	// ringedItem is the row the list chose, with a focus ring.
	ringedItem
	// grayItem is the open scope while the list has no focus.
	grayItem
)

// changeItem builds a row that opens a scope, in the look of a sidebar
// item: the icon, the label, then what end builds.
func changeItem(ctx *ui.Context, icon *ui.SVG, label string, look itemLook, end func()) ui.Element {
	theme := ctx.Theme()
	iconColor := theme.Accent
	item := ui.Row(ctx).AlignItems(ui.Center).Gap(theme.Space(2)).Padding(theme.Space(1.5), theme.Space(2)).Radius(theme.Radius)
	switch look {
	case ringedItem:
		item.DrawOver(func(p *ui.Painter, r ui.Rect) {
			p.FocusRing(r, [4]float32{theme.Radius, theme.Radius, theme.Radius, theme.Radius})
		})
		fallthrough
	case accentItem:
		iconColor = theme.AccentText
	case grayItem:
		item.Background(theme.SurfacePressed)
	case plainItem:
	}
	return item.Children(func() {
		ui.Icon(ctx, icon).FontSize(16).TextColor(iconColor)
		ui.Text(ctx, label).Grow(1).SingleLine()
		end()
	})
}

// changeKind is what a row of the list of changes shows.
type changeKind uint8

const (
	headingRow changeKind = iota
	sectionRow
	scopeRow
	commitRow
	// noteRow is under the commits: that there are none, or an error.
	noteRow
)

// changeRow is a row of the list of changes other than a commit.
type changeRow struct {
	kind    changeKind
	text    string
	icon    *ui.SVG
	scope   gitx.Scope
	tooltip string
	badge   string
}

// changesAbove are the rows of the list of changes above the commits.
func (app *app) changesAbove() []changeRow {
	rows := []changeRow{{kind: headingRow, text: "Changes"}}
	if app.cmpr.uncommittedShown() {
		rows = append(rows, changeRow{kind: scopeRow, text: "Uncommitted changes", icon: editIcon, scope: gitx.Scope{Kind: gitx.ScopeUncommitted}})
	}
	rows = append(rows, changeRow{kind: sectionRow, text: app.commitsTitle()})
	if scope, ok := app.cmpr.allScope(); ok && len(app.commits.rows) > 0 {
		rows = append(rows, changeRow{kind: scopeRow, text: "All changes", icon: layersIcon, scope: scope, tooltip: scope.Label(), badge: fmt.Sprint(app.commits.total)})
	}
	return rows
}

// commitsNote is the row under the commits, if any.
func (app *app) commitsNote() []changeRow {
	switch {
	case app.commits.err != "" && len(app.commits.rows) > 0:
		return []changeRow{{kind: noteRow, text: "Could not list more commits: " + app.commits.err}}
	case app.commits.err != "":
		return []changeRow{{kind: noteRow, text: "Could not list the commits: " + app.commits.err}}
	case len(app.commits.rows) == 0:
		return []changeRow{{kind: noteRow, text: "No commits"}}
	}
	return nil
}

// changeRows are the rows of the list of changes: those above the
// commits, the commits, and the note under them.
type changeRows struct {
	above   []changeRow
	commits []gitx.Commit
	note    []changeRow
}

func (rows *changeRows) len() int { return len(rows.above) + len(rows.commits) + len(rows.note) }

// commit returns the commit of row i, nil for a row that is not one.
func (rows *changeRows) commit(index int) *gitx.Commit {
	if commitIndex := index - len(rows.above); commitIndex >= 0 && commitIndex < len(rows.commits) {
		return &rows.commits[commitIndex]
	}
	return nil
}

// other returns row i when it is not a commit.
func (rows *changeRows) other(index int) changeRow {
	if index < len(rows.above) {
		return rows.above[index]
	}
	return rows.note[index-len(rows.above)-len(rows.commits)]
}

func (rows *changeRows) kind(index int) changeKind {
	if rows.commit(index) != nil {
		return commitRow
	}
	return rows.other(index).kind
}

func (rows *changeRows) key(index int) any {
	if cm := rows.commit(index); cm != nil {
		return cm.SHA
	}
	return rows.other(index).text
}

func (rows *changeRows) label(index int) string {
	if cm := rows.commit(index); cm != nil {
		return cm.Subject
	}
	return rows.other(index).text
}

// header reports whether row i is one the list cannot choose.
func (rows *changeRows) header(index int) bool {
	kind := rows.kind(index)
	return kind != scopeRow && kind != commitRow
}

// scope returns the scope row i opens.
func (rows *changeRows) scope(index int) (gitx.Scope, bool) {
	if cm := rows.commit(index); cm != nil {
		return gitx.Scope{Kind: gitx.ScopeCommit, Commit: cm.SHA}, true
	}
	row := rows.other(index)
	return row.scope, row.kind == scopeRow
}

// find returns the row that opens s, -1 for none.
func (rows *changeRows) find(scope gitx.Scope) int {
	if scope.Kind == gitx.ScopeCommit {
		for j := range rows.commits {
			if rows.commits[j].SHA == scope.Commit {
				return len(rows.above) + j
			}
		}
		return -1
	}
	key := scope.Key()
	for i, row := range rows.above {
		if row.kind == scopeRow && row.scope.Key() == key {
			return i
		}
	}
	return -1
}

// commitsTitle names the commit list.
func (app *app) commitsTitle() string {
	switch {
	case app.cmpr.rng != nil:
		return "Commits in " + app.cmpr.rng.Label()
	case app.cmpr.commitBase() != "":
		return "Commits on " + app.cmpr.headBranch()
	}
	return "Recent commits on " + app.cmpr.headBranch()
}

// scopeHeader names the changes under review, above their review.
func (app *app) scopeHeader(ctx *ui.Context) {
	theme := ctx.Theme()
	add, del := 0, 0
	for _, f := range app.files {
		add += f.Additions
		del += f.Deletions
	}
	ui.Column(ctx).Gap(2).Padding(10, 16).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
		ui.Row(ctx).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(ctx, app.scopeLabel()).Bold().SingleLine().Shrink(1).Tooltip(app.scopeLabel())
			if app.loader.switching() {
				ui.Spinner(ctx).Size(12, 12).Label("Reading changes")
			}
		})
		if len(app.files) == 0 {
			return
		}
		ui.Row(ctx).Gap(8).Children(func() {
			ui.Text(ctx, plural(len(app.files), "file")).FontSize(11).TextColor(theme.TextMuted)
			ui.Text(ctx, fmt.Sprintf("+%d", add)).FontSize(11).TextColor(theme.Success)
			ui.Text(ctx, fmt.Sprintf("−%d", del)).FontSize(11).TextColor(theme.Danger)
			viewed := 0
			for _, f := range app.files {
				if app.rev.Viewed[f.Path] {
					viewed++
				}
			}
			ui.Text(ctx, fmt.Sprintf("%d of %d viewed", viewed, len(app.files))).FontSize(11).TextColor(theme.TextMuted)
		})
	})
}

func (app *app) reviewNav(ctx *ui.Context) {
	chosen := app.nav.page.String()
	nav := ui.Sidebar(ctx, &chosen, func() {
		sidebarHeading(ctx, "Review", nil)
		ui.SidebarItem(ctx, overview.String(), overviewIcon, "Overview").Children(func() { targetMark(ctx, app.state(review.TargetOverview)) })
		app.walkItems(ctx, "", 1)
		ui.SidebarItem(ctx, page{kind: kindAll}.String(), filesIcon, "All files diff")
		threads := len(app.rev.Threads)
		ui.SidebarItem(ctx, page{kind: kindThreads}.String(), messageIcon, "Questions & comments").Children(func() {
			if threads > 0 {
				ui.Badge(ctx, fmt.Sprint(threads))
			}
		})
		feedback := len(review.ExportedThreads(app.rev, review.ExportOptions{}))
		ui.SidebarItem(ctx, page{kind: kindFeedback}.String(), sendIcon, "Feedback").Children(func() {
			if feedback > 0 {
				ui.Badge(ctx, fmt.Sprint(feedback))
			}
		})
	}).Shrink(0).Label("Review")
	if nav.Changed() {
		app.nav.goTo(parsePage(chosen))
	}
}

// fileNav lists the files, by path or in their groups, under the switches
// that choose how.
func (app *app) fileNav(ctx *ui.Context) {
	theme := ctx.Theme()
	chosen := app.nav.page.String()
	groups := app.groups()
	threads := app.rev.ThreadCounts()
	ui.Column(ctx).Padding(0, 10).Children(func() {
		sidebarHeading(ctx, "Files", func() {
			app.cmdSegmented(ctx, []string{"flat-files", "grouped-files"}, "Flat", "Grouped").Label("List files").FontSize(11)
		})
		ui.Row(ctx).Padding(0, 8, 4, 8).Children(func() {
			app.cmdCheckbox(ctx, "hide-viewed", "show-viewed", "", "Hide viewed files").FontSize(12)
		})
	})
	nav := ui.Sidebar(ctx, &chosen, func() {
		if app.cfg.FlatFiles {
			for _, f := range app.files {
				if app.listed(f.Path) {
					app.fileItem(ctx, f.Path, threads[f.Path])
				}
			}
		}
		for gi, g := range groups {
			if app.cfg.FlatFiles {
				break
			}
			files := slices.DeleteFunc(slices.Clone(g.Files), func(f string) bool { return !app.listed(f) })
			if len(files) == 0 {
				continue
			}
			id := app.groupID(gi)
			open := app.groupOpen(id)
			column := ui.Column(ctx.Key(id)).Gap(2)
			if gi > 0 {
				column.Margin(8, 0, 0, 0)
			}
			column.Children(func() {
				app.groupItem(ctx, id, g.Title, len(files), open)
				if *open {
					for _, f := range files {
						app.fileItem(ctx, f, threads[f]).Level(2)
					}
				}
			})
		}
		if len(app.files) > 0 && !slices.ContainsFunc(app.files, func(f gitx.FileChange) bool { return app.listed(f.Path) }) {
			ui.Text(ctx, "All files are viewed.").FontSize(12).TextColor(theme.TextMuted).Padding(4, 12)
		}
		if app.loader.firstLoad() {
			ui.Row(ctx).Gap(8).Padding(8, 12).Children(func() {
				ui.Spinner(ctx).Size(12, 12)
				ui.Text(ctx, "Reading changes…").FontSize(12).TextColor(theme.TextMuted)
			})
		}
	}).Grow(1).Shrink(1).MinHeight(0).Label("Files")
	app.takeFocus(nav, "files")
	// Left and Right close and open the group chosen, as in a tree.
	if chosenPage := parsePage(chosen); chosenPage.kind == kindGroup {
		if _, ok := app.groupIndex(chosenPage.key); ok {
			open := app.groupOpen(chosenPage.key)
			switch {
			case *open && nav.Shortcut(0, ui.KeyLeft):
				*open = false
			case !*open && nav.Shortcut(0, ui.KeyRight):
				*open = true
			}
		}
	}
	if nav.Changed() {
		app.nav.goTo(parsePage(chosen))
	}
}

// walkItems lists the walks under parent in the sidebar, each indented
// under the one it goes deeper into.
func (app *app) walkItems(ctx *ui.Context, parent string, depth int) {
	for _, w := range app.rev.Walks {
		if w.Parent != parent || w.Question != "" {
			continue
		}
		item := ui.SidebarItem(ctx, page{kindWalk, w.ID}.String(), nil, strings.Repeat("    ", depth)+excerpt(app.rev.WalkTitle(w), 36)).Tooltip(app.rev.WalkTitle(w))
		item.Children(func() { targetMark(ctx, app.state(w.ID)) })
		if depth < 8 {
			app.walkItems(ctx, w.ID, depth+1)
		}
	}
}

func (app *app) groupOpen(id string) *bool {
	open := app.groupsOpen[id]
	if open == nil {
		isOpen := true
		open = &isOpen
		app.groupsOpen[id] = open
	}
	return open
}

// groupItem is the title of a group in the sidebar. Choosing it opens
// the group page, and its chevron shows and hides the group's files.
func (app *app) groupItem(ctx *ui.Context, id, title string, files int, open *bool) {
	theme := ctx.Theme()
	ui.SidebarItem(ctx, page{kindGroup, id}.String(), nil, title).Tooltip(title).FontSize(12).Bold().Expanded(*open).Children(func() {
		ui.Text(ctx, fmt.Sprint(files)).FontSize(11).TextColor(theme.TextMuted)
		label := "Hide files"
		if !*open {
			label = "Show files"
		}
		btn := ui.ButtonBase(ctx).Padding(2).Radius(4).Label(label).Tooltip(label).Children(func() {
			arrow := ui.Icon(ctx, chevronIcon).Size(12, 12).TextColor(theme.TextMuted)
			if !*open {
				arrow.Rotate(-90)
			}
		})
		if btn.Hovered() {
			btn.Background(theme.Text.Alpha(0.1))
		}
		if btn.Clicked() {
			*open = !*open
		}
	})
}

// fileItem is a file in the sidebar, with the number of threads on it.
// In the flat list, the file's folder shows on a second line under its
// name.
func (app *app) fileItem(ctx *ui.Context, file string, threads int) ui.Element {
	theme := ctx.Theme()
	viewed := app.rev.Viewed[file]
	item := ui.SidebarItem(ctx, page{kindFile, file}.String(), nil, path.Base(file)).Tooltip(file)
	dir := path.Dir(file)
	folder := app.cfg.FlatFiles && dir != "."
	if folder {
		// SidebarItem builds the name first, so the name takes the first
		// cell and the marks after it the second. The marks keep their
		// width, and the name is cut only when the row is too narrow.
		item.Grid().ColumnTracks(ui.Fr(1), ui.FitContent()).AlignItems(ui.Center).GapY(1)
	}
	if viewed && app.nav.page != (page{kindFile, file}) {
		item.TextColor(theme.TextMuted)
	}
	diff := app.diffs[file]
	marks := func() {
		if threads > 0 {
			ui.Badge(ctx, fmt.Sprint(threads))
		}
		if diff != nil {
			ui.Row(ctx).Gap(4).Children(func() {
				if diff.change.Additions > 0 {
					ui.Text(ctx, fmt.Sprintf("+%d", diff.change.Additions)).FontSize(11).TextColor(theme.Success)
				}
				if diff.change.Deletions > 0 {
					ui.Text(ctx, fmt.Sprintf("−%d", diff.change.Deletions)).FontSize(11).TextColor(theme.Danger)
				}
			})
		}
		if viewed {
			ui.Icon(ctx, checkIcon).FontSize(12).TextColor(theme.Success).Label("Viewed")
		}
	}
	item.Children(func() {
		if !folder {
			marks()
			return
		}
		ui.Row(ctx).Gap(theme.Space(2)).AlignItems(ui.Center).Children(marks)
		// Opacity keeps the row's text color, so the folder stays readable
		// on the accent of the chosen row.
		ui.Text(ctx, dir).FontSize(11).Opacity(0.7).SingleLine().ColumnStart(1).RowStart(2)
	})
	return item
}

func (app *app) main(ctx *ui.Context) {
	ui.Column(ctx).Fill().Children(func() {
		app.backBar(ctx)
		if app.loadErr != "" {
			app.errorState(ctx, "Could not read the changes", app.loadErr)
			return
		}
		switch current := app.nav.page; current.kind {
		case kindOverview:
			app.overviewPage(ctx)
		case kindThreads:
			app.threadsPage(ctx)
		case kindFeedback:
			app.feedbackPage(ctx)
		case kindAll:
			app.allPage(ctx)
		case kindArchived:
			app.archivedPage(ctx, current.key)
		case kindFile:
			app.filePage(ctx, current.key)
		case kindWalk:
			app.walkPage(ctx, current.key)
		case kindGroup:
			app.groupPage(ctx, current.key)
		}
	})
}

// saveNotice says that the review or the settings could not be saved,
// above every page, until a later save works.
func (app *app) saveNotice(ctx *ui.Context) {
	if app.saveErr == "" {
		return
	}
	theme := ctx.Theme()
	ui.Column(ctx).Gap(2).Padding(8, 14).Background(theme.Danger.Alpha(0.15)).BorderWidth(0, 0, 1, 0).BorderColor(theme.Danger).Children(func() {
		ui.Text(ctx, "Could not save your changes. AgentDiff tries again on the next change.").Bold()
		ui.Text(ctx, app.saveErr).FontSize(12).TextColor(theme.TextMuted).Selectable()
	})
}

func (app *app) errorState(ctx *ui.Context, title, detail string) {
	theme := ctx.Theme()
	ui.Column(ctx).Grow(1).Center().Gap(8).Padding(24).Children(func() {
		ui.Text(ctx, title).FontSize(16).Bold()
		ui.Text(ctx, detail).TextColor(theme.TextMuted).Selectable().MaxWidth(600)
	})
}

func (app *app) emptyState(ctx *ui.Context, title, detail string) {
	theme := ctx.Theme()
	ui.Column(ctx).Grow(1).Center().Gap(8).Padding(24).Children(func() {
		ui.Text(ctx, title).FontSize(16).Bold()
		ui.Text(ctx, detail).TextColor(theme.TextMuted).MaxWidth(520).TextAlign(ui.Center)
	})
}

// age is how long ago t was, in a few characters.
func age(when time.Time) string {
	elapsed := time.Since(when)
	switch {
	case elapsed < time.Minute:
		return "now"
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh", int(elapsed.Hours()))
	case elapsed < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(elapsed.Hours()/24))
	case elapsed < 365*24*time.Hour:
		return when.Format("Jan 2")
	}
	return when.Format("Jan 2006")
}

func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}
