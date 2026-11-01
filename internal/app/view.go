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

func (a *app) view(c *ui.Context) {
	a.syncCodeTheme(c)
	a.runQueued(c)
	a.updateMenu()
	if a.repo == nil {
		a.welcome(c)
		return
	}
	t := c.Theme()
	a.markSeen()
	a.keys(c)
	ui.Column(c).Fill().Children(func() {
		a.toolbar(c)
		a.saveNotice(c)
		ui.Split(c, &a.sidebarW, func() { a.sidebar(c) }, func() {
			var panel func()
			switch id := a.threadID(); {
			case id != "" && a.rev.Thread(id) != nil:
				panel = func() { a.threadPanel(c) }
			case a.questionOpen():
				panel = func() { a.questionPanel(c) }
			}
			var side func()
			switch {
			case panel != nil && a.logOpen:
				side = func() {
					ui.SplitVerticalPercent(c, &a.logPct, panel, func() { a.logPanel(c) }).Fill()
				}
			case panel != nil:
				side = panel
			case a.logOpen:
				side = func() { a.logPanel(c) }
			default:
				a.main(c)
				return
			}
			ui.SplitPercent(c, &a.mainPct, func() { a.main(c) }, side).Fill()
		}).Grow(1).FillWidth()
	}).Background(t.Background)
	a.settingsModal(c)
	a.compareModal(c)
	a.shortcutsModal(c)
	a.paletteModal(c)
}

func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	ui.SplitVerticalPercent(c, &a.scopePct, func() {
		ui.Column(c).Fill().Background(t.Surface).Children(func() {
			a.scopeList(c)
		})
	}, func() {
		ui.Column(c).Fill().Background(t.Surface).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
			a.scopeHeader(c)
			a.reviewNav(c)
			a.fileNav(c)
		})
	}).Fill()
}

// sidebarHeading names a part of a sidebar, above its sections, with the
// controls end builds after the title, if any.
func sidebarHeading(c *ui.Context, title string, end func()) {
	t := c.Theme()
	ui.Row(c).Gap(6).Padding(10, 8, 4, 8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, strings.ToUpper(title)).FontSize(10).Bold().LetterSpacing(0.6).TextColor(t.TextMuted).Grow(1).SingleLine()
		if end != nil {
			end()
		}
	})
}

