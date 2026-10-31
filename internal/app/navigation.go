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

func (p page) String() string { return pageNames[p.kind] + p.key }

// parsePage reads the string form of a page. A string that names no page
// is the Overview.
func parsePage(s string) page {
	for kind, name := range pageNames {
		if !strings.HasSuffix(name, ":") {
			if s == name {
				return page{kind: kind}
			}
			continue
		}
		if key, ok := strings.CutPrefix(s, name); ok {
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
func (n *navigation) goTo(p page) {
	if p == n.page {
		return
	}
	n.history = push(n.history, n.page)
	n.ahead = nil
	n.page = p
}

// replace shows p in place of the page on screen, which Back does not
// return to.
func (n *navigation) replace(p page) { n.page = p }

// forget removes the gone pages from Back and Forward.
func (n *navigation) forget(gone func(page) bool) {
	n.history = deleteGone(n.history, gone)
	n.ahead = deleteGone(n.ahead, gone)
}

// prev is the page back opens.
func (n *navigation) prev(gone func(page) bool) (page, bool) {
	i := n.last(n.history, gone)
	if i < 0 {
		return page{}, false
	}
	return n.history[i], true
}

func (n *navigation) back(gone func(page) bool) {
	n.history, n.ahead = n.step(n.history, n.ahead, gone)
}

func (n *navigation) forward(gone func(page) bool) {
	n.ahead, n.history = n.step(n.ahead, n.history, gone)
}

// step opens the last page of from that Back or Forward can open, drops
// the pages after it, and remembers the page it leaves in to.
func (n *navigation) step(from, to []page, gone func(page) bool) ([]page, []page) {
	i := n.last(from, gone)
	if i < 0 {
		return from, to
	}
	to = push(to, n.page)
	n.page = from[i]
	return from[:i], to
}

// last is the index of the last page in pages that Back or Forward can
// open, or -1.
func (n *navigation) last(pages []page, gone func(page) bool) int {
	for i, p := range slices.Backward(pages) {
		if p != n.page && !gone(p) {
			return i
		}
	}
	return -1
}

func push(pages []page, p page) []page {
	pages = append(pages, p)
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
