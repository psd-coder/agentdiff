package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/psd-coder/agentdiff/internal/gitx"
)

// pagedApp opens a fixture repository on the branch work, which has n
// empty commits on top of main, from "Commit number 0" to the newest,
// "Commit number n-1".
func pagedApp(t *testing.T, commitCount int) (*app, *ui.Tester) {
	t.Helper()
	app := newTestApp(t)
	dir := app.repo.Root
	addWork(t, dir, commitCount)
	app.openRepo(dir)
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	return app, tt
}

// pagedRepo is a fixture repository checked out on the branch work, which
// has n empty commits on top of main, as pagedApp has.
func pagedRepo(t *testing.T, commitCount int) string {
	t.Helper()
	dir := fixtureRepo(t)
	addWork(t, dir, commitCount)
	return dir
}

// addWork adds the branch work with n empty commits on top of main and
// checks it out.
func addWork(t *testing.T, dir string, commitCount int) {
	t.Helper()
	var script strings.Builder
	for i := range commitCount {
		fmt.Fprintf(&script, "commit refs/heads/work\ncommitter T <t@e> %d +0000\ndata <<END\nCommit number %d\nEND\n", 1700000000+i, i)
		if i == 0 {
			script.WriteString("from refs/heads/main^0\n")
		}
		script.WriteString("\n")
	}
	cmd := exec.Command("git", "fast-import", "--quiet")
	cmd.Dir, cmd.Stdin = dir, strings.NewReader(script.String())
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
func scrollCommits(t *testing.T, app *app, tt *ui.Tester, done func() bool) {
	t.Helper()
	bounds, ok := tt.Find("Changes")
	if !ok {
		t.Fatalf("no commit list: %q", tt.Texts())
	}
	for range 200 {
		if done() {
			return
		}
		tt.Scroll(bounds.X+bounds.W/2, bounds.Y+bounds.H/2, 0, 200)
		tt.Frame()
	}
	t.Fatalf("%d of %d commits after scrolling, error %q", len(app.commits.rows), app.commits.total, app.commits.err)
}

// TestCommitListPages lists all commits of a branch: the first page at
// once and the next ones as the list scrolls to its end, while it builds
// only the rows in view.
func TestCommitListPages(t *testing.T) {
	app, tt := pagedApp(t, 130)
	if len(app.commits.rows) != 50 || !tt.HasText("130") {
		t.Fatalf("first page: %d commits, texts %q", len(app.commits.rows), tt.Texts())
	}
	if rowCount := commitRows(tt); rowCount == 0 || rowCount > 20 {
		t.Errorf("built %d commit rows of 50", rowCount)
	}
	scrollCommits(t, app, tt, func() bool { return len(app.commits.rows) == 130 })
	for i, cm := range app.commits.rows {
		if want := fmt.Sprint("Commit number ", 129-i); cm.Subject != want {
			t.Fatalf("commit %d is %q, want %q", i, cm.Subject, want)
		}
	}
	scrollCommits(t, app, tt, func() bool { return tt.HasText("Commit number 0") })
	if rowCount := commitRows(tt); rowCount > 20 {
		t.Errorf("built %d commit rows of 130", rowCount)
	}
}

// TestCommitListPagesWithKeys reads the next page when End moves the
// choice to the last commit read, and opens that commit.
func TestCommitListPagesWithKeys(t *testing.T) {
	app, tt := pagedApp(t, 70)
	app.queueCommand("focus-changes")
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
	if len(app.commits.rows) != 70 || app.chosen.Commit != app.commits.rows[69].SHA {
		t.Errorf("%d commits, chosen %+v", len(app.commits.rows), app.chosen)
	}
	tt.Key(0, ui.KeyHome)
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if scope, ok := app.cmpr.allScope(); !ok || app.chosen != scope {
		t.Errorf("Home then Down chose %+v", app.chosen)
	}
}

// TestNewListStartsAtTop opens another project while the commit list
// was scrolled to its end: the list starts at the top.
func TestNewListStartsAtTop(t *testing.T) {
	app, tt := pagedApp(t, 130)
	app.readCommitPage(49)
	app.changeList.ScrollToEnd()
	tt.Frame()
	if tt.HasText("Commit number 129") {
		t.Fatalf("the list is not at its end: %q", tt.Texts())
	}
	app.openRepo(pagedRepo(t, 130))
	for range 3 {
		tt.Frame()
	}
	if len(app.commits.rows) != 50 || !tt.HasText("Commit number 129") {
		t.Errorf("other project: %d commits, texts %q", len(app.commits.rows), tt.Texts())
	}
}

// TestCommitPageError shows why a page of commits could not be read, and
// reads it again with Try again.
func TestCommitPageError(t *testing.T) {
	app, tt := pagedApp(t, 130)
	gitDir, away := filepath.Join(app.repo.Root, ".git"), filepath.Join(t.TempDir(), "git")
	if err := os.Rename(gitDir, away); err != nil {
		t.Fatal(err)
	}
	app.readCommitPage(49)
	app.changeList.ScrollToEnd()
	tt.Frame()
	tt.Frame()
	if len(app.commits.rows) != 50 || !tt.HasText("Could not list more commits") || !tt.HasText("Try again") {
		t.Fatalf("%d commits, texts %q", len(app.commits.rows), tt.Texts())
	}
	snapshot(t, tt, "commit-page-error")
	if err := os.Rename(away, gitDir); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Try again"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(app.commits.rows) != 100 || app.commits.err != "" || tt.HasText("Try again") {
		t.Errorf("after Try again: %d commits, error %q", len(app.commits.rows), app.commits.err)
	}
}

// TestPickBranchOpensNewestAfterRefresh picks a branch without a base and
// refreshes before its commit list lands: the newest commit of the branch
// still opens.
func TestPickBranchOpensNewestAfterRefresh(t *testing.T) {
	app, _ := pagedApp(t, 3)
	tip := revSHA(t, app.repo.Root, "main")
	app.queue = make(chan func(), 64)
	app.pickBranch("main")
	app.refresh()
	pump(t, app, "the newest commit of main", func() bool { return app.chosen.Kind == gitx.ScopeCommit })
	if app.chosen.Commit != tip {
		t.Errorf("opened %s, want the tip of main %s", app.chosen.Commit, tip)
	}
}
