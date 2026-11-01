package app

import "github.com/psd-coder/agentdiff/internal/gitx"

// commitList is the commit list of the sidebar: the commits read so far,
// and when to read the next page. The app runs the git reads and hands
// their results to it. A result of an older generation is dropped.
type commitList struct {
	rows []gitx.Commit
	// total is how many commits the whole list has.
	total int
	err   string
	// failedAt is the last row in view when a page failed. The list reads
	// the page again once it scrolls away from that row, or on a retry.
	failedAt int
	gen      int
	// log is where the list reads its next pages.
	log gitx.CommitLog
	// key is the range of the rows, and readKey the range of the read
	// that runs.
	key, readKey string
	// reading is set while git reads the list or a page of it. Only one
	// read runs at a time.
	reading bool
	// pageAt is the last row in view when the page being read started.
	pageAt int
	// skip is set when a new list lands: the next build still reports
	// the rows in view of the old list.
	skip bool
	// top is set when a list for a new key lands, until the next build.
	top bool
	// newest is set while the list should open its newest commit once it
	// lands.
	newest bool
}

// reset empties the list and drops the reads that run.
func (list *commitList) reset() { *list = commitList{gen: list.gen + 1} }

// read starts a read of the whole list for key. It returns the generation
// of the read and how many commits to read: for the same key as many as
// the list has, so the list keeps its rows and its place.
func (list *commitList) read(key string) (gen, limit int) {
	list.gen++
	limit = commitPage
	if key == list.key {
		limit = max(limit, len(list.rows))
	}
	list.readKey, list.reading = key, true
	return list.gen, limit
}

// listed takes the result of a read of the whole list. It returns the
// newest commit when the list should open it, or "".
func (list *commitList) listed(gen int, log gitx.CommitLog, rows []gitx.Commit, total int, err error) string {
	if gen != list.gen {
		return ""
	}
	if err != nil {
		rows, total = nil, 0
	}
	list.rows, list.total, list.log, list.err = rows, total, log, errText(err)
	list.reading, list.skip, list.top = false, true, list.readKey != list.key
	list.key = list.readKey
	if !list.newest || len(rows) == 0 {
		return ""
	}
	list.newest = false
	return rows[0].SHA
}

// scrollTop reports whether the view scrolls the list to the top before
// it builds it.
func (list *commitList) scrollTop() bool { return list.top }

// built is what the view reports after each build: last is the last
// commit row in view, or -1. It returns whether to read the next page.
func (list *commitList) built(last int) bool {
	list.top = false
	if list.skip {
		list.skip = false
		return false
	}
	count := len(list.rows)
	return !list.reading && last >= 0 && count < list.total && last >= count-commitsAhead &&
		(list.err == "" || last != list.failedAt)
}

// readPage starts a read of the page after the rows, unless a read runs.
// last is the last commit row in view. It returns the generation of the
// read and the first row of the page.
func (list *commitList) readPage(last int) (gen, from int, ok bool) {
	if list.reading {
		return 0, 0, false
	}
	list.reading, list.pageAt = true, last
	return list.gen, len(list.rows), true
}

// paged takes the result of a page read.
func (list *commitList) paged(gen int, page []gitx.Commit, err error) {
	if gen != list.gen {
		return
	}
	list.reading = false
	list.err, list.failedAt = errText(err), list.pageAt
	if err == nil && len(page) < commitPage {
		// A short page ends the log, whatever git counted.
		list.total = len(list.rows) + len(page)
	}
	list.rows = append(list.rows, page...)
}
