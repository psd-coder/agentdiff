package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// pagedApp opens a fixture repository on the branch work, which has n
// empty commits on top of main, from "Commit number 0" to the newest,
// "Commit number n-1".
func pagedApp(t *testing.T, n int) (*app, *ui.Tester) {
	t.Helper()
	a := newTestApp(t)
	dir := a.repo.Root
	addWork(t, dir, n)
	a.openRepo(dir)
	tt := ui.NewTester(a.view, 1300, 800)
	tt.Frame()
	return a, tt
}

// pagedRepo is a fixture repository checked out on the branch work, which
// has n empty commits on top of main, as pagedApp has.
func pagedRepo(t *testing.T, n int) string {
	t.Helper()
	dir := fixtureRepo(t)
	addWork(t, dir, n)
	return dir
}

// addWork adds the branch work with n empty commits on top of main and
// checks it out.
func addWork(t *testing.T, dir string, n int) {
	t.Helper()
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "commit refs/heads/work\ncommitter T <t@e> %d +0000\ndata <<END\nCommit number %d\nEND\n", 1700000000+i, i)
		if i == 0 {
			b.WriteString("from refs/heads/main^0\n")
		}
		b.WriteString("\n")
	}
	cmd := exec.Command("git", "fast-import", "--quiet")
	cmd.Dir, cmd.Stdin = dir, strings.NewReader(b.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git fast-import: %v\n%s", err, out)
	}
	git(t, dir, "checkout", "-q", "work")
}

// commitRows returns how many commits the list built, as rows.
func commitRows(tt *ui.Tester) int {
	seen := map[string]bool{}
	for _, s := range tt.Texts() {
		if strings.HasPrefix(s, "Commit number ") {
			seen[s] = true
		}
	}
	return len(seen)
}

// scrollCommits scrolls the commit list down until done reports true,
// and fails the test when it never does.
func scrollCommits(t *testing.T, a *app, tt *ui.Tester, done func() bool) {
	t.Helper()
	r, ok := tt.Find("Changes")
	if !ok {
		t.Fatalf("no commit list: %q", tt.Texts())
	}
	for range 200 {
		if done() {
			return
		}
		tt.Scroll(r.X+r.W/2, r.Y+r.H/2, 0, 200)
		tt.Frame()
	}
	t.Fatalf("%d of %d commits after scrolling, error %q", len(a.commits), a.commitsTotal, a.commitsErr)
}

// TestCommitListPages lists all commits of a branch: the first page at
// once and the next ones as the list scrolls to its end, while it builds
// only the rows in view.
func TestCommitListPages(t *testing.T) {
	a, tt := pagedApp(t, 130)
	if len(a.commits) != 50 || !tt.HasText("130") {
		t.Fatalf("first page: %d commits, texts %q", len(a.commits), tt.Texts())
	}
	if n := commitRows(tt); n == 0 || n > 20 {
		t.Errorf("built %d commit rows of 50", n)
	}
	scrollCommits(t, a, tt, func() bool { return len(a.commits) == 130 })
	for i, cm := range a.commits {
		if want := fmt.Sprint("Commit number ", 129-i); cm.Subject != want {
			t.Fatalf("commit %d is %q, want %q", i, cm.Subject, want)
		}
	}
	scrollCommits(t, a, tt, func() bool { return tt.HasText("Commit number 0") })
	if n := commitRows(tt); n > 20 {
		t.Errorf("built %d commit rows of 130", n)
	}
}

// TestCommitListPagesWithKeys reads the next page when End moves the
// choice to the last commit read, and opens that commit.
func TestCommitListPagesWithKeys(t *testing.T) {
	a, tt := pagedApp(t, 70)
	a.queueCommand("focus-changes")
	tt.Frame()
	tt.Frame()
	if !tt.Focused("Changes") {
		t.Fatal("the list of changes has no focus")
	}
	for range 5 {
		tt.Key(0, ui.KeyEnd)
		tt.Frame()
		tt.Frame()
	}
	if len(a.commits) != 70 || a.chosen.Commit != a.commits[69].SHA {
		t.Errorf("%d commits, chosen %+v", len(a.commits), a.chosen)
	}
	tt.Key(0, ui.KeyHome)
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if s, ok := a.allScope(); !ok || a.chosen != s {
		t.Errorf("Home then Down chose %+v", a.chosen)
	}
}

// TestRefreshKeepsLoadedCommits reads as many commits again as the list
// has, and starts again from the first page for another range.
func TestRefreshKeepsLoadedCommits(t *testing.T) {
	a, tt := pagedApp(t, 130)
	scrollCommits(t, a, tt, func() bool { return len(a.commits) == 100 })
	git(t, a.repo.Root, "commit", "-q", "--allow-empty", "-m", "Commit the work")
	a.refresh()
	if len(a.commits) != 100 || a.commitsTotal != 131 || a.commits[0].Subject != "Commit the work" {
		t.Errorf("after refresh: %d of %d commits, newest %q", len(a.commits), a.commitsTotal, a.commits[0].Subject)
	}

	// The window shows the old commits while git reads the range.
	a.queue = make(chan func(), 64)
	a.setRange("main", "work~1")
	tt.Frame()
	pump(t, a, "the commits of the range", func() bool { return !a.commitsReading })
	tt.Frame()
	tt.Frame()
	pump(t, a, "the updates", func() bool { return len(a.queue) == 0 })
	if len(a.commits) != 50 || a.commitsTotal != 130 {
		t.Errorf("another range: %d of %d commits", len(a.commits), a.commitsTotal)
	}
}