// scopeList lists the changes to review: the uncommitted ones, all the
// changes of the branch or range, and its commits. It builds only the
// rows in view, and reads the next page of commits as the list nears the
// last one read.
func (a *app) scopeList(c *ui.Context) {
	t := c.Theme()
	s := &a.changeList
	above := a.changesAbove()
	if a.commits.scrollTop() {
		s.ScrollTo(0, ui.Start)
	}
	last := -1
	if first, l := s.Visible(); first <= l {
		last = l - len(above)
	}
	if a.commits.built(last) {
		a.readCommitPage(last)
	}
	rows := &changeRows{above: above, commits: a.commits.rows, note: a.commitsNote()}
	chosen := rows.find(a.chosen)
	// The list paints its chosen row in the accent color. As in a
	// sidebar, the open scope shows so only while the list has the focus,
	// and in gray without it.
	focused := s.Handle.Focused(c)
	sel := -1
	if focused {
		sel = chosen
	}
	s.Selected = &sel
	s.Key = rows.key
	s.Label = rows.label
	s.Header = rows.header
	list := ui.List(c.Key("changes"), s, rows.len()).Fill().Padding(t.Space(2.5), t.Space(2.5)).Gap(t.Space(0.5)).
		Background(t.Surface).FocusRing(false).Label("Changes")
	ring := list.FocusVisible()
	list.Rows(func(lr ui.ListRow) {
		i := lr.Index
		look := plainItem
		switch {
		case lr.Selected() && ring:
			look = ringedItem
		case lr.Selected():
			look = accentItem
		case !focused && i == chosen:
			look = grayItem
		}
		switch k := rows.kind(i); {
		case k == headingRow:
			ui.Column(c).Background(t.Surface).Children(func() { sidebarHeading(c, rows.label(i), nil) })
		case k == sectionRow:
			ui.Row(c).Padding(t.Space(4), t.Space(2), t.Space(1), t.Space(2)).Background(t.Surface).Children(func() {
				ui.Text(c, rows.label(i)).FontSize(t.FontSize - 1).FontWeight(600).TextColor(t.TextMuted).Grow(1).SingleLine()
			})
		case k == noteRow && a.commits.err != "" && len(rows.commits) > 0:
			ui.Column(c).Gap(4).Padding(4, 8).Children(func() {
				ui.Text(c, rows.label(i)).FontSize(12).TextColor(t.Danger).Selectable()
				textLink(c, "Try again", func(e ui.Element) { e.FontSize(12) }).AlignSelf(ui.Start).
					OnClick(func() { a.readCommitPage(a.commits.failedAt) })
			})
		case k == noteRow && a.commits.err != "":
			ui.Text(c, rows.label(i)).FontSize(12).TextColor(t.Danger).Padding(4, 8).Selectable()
		case k == noteRow:
			ui.Text(c, rows.label(i)).FontSize(12).TextColor(t.TextMuted).Padding(4, 8)
		case k == commitRow:
			cm := rows.commit(i)
			changeItem(c, commitIcon, cm.Subject, look, func() {
				s, _ := rows.scope(i)
				a.scopeMark(c, s)
				ui.Text(c, age(cm.Time)).FontSize(11).TextColor(t.TextMuted).SingleLine()
			}).Tooltip(cm.Short + "  " + cm.Subject + "\n" + cm.Author + ", " + cm.Time.Format("2006-01-02 15:04"))
		default:
			row := rows.above[i]
			item := changeItem(c, row.icon, row.text, look, func() {
				a.scopeMark(c, row.scope)
				if row.badge != "" {
					ui.Badge(c, row.badge)
				}
			})
			if row.tooltip != "" {
				item.Tooltip(row.tooltip)
			}
		}
	})
	a.takeFocus(list, "changes")
	if list.Changed() && sel >= 0 {
		if scope, ok := rows.scope(sel); ok && scope.Key() != a.chosen.Key() {
			a.setScope(scope)
		}
	}
}

// scopeMark shows in the row of scope s in the list of changes what its
// review has new: a spinner while the agent writes the overview, and a dot
// while a run ended that the user did not see.
func (a *app) scopeMark(c *ui.Context, s gitx.Scope) {
	t := c.Theme()
	rev := a.reviews.Kept(a.repo.Root, s)
	if rev == nil {
		return
	}
	ov := a.targets.state(rev, review.TargetOverview)
	switch {
	case ov.status == targetRunning:
		ui.Spinner(c).Size(12, 12).Tooltip("Writing the overview")
	case ov.unseen && ov.status == targetFailed:
		unseenDot(c, t.Danger, "The overview failed")
	case ov.unseen:
		unseenDot(c, t.Accent, "The overview is ready")
	case a.targets.unseenIn(rev):
		unseenDot(c, t.Accent, "New answers")
	}
}

func unseenDot(c *ui.Context, color ui.Color, tip string) {
	ui.Box(c).Size(8, 8).Radius(4).Background(color).Tooltip(tip)
}

// targetMark shows the state of a run target in a row that links to it:
// a spinner while the agent writes it, "failed" or "stopped" when its
// latest answer did not come, and a dot while the user did not see the end
// of its run.
func targetMark(c *ui.Context, s targetState) {
	t := c.Theme()
	switch s.status {
	case targetQueued, targetRunning:
		ui.Spinner(c).Size(12, 12)
		return
	case targetFailed:
		ui.Text(c, "failed").FontSize(11).TextColor(t.Danger)
	case targetStopped:
		ui.Text(c, "stopped").FontSize(11).TextColor(t.TextMuted)
	case targetIdle, targetAnswered:
	}
	if s.unseen {
		unseenDot(c, t.Accent, "New answer")
	}
}

