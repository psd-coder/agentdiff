package app

import (
	"path"

	"github.com/egoist/mygo/ui"
)

func (a *app) goBack() { a.nav.back(a.pageGone) }

func (a *app) goForward() { a.nav.forward(a.pageGone) }

// pageGone reports whether the target of p is gone: a deleted walk, a
// file out of the changes, a group that no longer exists or a dropped
// earlier review.
func (a *app) pageGone(p page) bool {
	switch p.kind {
	case kindWalk:
		return a.rev.Walk(p.key) == nil
	case kindFile:
		return a.diffs[p.key] == nil
	case kindArchived:
		return a.reviews.Find(p.key) == nil
	case kindGroup:
		_, found := a.groupIndex(p.key)
		return !found
	case kindOverview, kindThreads, kindFeedback, kindAll:
	}
	return false
}

// leaveGone forgets the gone pages in Back and Forward, and replaces a
// gone page on screen with the Overview.
func (a *app) leaveGone() {
	a.nav.forget(a.pageGone)
	if a.pageGone(a.nav.page) {
		a.nav.replace(overview)
	}
}

// takeFocus gives e the keyboard focus while a.refocus names it, until it
// has it: a dialog that closes in the same frame gives the focus back to
// where it was before, after e asked for it.
func (a *app) takeFocus(e ui.Element, name string) {
	if a.refocus != name {
		return
	}
	if e.Focused() {
		a.refocus = ""
		return
	}
	e.Focus()
}

// openPage goes to the page and puts the keyboard focus on it.
func (a *app) openPage(p page) {
	a.nav.goTo(p)
	a.refocus = "page"
}

// pageScroll is the scroll of a page, which keeps its place. It takes the
// keyboard focus that Focus Page and Cmd+1 to Cmd+3 move to the page, so
// that the arrows and Page Up and Page Down scroll it.
func (a *app) pageScroll(c *ui.Context, p page) ui.Element {
	s := ui.Scroll(c.Key("page-" + p.String())).TrackScroll(a.scrollOf(p.String())).Focusable().FocusRing(false)
	a.takeFocus(s, "page")
	return s
}

// scrollOf is the scroll place of a page or a panel, by its key, so that
// a page opened again shows where the reviewer left it.
func (a *app) scrollOf(key string) *ui.ScrollState {
	s := a.scrolls[key]
	if s == nil {
		s = &ui.ScrollState{}
		a.scrolls[key] = s
	}
	return s
}

// cardsOf is the place of a page of file cards, as scrollOf is of a page
// that scrolls.
func (a *app) cardsOf(p page) *cardPage {
	cards := a.lists[p]
	if cards == nil {
		cards = &cardPage{}
		a.lists[p] = cards
	}
	return cards
}

func (a *app) pageLabel(p page) string {
	switch p.kind {
	case kindOverview:
		return "Overview"
	case kindThreads:
		return "Comments"
	case kindFeedback:
		return "Feedback"
	case kindAll:
		return "All files diff"
	case kindArchived:
		return "Earlier review"
	case kindWalk:
		if w := a.rev.Walk(p.key); w != nil {
			return excerpt(a.rev.WalkTitle(w), 48)
		}
	case kindFile:
		return path.Base(p.key)
	case kindGroup:
		if i, ok := a.groupIndex(p.key); ok {
			return a.groups()[i].Title
		}
	}
	return "previous page"
}

// backBar leads back to the page Back opens.
func (a *app) backBar(c *ui.Context) {
	t := c.Theme()
	prev, ok := a.nav.prev(a.pageGone)
	if !ok {
		return
	}
	ui.Row(c).Gap(8).Padding(8, pageInset).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		a.cmdButton(c, "back", "", "‹ Back to "+a.pageLabel(prev))
	})
}