// TestReopenReadsNoOldPage opens a project again while its commit list
// was scrolled: no page of the old list is read before the new list.
func TestReopenReadsNoOldPage(t *testing.T) {
	a, tt := pagedApp(t, 130)
	scrollCommits(t, a, tt, func() bool { return len(a.commits) == 100 })
	a.queue = make(chan func(), 64)
	a.openRepo(a.repo.Root)
	tt.Frame()
	if a.commitsReading {
		t.Error("a page of the old commit list is read")
	}
	pump(t, a, "the commits", func() bool { return len(a.commits) == 50 })
}

// TestReopenStartsAtTop opens a project again while its commit list was
// scrolled: the list starts at the top and reads only its first page.
func TestReopenStartsAtTop(t *testing.T) {
	a, tt := pagedApp(t, 130)
	scrollCommits(t, a, tt, func() bool { return len(a.commits) == 100 })
	a.openRepo(a.repo.Root)
	for range 3 {
		tt.Frame()
	}
	if len(a.commits) != 50 || !tt.HasText("Commit number 129") {
		t.Errorf("reopened project: %d commits, texts %q", len(a.commits), tt.Texts())
	}
}

// TestOtherRepoStartsFresh opens another project with the same branch
// names while the commit list was scrolled: its list starts at the top
// and reads only its first page.
func TestOtherRepoStartsFresh(t *testing.T) {
	a, tt := pagedApp(t, 130)
	scrollCommits(t, a, tt, func() bool { return len(a.commits) == 100 })
	a.openRepo(pagedRepo(t, 130))
	for range 3 {
		tt.Frame()
	}
	if len(a.commits) != 50 || !tt.HasText("Commit number 129") {
		t.Errorf("other project: %d commits, texts %q", len(a.commits), tt.Texts())
	}
}

// TestOtherRepoDropsPageError opens another project after a page of
// commits failed: the error does not show while git reads the new list.
func TestOtherRepoDropsPageError(t *testing.T) {
	a, tt := pagedApp(t, 130)
	gitDir := filepath.Join(a.repo.Root, ".git")
	if err := os.Rename(gitDir, filepath.Join(t.TempDir(), "git")); err != nil {
		t.Fatal(err)
	}
	scrollCommits(t, a, tt, func() bool { return tt.HasText("Could not list more commits") })
	a.queue = make(chan func(), 64)
	a.openRepo(pagedRepo(t, 130))
	tt.Frame()
	if a.commitsErr != "" || tt.HasText("Could not list") {
		t.Errorf("error %q of the old project shows", a.commitsErr)
	}
	pump(t, a, "the commits", func() bool { return len(a.commits) == 50 && !a.commitsReading })
	tt.Frame()
	if a.commitsErr != "" || tt.HasText("Could not list") {
		t.Errorf("error %q after the new list", a.commitsErr)
	}
}

// TestCommitPageError shows why a page of commits could not be read, and
// reads it again once the list scrolls, or at once with Try again.
func TestCommitPageError(t *testing.T) {
	a, tt := pagedApp(t, 130)
	dir := a.repo.Root
	gitDir, away := filepath.Join(dir, ".git"), filepath.Join(t.TempDir(), "git")
	if err := os.Rename(gitDir, away); err != nil {
		t.Fatal(err)
	}
	scrollCommits(t, a, tt, func() bool { return tt.HasText("Could not list more commits") })
	if len(a.commits) != 50 {
		t.Errorf("%d commits, texts %q", len(a.commits), tt.Texts())
	}
	if err := os.Rename(away, gitDir); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if len(a.commits) != 50 {
		t.Errorf("read again without a scroll: %d commits", len(a.commits))
	}
	// The list is at its end, so it scrolls up.
	r, _ := tt.Find("Changes")
	tt.Scroll(r.X+r.W/2, r.Y+r.H/2, 0, -100)
	tt.Frame()
	tt.Frame()
	if len(a.commits) != 100 {
		t.Fatalf("after a scroll: %d commits, error %q", len(a.commits), a.commitsErr)
	}
	if a.commitsErr != "" || tt.HasText("Could not list more commits") {
		t.Errorf("error %q stays", a.commitsErr)
	}

	// Try again reads the page at once, without a scroll.
	if err := os.Rename(gitDir, away); err != nil {
		t.Fatal(err)
	}
	scrollCommits(t, a, tt, func() bool { return tt.HasText("Try again") })
	for range 5 {
		tt.Scroll(r.X+r.W/2, r.Y+r.H/2, 0, 200)
		tt.Frame()
	}
	snapshot(t, tt, "commit-page-error")
	if err := os.Rename(away, gitDir); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.commits) != 100 {
		t.Fatalf("read again before Try again: %d commits", len(a.commits))
	}
	if err := tt.Click("Try again"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.commits) != 130 || a.commitsErr != "" || tt.HasText("Try again") {
		t.Errorf("after Try again: %d commits, error %q", len(a.commits), a.commitsErr)
	}
}
