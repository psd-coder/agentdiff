package main

import (
	"fmt"
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
	a.history = append(a.history, a.page)
	if len(a.history) > maxHistory {
		a.history = a.history[1:]
	}
	a.page = page
}

// goBack returns to the last page that still exists.
func (a *app) goBack() {
	for len(a.history) > 0 {
		page := a.history[len(a.history)-1]
		a.history = a.history[:len(a.history)-1]
		if a.pageExists(page) {
			a.page = page
			return
		}
	}
}

func (a *app) pageExists(page string) bool {
	if id, ok := strings.CutPrefix(page, "walk:"); ok {
		return a.rev.Walk(id) != nil
	}
	if f, ok := strings.CutPrefix(page, "file:"); ok {
		return a.diffs[f] != nil
	}
	return page != a.page
}

// resetNav forgets the history and the scroll places, for another scope.
func (a *app) resetNav() {
	a.page, a.history = "overview", nil
	a.scrolls = map[string]*ui.ScrollState{}
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

func (a *app) pageLabel(page string) string {
	switch {
	case page == "overview":
		return "Overview"
	case page == "threads":
		return "Comments"
	case page == "feedback":
		return "Feedback"
	case strings.HasPrefix(page, "walk:"):
		if w := a.rev.Walk(strings.TrimPrefix(page, "walk:")); w != nil {
			return excerpt(a.rev.WalkTitle(w), 48)
		}
	case strings.HasPrefix(page, "file:"):
		return path.Base(strings.TrimPrefix(page, "file:"))
	case strings.HasPrefix(page, "group:"):
		var i int
		fmt.Sscanf(page, "group:%d", &i)
		if groups := a.groups(); i >= 0 && i < len(groups) {
			return groups[i].Title
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
	ui.Row(c).Gap(8).Padding(4, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Button(c, "‹ Back to "+a.pageLabel(prev)).Tooltip("Cmd+[").OnClick(a.goBack)
	})
}