// errorText shows why an answer did not come: the agent's error, or
// Stopped in gray for a run the user stopped.
func errorText(c *ui.Context, err string) {
	t := c.Theme()
	if err == review.Stopped {
		ui.Text(c, err).FontSize(12).TextColor(t.TextMuted)
		return
	}
	ui.Text(c, err).FontSize(12).TextColor(t.Danger).Selectable()
}

// failureBox shows the error of a page's answer in a red box, or Stopped
// in gray for a run the user stopped.
func failureBox(c *ui.Context, err string) {
	t := c.Theme()
	if err == review.Stopped {
		errorText(c, err)
		return
	}
	ui.Column(c).Gap(4).Padding(12).Radius(8).Border(1, t.Danger).Children(func() {
		ui.Text(c, "The agent failed").Bold().TextColor(t.Danger)
		ui.Text(c, err).FontSize(12).Font("monospace").Selectable()
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
func changeItem(c *ui.Context, icon *ui.SVG, label string, look itemLook, end func()) ui.Element {
	t := c.Theme()
	iconColor := t.Accent
	item := ui.Row(c).AlignItems(ui.Center).Gap(t.Space(2)).Padding(t.Space(1.5), t.Space(2)).Radius(t.Radius)
	switch look {
	case ringedItem:
		item.DrawOver(func(p *ui.Painter, r ui.Rect) {
			p.FocusRing(r, [4]float32{t.Radius, t.Radius, t.Radius, t.Radius})
		})
		fallthrough
	case accentItem:
		iconColor = t.AccentText
	case grayItem:
		item.Background(t.SurfacePressed)
	case plainItem:
	}
	return item.Children(func() {
		ui.Icon(c, icon).FontSize(16).TextColor(iconColor)
		ui.Text(c, label).Grow(1).SingleLine()
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
func (a *app) changesAbove() []changeRow {
	rows := []changeRow{{kind: headingRow, text: "Changes"}}
	if a.cmpr.uncommittedShown() {
		rows = append(rows, changeRow{kind: scopeRow, text: "Uncommitted changes", icon: editIcon, scope: gitx.Scope{Kind: gitx.ScopeUncommitted}})
	}
	rows = append(rows, changeRow{kind: sectionRow, text: a.commitsTitle()})
	if s, ok := a.cmpr.allScope(); ok && len(a.commits.rows) > 0 {
		rows = append(rows, changeRow{kind: scopeRow, text: "All changes", icon: layersIcon, scope: s, tooltip: s.Label(), badge: fmt.Sprint(a.commits.total)})
	}
	return rows
}

// commitsNote is the row under the commits, if any.
func (a *app) commitsNote() []changeRow {
	switch {
	case a.commits.err != "" && len(a.commits.rows) > 0:
		return []changeRow{{kind: noteRow, text: "Could not list more commits: " + a.commits.err}}
	case a.commits.err != "":
		return []changeRow{{kind: noteRow, text: "Could not list the commits: " + a.commits.err}}
	case len(a.commits.rows) == 0:
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

func (r *changeRows) len() int { return len(r.above) + len(r.commits) + len(r.note) }

// commit returns the commit of row i, nil for a row that is not one.
func (r *changeRows) commit(i int) *gitx.Commit {
	if j := i - len(r.above); j >= 0 && j < len(r.commits) {
		return &r.commits[j]
	}
	return nil
}

// other returns row i when it is not a commit.
func (r *changeRows) other(i int) changeRow {
	if i < len(r.above) {
		return r.above[i]
	}
	return r.note[i-len(r.above)-len(r.commits)]
}

func (r *changeRows) kind(i int) changeKind {
	if r.commit(i) != nil {
		return commitRow
	}
	return r.other(i).kind
}

func (r *changeRows) key(i int) any {
	if cm := r.commit(i); cm != nil {
		return cm.SHA
	}
	return r.other(i).text
}

func (r *changeRows) label(i int) string {
	if cm := r.commit(i); cm != nil {
		return cm.Subject
	}
	return r.other(i).text
}

// header reports whether row i is one the list cannot choose.
func (r *changeRows) header(i int) bool {
	k := r.kind(i)
	return k != scopeRow && k != commitRow
}

// scope returns the scope row i opens.
func (r *changeRows) scope(i int) (gitx.Scope, bool) {
	if cm := r.commit(i); cm != nil {
		return gitx.Scope{Kind: gitx.ScopeCommit, Commit: cm.SHA}, true
	}
	row := r.other(i)
	return row.scope, row.kind == scopeRow
}

// find returns the row that opens s, -1 for none.
func (r *changeRows) find(s gitx.Scope) int {
	if s.Kind == gitx.ScopeCommit {
		for j := range r.commits {
			if r.commits[j].SHA == s.Commit {
				return len(r.above) + j
			}
		}
		return -1
	}
	key := s.Key()
	for i, row := range r.above {
		if row.kind == scopeRow && row.scope.Key() == key {
			return i
		}
	}
	return -1
}

// commitsTitle names the commit list.
func (a *app) commitsTitle() string {
	switch {
	case a.cmpr.rng != nil:
		return "Commits in " + a.cmpr.rng.Label()
	case a.cmpr.commitBase() != "":
		return "Commits on " + a.cmpr.headBranch()
	}
	return "Recent commits on " + a.cmpr.headBranch()
}

// scopeHeader names the changes under review, above their review.
func (a *app) scopeHeader(c *ui.Context) {
	t := c.Theme()
	add, del := 0, 0
	for _, f := range a.files {
		add += f.Additions
		del += f.Deletions
	}
	ui.Column(c).Gap(2).Padding(10, 16).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.scopeLabel()).Bold().SingleLine().Shrink(1).Tooltip(a.scopeLabel())
			if a.loader.switching() {
				ui.Spinner(c).Size(12, 12).Label("Reading changes")
			}
		})
		if len(a.files) == 0 {
			return
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, plural(len(a.files), "file")).FontSize(11).TextColor(t.TextMuted)
			ui.Text(c, fmt.Sprintf("+%d", add)).FontSize(11).TextColor(t.Success)
			ui.Text(c, fmt.Sprintf("−%d", del)).FontSize(11).TextColor(t.Danger)
			viewed := 0
			for _, f := range a.files {
				if a.rev.Viewed[f.Path] {
					viewed++
				}
			}
			ui.Text(c, fmt.Sprintf("%d of %d viewed", viewed, len(a.files))).FontSize(11).TextColor(t.TextMuted)
		})
	})
}

func (a *app) reviewNav(c *ui.Context) {
	chosen := a.nav.page.String()
	nav := ui.Sidebar(c, &chosen, func() {
		sidebarHeading(c, "Review", nil)
		ui.SidebarItem(c, overview.String(), overviewIcon, "Overview").Children(func() { targetMark(c, a.state(review.TargetOverview)) })
		a.walkItems(c, "", 1)
		ui.SidebarItem(c, page{kind: kindAll}.String(), filesIcon, "All files diff")
		threads := len(a.rev.Threads)
		ui.SidebarItem(c, page{kind: kindThreads}.String(), messageIcon, "Questions & comments").Children(func() {
			if threads > 0 {
				ui.Badge(c, fmt.Sprint(threads))
			}
		})
		feedback := len(review.ExportedThreads(a.rev, review.ExportOptions{}))
		ui.SidebarItem(c, page{kind: kindFeedback}.String(), sendIcon, "Feedback").Children(func() {
			if feedback > 0 {
				ui.Badge(c, fmt.Sprint(feedback))
			}
		})
	}).Shrink(0).Label("Review")
	if nav.Changed() {
		a.nav.goTo(parsePage(chosen))
	}
}

// fileNav lists the files, by path or in their groups, under the switches
// that choose how.
func (a *app) fileNav(c *ui.Context) {
	t := c.Theme()
	chosen := a.nav.page.String()
	groups := a.groups()
	threads := a.rev.ThreadCounts()
	ui.Column(c).Padding(0, 10).Children(func() {
		sidebarHeading(c, "Files", func() {
			a.cmdSegmented(c, []string{"flat-files", "grouped-files"}, "Flat", "Grouped").Label("List files").FontSize(11)
		})
		ui.Row(c).Padding(0, 8, 4, 8).Children(func() {
			a.cmdCheckbox(c, "hide-viewed", "show-viewed", "", "Hide viewed files").FontSize(12)
		})
	})
	nav := ui.Sidebar(c, &chosen, func() {
		if a.cfg.FlatFiles {
			for _, f := range a.files {
				if a.listed(f.Path) {
					a.fileItem(c, f.Path, threads[f.Path])
				}
			}
		}
		for gi, g := range groups {
			if a.cfg.FlatFiles {
				break
			}
			files := slices.DeleteFunc(slices.Clone(g.Files), func(f string) bool { return !a.listed(f) })
			if len(files) == 0 {
				continue
			}
			id := a.groupID(gi)
			open := a.groupOpen(id)
			column := ui.Column(c.Key(id)).Gap(2)
			if gi > 0 {
				column.Margin(8, 0, 0, 0)
			}
			column.Children(func() {
				a.groupItem(c, id, g.Title, len(files), open)
				if *open {
					for _, f := range files {
						a.fileItem(c, f, threads[f]).Level(2)
					}
				}
			})
		}
		if len(a.files) > 0 && !slices.ContainsFunc(a.files, func(f gitx.FileChange) bool { return a.listed(f.Path) }) {
			ui.Text(c, "All files are viewed.").FontSize(12).TextColor(t.TextMuted).Padding(4, 12)
		}
		if a.loader.firstLoad() {
			ui.Row(c).Gap(8).Padding(8, 12).Children(func() {
				ui.Spinner(c).Size(12, 12)
				ui.Text(c, "Reading changes…").FontSize(12).TextColor(t.TextMuted)
			})
		}
	}).Grow(1).Shrink(1).MinHeight(0).Label("Files")
	a.takeFocus(nav, "files")
	// Left and Right close and open the group chosen, as in a tree.
	if p := parsePage(chosen); p.kind == kindGroup {
		if _, ok := a.groupIndex(p.key); ok {
			open := a.groupOpen(p.key)
			switch {
			case *open && nav.Shortcut(0, ui.KeyLeft):
				*open = false
			case !*open && nav.Shortcut(0, ui.KeyRight):
				*open = true
			}
		}
	}
	if nav.Changed() {
		a.nav.goTo(parsePage(chosen))
	}
}

// walkItems lists the walks under parent in the sidebar, each indented
// under the one it goes deeper into.
func (a *app) walkItems(c *ui.Context, parent string, depth int) {
	for _, w := range a.rev.Walks {
		if w.Parent != parent || w.Question != "" {
			continue
		}
		item := ui.SidebarItem(c, page{kindWalk, w.ID}.String(), nil, strings.Repeat("    ", depth)+excerpt(a.rev.WalkTitle(w), 36)).Tooltip(a.rev.WalkTitle(w))
		item.Children(func() { targetMark(c, a.state(w.ID)) })
		if depth < 8 {
			a.walkItems(c, w.ID, depth+1)
		}
	}
}

func (a *app) groupOpen(id string) *bool {
	open := a.groupsOpen[id]
	if open == nil {
		v := true
		open = &v
		a.groupsOpen[id] = open
	}
	return open
}

// groupItem is the title of a group in the sidebar. Choosing it opens
// the group page, and its chevron shows and hides the group's files.
func (a *app) groupItem(c *ui.Context, id, title string, files int, open *bool) {
	t := c.Theme()
	ui.SidebarItem(c, page{kindGroup, id}.String(), nil, title).Tooltip(title).FontSize(12).Bold().Expanded(*open).Children(func() {
		ui.Text(c, fmt.Sprint(files)).FontSize(11).TextColor(t.TextMuted)
		label := "Hide files"
		if !*open {
			label = "Show files"
		}
		btn := ui.ButtonBase(c).Padding(2).Radius(4).Label(label).Tooltip(label).Children(func() {
			arrow := ui.Icon(c, chevronIcon).Size(12, 12).TextColor(t.TextMuted)
			if !*open {
				arrow.Rotate(-90)
			}
		})
		if btn.Hovered() {
			btn.Background(t.Text.Alpha(0.1))
		}
		if btn.Clicked() {
			*open = !*open
		}
	})
}

// fileItem is a file in the sidebar, with the number of threads on it.
// In the flat list, the file's folder shows on a second line under its
// name.
func (a *app) fileItem(c *ui.Context, file string, threads int) ui.Element {
	t := c.Theme()
	viewed := a.rev.Viewed[file]
	item := ui.SidebarItem(c, page{kindFile, file}.String(), nil, path.Base(file)).Tooltip(file)
	dir := path.Dir(file)
	folder := a.cfg.FlatFiles && dir != "."
	if folder {
		// SidebarItem builds the name first, so the name takes the first
		// cell and the marks after it the second. The marks keep their
		// width, and the name is cut only when the row is too narrow.
		item.Grid().ColumnTracks(ui.Fr(1), ui.FitContent()).AlignItems(ui.Center).GapY(1)
	}
	if viewed && a.nav.page != (page{kindFile, file}) {
		item.TextColor(t.TextMuted)
	}
	d := a.diffs[file]
	marks := func() {
		if threads > 0 {
			ui.Badge(c, fmt.Sprint(threads))
		}
		if d != nil {
			ui.Row(c).Gap(4).Children(func() {
				if d.change.Additions > 0 {
					ui.Text(c, fmt.Sprintf("+%d", d.change.Additions)).FontSize(11).TextColor(t.Success)
				}
				if d.change.Deletions > 0 {
					ui.Text(c, fmt.Sprintf("−%d", d.change.Deletions)).FontSize(11).TextColor(t.Danger)
				}
			})
		}
		if viewed {
			ui.Icon(c, checkIcon).FontSize(12).TextColor(t.Success).Label("Viewed")
		}
	}
	item.Children(func() {
		if !folder {
			marks()
			return
		}
		ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(marks)
		// Opacity keeps the row's text color, so the folder stays readable
		// on the accent of the chosen row.
		ui.Text(c, dir).FontSize(11).Opacity(0.7).SingleLine().ColumnStart(1).RowStart(2)
	})
	return item
}

func (a *app) main(c *ui.Context) {
	ui.Column(c).Fill().Children(func() {
		a.backBar(c)
		if a.loadErr != "" {
			a.errorState(c, "Could not read the changes", a.loadErr)
			return
		}
		switch p := a.nav.page; p.kind {
		case kindOverview:
			a.overviewPage(c)
		case kindThreads:
			a.threadsPage(c)
		case kindFeedback:
			a.feedbackPage(c)
		case kindAll:
			a.allPage(c)
		case kindArchived:
			a.archivedPage(c, p.key)
		case kindFile:
			a.filePage(c, p.key)
		case kindWalk:
			a.walkPage(c, p.key)
		case kindGroup:
			a.groupPage(c, p.key)
		}
	})
}

// saveNotice says that the review or the settings could not be saved,
// above every page, until a later save works.
func (a *app) saveNotice(c *ui.Context) {
	if a.saveErr == "" {
		return
	}
	t := c.Theme()
	ui.Column(c).Gap(2).Padding(8, 14).Background(t.Danger.Alpha(0.15)).BorderWidth(0, 0, 1, 0).BorderColor(t.Danger).Children(func() {
		ui.Text(c, "Could not save your changes. AgentDiff tries again on the next change.").Bold()
		ui.Text(c, a.saveErr).FontSize(12).TextColor(t.TextMuted).Selectable()
	})
}

func (a *app) errorState(c *ui.Context, title, detail string) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Gap(8).Padding(24).Children(func() {
		ui.Text(c, title).FontSize(16).Bold()
		ui.Text(c, detail).TextColor(t.TextMuted).Selectable().MaxWidth(600)
	})
}

func (a *app) emptyState(c *ui.Context, title, detail string) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Gap(8).Padding(24).Children(func() {
		ui.Text(c, title).FontSize(16).Bold()
		ui.Text(c, detail).TextColor(t.TextMuted).MaxWidth(520).TextAlign(ui.Center)
	})
}

// age is how long ago t was, in a few characters.
func age(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2006")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
