package app

import (
	"path"
	"strings"

	"github.com/egoist/mygo/ui"
)

// maxHistory is how many pages Back can return through.
const maxHistory = 50

// goTo opens a page and remembers the one it leaves, for Back.
func (a *app) goTo(page string) {
	if page == a.page {
		return
	}
	a.history = push(a.history, a.page)
	a.forward = nil
	a.page = page
}

func push(pages []string, page string) []string {
	pages = append(pages, page)
	if len(pages) > maxHistory {
		pages = pages[1:]
	}
	return pages
}

// goBack returns to the last page that still exists.
func (a *app) goBack() {
	if page, ok := a.pop(&a.history); ok {
		a.forward = push(a.forward, a.page)
		a.page = page
	}
}

// goForward opens again the page Back left.
func (a *app) goForward() {
	if page, ok := a.pop(&a.forward); ok {
		a.history = push(a.history, a.page)
		a.page = page
	}
}

// pop takes the last page that still exists off pages.
func (a *app) pop(pages *[]string) (string, bool) {
	for len(*pages) > 0 {
		page := (*pages)[len(*pages)-1]
		*pages = (*pages)[:len(*pages)-1]
		if a.pageExists(page) {
			return page, true
		}
	}
	return "", false
}

func (a *app) pageExists(page string) bool {
	if id, ok := strings.CutPrefix(page, "walk:"); ok {
		return a.rev.Walk(id) != nil
	}
	if f, ok := strings.CutPrefix(page, "file:"); ok {
		return a.diffs[f] != nil
	}
	if id, ok := strings.CutPrefix(page, "archived:"); ok {
		return a.reviews.Find(id) != nil
	}
	if g, ok := strings.CutPrefix(page, "group:"); ok {
		if _, found := a.groupIndex(g); !found {
			return false
		}
	}
	return page != a.page
}

// leaveGoneGroup replaces an open group page with the Overview when the
// groups have changed and none has its id any more. History does not
// keep the gone page.
func (a *app) leaveGoneGroup() {
	if g, ok := strings.CutPrefix(a.page, "group:"); ok {
		if _, found := a.groupIndex(g); !found {
			a.page = "overview"
		}
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
func (a *app) openPage(page string) {
	a.goTo(page)
	a.refocus = "page"
}

// pageScroll is the scroll of a page, which keeps its place. It takes the
// keyboard focus that Focus Page and Cmd+1 to Cmd+3 move to the page, so
// that the arrows and Page Up and Page Down scroll it.
func (a *app) pageScroll(c *ui.Context, page string) ui.Element {
	s := ui.Scroll(c.Key("page-" + page)).TrackScroll(a.scrollOf(page)).Focusable().FocusRing(false)
	a.takeFocus(s, "page")
	return s
}

// scrollOf is the scroll place of a page, so that a page opened again
// shows where the reviewer left it.
func (a *app) scrollOf(page string) *ui.ScrollState {
	s := a.scrolls[page]
	if s == nil {
		s = &ui.ScrollState{}
		a.scrolls[page] = s
	}
	return s
}

// cardsOf is the place of a page of file cards, as scrollOf is of a page
// that scrolls.
func (a *app) cardsOf(page string) *cardPage {
	p := a.lists[page]
	if p == nil {
		p = &cardPage{}
		a.lists[page] = p
	}
	return p
}

func (a *app) pageLabel(page string) string {
	switch {
	case page == "overview":
		return "Overview"
	case page == "threads":
		return "Comments"
	case page == "feedback":
		return "Feedback"
	case page == "all":
		return "All files diff"
	case strings.HasPrefix(page, "archived:"):
		return "Earlier review"
	case strings.HasPrefix(page, "walk:"):
		if w := a.rev.Walk(strings.TrimPrefix(page, "walk:")); w != nil {
			return excerpt(a.rev.WalkTitle(w), 48)
		}
	case strings.HasPrefix(page, "file:"):
		return path.Base(strings.TrimPrefix(page, "file:"))
	case strings.HasPrefix(page, "group:"):
		if i, ok := a.groupIndex(strings.TrimPrefix(page, "group:")); ok {
			return a.groups()[i].Title
		}
	}
	return "previous page"
}

// backBar leads back to the page the reviewer came from.
func (a *app) backBar(c *ui.Context) {
	t := c.Theme()
	if len(a.history) == 0 {
		return
	}
	prev := a.history[len(a.history)-1]
	ui.Row(c).Gap(8).Padding(8, pageInset).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		a.cmdButton(c, "back", "", "‹ Back to "+a.pageLabel(prev))
	})
}
