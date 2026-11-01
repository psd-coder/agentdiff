package app

import (
	"path"

	"github.com/egoist/mygo/ui"
)

func (app *app) goBack() { app.nav.back(app.pageGone) }

func (app *app) goForward() { app.nav.forward(app.pageGone) }

// pageGone reports whether the target of p is gone: a deleted walk, a
// file out of the changes, a group that no longer exists or a dropped
// earlier review.
func (app *app) pageGone(page page) bool {
	switch page.kind {
	case kindWalk:
		return app.rev.Walk(page.key) == nil
	case kindFile:
		return app.diffs[page.key] == nil
	case kindArchived:
		return app.reviews.Find(page.key) == nil
	case kindGroup:
		_, found := app.groupIndex(page.key)
		return !found
	case kindOverview, kindThreads, kindFeedback, kindAll:
	}
	return false
}

// leaveGone forgets the gone pages in Back and Forward, and replaces a
// gone page on screen with the Overview.
func (app *app) leaveGone() {
	app.nav.forget(app.pageGone)
	if app.pageGone(app.nav.page) {
		app.nav.replace(overview)
	}
}

// takeFocus gives e the keyboard focus while a.refocus names it, until it
// has it: a dialog that closes in the same frame gives the focus back to
// where it was before, after e asked for it.
func (app *app) takeFocus(element ui.Element, name string) {
	if app.refocus != name {
		return
	}
	if element.Focused() {
		app.refocus = ""
		return
	}
	element.Focus()
}

// openPage goes to the page and puts the keyboard focus on it.
func (app *app) openPage(page page) {
	app.nav.goTo(page)
	app.refocus = "page"
}

// pageScroll is the scroll of a page, which keeps its place. It takes the
// keyboard focus that Focus Page and Cmd+1 to Cmd+3 move to the page, so
// that the arrows and Page Up and Page Down scroll it.
func (app *app) pageScroll(ctx *ui.Context, page page) ui.Element {
	scroll := ui.Scroll(ctx.Key("page-" + page.String())).TrackScroll(app.scrollOf(page.String())).Focusable().FocusRing(false)
	app.takeFocus(scroll, "page")
	return scroll
}

// scrollOf is the scroll place of a page or a panel, by its key, so that
// a page opened again shows where the reviewer left it.
func (app *app) scrollOf(key string) *ui.ScrollState {
	scroll := app.scrolls[key]
	if scroll == nil {
		scroll = &ui.ScrollState{}
		app.scrolls[key] = scroll
	}
	return scroll
}

// cardsOf is the place of a page of file cards, as scrollOf is of a page
// that scrolls.
func (app *app) cardsOf(page page) *cardPage {
	cards := app.lists[page]
	if cards == nil {
		cards = &cardPage{}
		app.lists[page] = cards
	}
	return cards
}

func (app *app) pageLabel(page page) string {
	switch page.kind {
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
		if walk := app.rev.Walk(page.key); walk != nil {
			return excerpt(app.rev.WalkTitle(walk), 48)
		}
	case kindFile:
		return path.Base(page.key)
	case kindGroup:
		if index, ok := app.groupIndex(page.key); ok {
			return app.groups()[index].Title
		}
	}
	return "previous page"
}

// backBar leads back to the page Back opens.
func (app *app) backBar(ctx *ui.Context) {
	theme := ctx.Theme()
	prev, ok := app.nav.prev(app.pageGone)
	if !ok {
		return
	}
	ui.Row(ctx).Gap(8).Padding(8, pageInset).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
		app.cmdButton(ctx, "back", "", "‹ Back to "+app.pageLabel(prev))
	})
}
