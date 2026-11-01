package app

import (
	"slices"
	"strings"
)

type pageKind int

const (
	kindOverview pageKind = iota
	kindThreads
	kindFeedback
	kindAll
	kindWalk
	kindFile
	kindGroup
	kindArchived
)

// page is a page of the main area. key names its target: the id of a
// walk or an earlier review, the path of a file or the id of a group.
type page struct {
	kind pageKind
	key  string
}

var overview = page{kind: kindOverview}

// pageNames are the string forms of the pages, without the key. The
// string form is the widget key of the page and its sidebar id.
var pageNames = map[pageKind]string{
	kindOverview: "overview",
	kindThreads:  "threads",
	kindFeedback: "feedback",
	kindAll:      "all",
	kindWalk:     "walk:",
	kindFile:     "file:",
	kindGroup:    "group:",
	kindArchived: "archived:",
}

func (page page) String() string { return pageNames[page.kind] + page.key }

// parsePage reads the string form of a page. A string that names no page
// is the Overview.
func parsePage(text string) page {
	for kind, name := range pageNames {
		if !strings.HasSuffix(name, ":") {
			if text == name {
				return page{kind: kind}
			}
			continue
		}
		if key, ok := strings.CutPrefix(text, name); ok {
			return page{kind, key}
		}
	}
	return overview
}

// maxHistory is how many pages Back can return through.
const maxHistory = 50

// navigation is the page on screen with the pages Back and Forward open.
// Back and Forward skip the pages gone reports, and the pages equal to
// the page on screen.
type navigation struct {
	page    page
	history []page
	ahead   []page
}

// goTo opens p and remembers the page it leaves, for Back.
func (navigation *navigation) goTo(page page) {
	if page == navigation.page {
		return
	}
	navigation.history = push(navigation.history, navigation.page)
	navigation.ahead = nil
	navigation.page = page
}

// replace shows p in place of the page on screen, which Back does not
// return to.
func (navigation *navigation) replace(page page) { navigation.page = page }

// forget removes the gone pages from Back and Forward.
func (navigation *navigation) forget(gone func(page) bool) {
	navigation.history = deleteGone(navigation.history, gone)
	navigation.ahead = deleteGone(navigation.ahead, gone)
}

// prev is the page back opens.
func (navigation *navigation) prev(gone func(page) bool) (page, bool) {
	index := navigation.last(navigation.history, gone)
	if index < 0 {
		return page{}, false
	}
	return navigation.history[index], true
}

func (navigation *navigation) back(gone func(page) bool) {
	navigation.history, navigation.ahead = navigation.step(navigation.history, navigation.ahead, gone)
}

func (navigation *navigation) forward(gone func(page) bool) {
	navigation.ahead, navigation.history = navigation.step(navigation.ahead, navigation.history, gone)
}

// step opens the last page of from that Back or Forward can open, drops
// the pages after it, and remembers the page it leaves in to.
func (navigation *navigation) step(from, to []page, gone func(page) bool) ([]page, []page) {
	index := navigation.last(from, gone)
	if index < 0 {
		return from, to
	}
	to = push(to, navigation.page)
	navigation.page = from[index]
	return from[:index], to
}

// last is the index of the last page in pages that Back or Forward can
// open, or -1.
func (navigation *navigation) last(pages []page, gone func(page) bool) int {
	for i, p := range slices.Backward(pages) {
		if p != navigation.page && !gone(p) {
			return i
		}
	}
	return -1
}

func push(pages []page, page page) []page {
	pages = append(pages, page)
	if len(pages) > maxHistory {
		pages = pages[1:]
	}
	return pages
}

func deleteGone(pages []page, gone func(page) bool) []page {
	var kept []page
	for _, p := range pages {
		if !gone(p) {
			kept = append(kept, p)
		}
	}
	return kept
}
