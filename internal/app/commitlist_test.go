package app

import (
	"errors"
	"fmt"
	"testing"

	"github.com/psd-coder/agentdiff/internal/gitx"
)

// fakeCommits returns n commits, the SHA of each its index from start.
func fakeCommits(start, n int) []gitx.Commit {
	rows := make([]gitx.Commit, n)
	for i := range rows {
		rows[i].SHA = fmt.Sprint(start + i)
	}
	return rows
}

// landedList is a list with the first 50 of 130 commits, which the view
// built once after it landed.
func landedList() *commitList {
	l := &commitList{}
	gen, _ := l.read("k")
	l.listed(gen, gitx.CommitLog{}, fakeCommits(0, 50), 130, nil)
	l.built(-1)
	return l
}

func TestCommitList(t *testing.T) {
	errGit := errors.New("git failed")
	tests := []struct {
		name string
		run  func(t *testing.T, l *commitList)
	}{
		{"skips one build after a new list", func(t *testing.T, l *commitList) {
			gen, _ := l.read("k")
			l.listed(gen, gitx.CommitLog{}, fakeCommits(0, 50), 130, nil)
			if l.built(49) {
				t.Error("the first build after the list read a page")
			}
			if !l.built(49) {
				t.Error("the second build read no page")
			}
		}},
		{"reads a page only near the end of the rows", func(t *testing.T, l *commitList) {
			*l = *landedList()
			if l.built(39) || l.built(-1) {
				t.Error("read a page far from the end")
			}
			if !l.built(40) {
				t.Error("read no page ten rows from the end")
			}
		}},
		{"reads one page at a time", func(t *testing.T, l *commitList) {
			*l = *landedList()
			if _, from, ok := l.readPage(49); !ok || from != 50 {
				t.Fatalf("first page: from %d, ok %v", from, ok)
			}
			if _, _, ok := l.readPage(49); ok || l.built(49) {
				t.Error("started a second read")
			}
		}},
		{"a failed page blocks reads at its row until a retry", func(t *testing.T, l *commitList) {
			*l = *landedList()
			gen, _, _ := l.readPage(45)
			l.paged(gen, nil, errGit)
			if l.err != "git failed" || len(l.rows) != 50 {
				t.Fatalf("after the failed page: %d rows, error %q", len(l.rows), l.err)
			}
			if l.built(45) {
				t.Error("read the failed page again at the same row")
			}
			gen, from, ok := l.readPage(l.failedAt)
			if !ok || from != 50 {
				t.Fatalf("retry: from %d, ok %v", from, ok)
			}
			l.paged(gen, fakeCommits(50, 50), nil)
			if l.err != "" || len(l.rows) != 100 {
				t.Errorf("after the retry: %d rows, error %q", len(l.rows), l.err)
			}
		}},
		{"a failed page reads again away from its row", func(t *testing.T, l *commitList) {
			*l = *landedList()
			gen, _, _ := l.readPage(45)
			l.paged(gen, nil, errGit)
			if !l.built(44) {
				t.Error("read no page after a scroll")
			}
		}},
		{"a short page ends the list", func(t *testing.T, l *commitList) {
			*l = *landedList()
			gen, _, _ := l.readPage(49)
			l.paged(gen, fakeCommits(50, 10), nil)
			if l.total != 60 || len(l.rows) != 60 {
				t.Errorf("%d of %d rows", len(l.rows), l.total)
			}
			if l.built(59) {
				t.Error("read past the end")
			}
		}},
		{"drops a list of an old generation", func(t *testing.T, l *commitList) {
			old, _ := l.read("k")
			gen, _ := l.read("k")
			l.listed(old, gitx.CommitLog{}, fakeCommits(0, 50), 130, nil)
			if len(l.rows) != 0 || !l.reading {
				t.Fatalf("took the old list: %d rows, reading %v", len(l.rows), l.reading)
			}
			l.listed(gen, gitx.CommitLog{}, fakeCommits(0, 50), 130, nil)
			if len(l.rows) != 50 || l.reading {
				t.Errorf("new list: %d rows, reading %v", len(l.rows), l.reading)
			}
		}},
		{"drops a page of an old generation", func(t *testing.T, l *commitList) {
			*l = *landedList()
			old, _, _ := l.readPage(49)
			gen, _ := l.read("k")
			l.paged(old, fakeCommits(50, 50), nil)
			if len(l.rows) != 50 || !l.reading {
				t.Errorf("took the old page: %d rows, reading %v", len(l.rows), l.reading)
			}
			l.listed(gen, gitx.CommitLog{}, fakeCommits(0, 50), 130, nil)
			if l.reading {
				t.Error("the new list still reads")
			}
		}},
		{"reads as many rows again for the same key, and keeps its place", func(t *testing.T, l *commitList) {
			*l = *landedList()
			gen, _, _ := l.readPage(49)
			l.paged(gen, fakeCommits(50, 50), nil)
			gen, limit := l.read("k")
			if limit != 100 {
				t.Errorf("read %d rows again, want 100", limit)
			}
			l.listed(gen, gitx.CommitLog{}, fakeCommits(0, 100), 131, nil)
			if l.scrollTop() {
				t.Error("scrolls to the top for the same key")
			}
		}},
		{"reads one page for a new key, and scrolls to the top once", func(t *testing.T, l *commitList) {
			*l = *landedList()
			gen, _, _ := l.readPage(49)
			l.paged(gen, fakeCommits(50, 50), nil)
			gen, limit := l.read("other")
			if limit != 50 {
				t.Errorf("read %d rows for a new key, want 50", limit)
			}
			if l.scrollTop() || len(l.rows) != 100 {
				t.Errorf("before the new list lands: %d rows, scrolls to the top %v", len(l.rows), l.scrollTop())
			}
			l.listed(gen, gitx.CommitLog{}, fakeCommits(0, 50), 130, nil)
			if !l.scrollTop() {
				t.Fatal("does not scroll to the top")
			}
			l.built(0)
			if l.scrollTop() {
				t.Error("scrolls to the top again after a build")
			}
		}},
		{"a refresh before a list for a new key lands still starts at the top", func(t *testing.T, l *commitList) {
			*l = *landedList()
			gen, _, _ := l.readPage(49)
			l.paged(gen, fakeCommits(50, 50), nil)
			l.read("other")
			gen, limit := l.read("other")
			if limit != 50 {
				t.Errorf("the refresh read %d rows, want 50", limit)
			}
			l.listed(gen, gitx.CommitLog{}, fakeCommits(0, 50), 130, nil)
			if !l.scrollTop() {
				t.Error("does not scroll to the top")
			}
		}},
		{"reset drops the list, its error and the read that runs", func(t *testing.T, l *commitList) {
			*l = *landedList()
			gen, _, _ := l.readPage(45)
			l.paged(gen, nil, errGit)
			gen, _ = l.read("k")
			l.reset()
			l.listed(gen, gitx.CommitLog{}, fakeCommits(0, 50), 130, nil)
			if len(l.rows) != 0 || l.err != "" || l.reading || l.built(0) {
				t.Errorf("after reset: %d rows, error %q, reading %v", len(l.rows), l.err, l.reading)
			}
			gen, limit := l.read("k")
			l.listed(gen, gitx.CommitLog{}, fakeCommits(0, 50), 130, nil)
			if limit != 50 || !l.scrollTop() {
				t.Errorf("first read after reset: limit %d, scrolls to top %v", limit, l.scrollTop())
			}
		}},
		{"opens the newest commit once, after a list with rows", func(t *testing.T, l *commitList) {
			l.newest = true
			gen, _ := l.read("k")
			if sha := l.listed(gen, gitx.CommitLog{}, nil, 0, errGit); sha != "" {
				t.Errorf("failed list opened %q", sha)
			}
			gen, _ = l.read("k")
			if sha := l.listed(gen, gitx.CommitLog{}, fakeCommits(0, 50), 50, nil); sha != "0" {
				t.Errorf("opened %q, want the newest commit 0", sha)
			}
			gen, _ = l.read("k")
			if sha := l.listed(gen, gitx.CommitLog{}, fakeCommits(0, 50), 50, nil); sha != "" {
				t.Errorf("opened %q a second time", sha)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { tc.run(t, &commitList{}) })
	}
}
