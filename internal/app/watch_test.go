package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/store"
)

// fakeWatch is a watch whose changes the test sends with emit.
type fakeWatch struct {
	ctx    context.Context
	dir    string
	events chan []mygo.FileEvent
	// handled takes a signal each time the app asks for the next changes
	// after it handled a batch.
	handled chan struct{}
	asked   bool
}

func (w *fakeWatch) Next(ctx context.Context) ([]mygo.FileEvent, error) {
	if w.asked {
		select {
		case w.handled <- struct{}{}:
		case <-w.ctx.Done():
			return nil, io.EOF
		}
	}
	w.asked = true
	select {
	case events := <-w.events:
		return events, nil
	case <-w.ctx.Done():
		return nil, io.EOF
	}
}

func (w *fakeWatch) Close() error { return nil }

// emit reports changes under paths and returns once the app has handled
// them.
func (w *fakeWatch) emit(paths ...string) {
	events := make([]mygo.FileEvent, len(paths))
	for i, p := range paths {
		events[i] = mygo.FileEvent{Op: mygo.FileHint, Path: p, IsDir: true}
	}
	w.events <- events
	<-w.handled
}

// newWatchedApp opens dir with fake watches, the working tree's first,
// then the git directory's.
func newWatchedApp(t *testing.T, dir string) (*app, *[]*fakeWatch) {
	t.Helper()
	watches := &[]*fakeWatch{}
	a := newApp(store.Store{Dir: t.TempDir()})
	a.queue = make(chan func(), 64)
	a.watchFiles = func(ctx context.Context, path string, _ mygo.FileWatchOptions) (watcher, error) {
		w := &fakeWatch{ctx: ctx, dir: path, events: make(chan []mygo.FileEvent), handled: make(chan struct{})}
		*watches = append(*watches, w)
		return w, nil
	}
	a.openRepo(dir)
	if a.repo == nil {
		t.Fatalf("open: %s", a.repoErr)
	}
	t.Cleanup(a.unwatch)
	drain(a)
	return a, watches
}

// drain runs the app's queued updates.
func drain(a *app) {
	for {
		select {
		case fn := <-a.queue:
			fn()
		default:
			return
		}
	}
}

func TestWatchShowsNewCommits(t *testing.T) {
	a, watches := newWatchedApp(t, fixtureRepo(t))
	dir := a.repo.Root
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "Commit the work")
	(*watches)[1].emit("refs/heads")
	drain(a)
	if len(a.commits.rows) != 2 || a.commits.rows[0].Subject != "Commit the work" {
		t.Fatalf("commits = %+v", a.commits.rows)
	}
}

func TestWatchWaitsForIndexLock(t *testing.T) {
	a, watches := newWatchedApp(t, fixtureRepo(t))
	lock := filepath.Join(a.repo.Root, ".git", "index.lock")
	write(t, filepath.Dir(lock), "index.lock", "")
	gen := a.gen
	(*watches)[1].emit(".")
	if len(a.queue) != 0 {
		t.Fatal("refreshed while git held the index lock")
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	(*watches)[1].emit(".")
	drain(a)
	if a.gen == gen {
		t.Error("did not refresh once the lock went")
	}
}

func TestWatchReloadsUncommittedChanges(t *testing.T) {
	dir := fixtureRepo(t)
	write(t, dir, ".gitignore", "dist/\n")
	write(t, dir, "dist/app.js", "x\n")
	a, watches := newWatchedApp(t, dir)
	tree := (*watches)[0]

	gen := a.gen
	tree.emit("dist")
	if len(a.queue) != 0 {
		t.Fatal("reloaded for a change in an ignored directory")
	}
	write(t, dir, "api/routes.go", "package api\n")
	tree.emit("dist", "api")
	drain(a)
	if a.gen == gen || !hasFile(a.files, "api/routes.go") {
		t.Fatalf("files after a change in api: %+v", a.files)
	}

	a.setScope(gitx.Scope{Kind: gitx.ScopeCommit, Commit: "HEAD"})
	drain(a)
	gen = a.gen
	tree.emit("api")
	drain(a)
	if a.gen != gen {
		t.Error("reloaded a commit for a change in the working tree")
	}
}

func hasFile(files []gitx.FileChange, path string) bool {
	for _, f := range files {
		if f.Path == path {
			return true
		}
	}
	return false
}

func TestWatchFollowsOpenProject(t *testing.T) {
	a, watches := newWatchedApp(t, fixtureRepo(t))
	first := *watches
	if len(first) != 2 || first[0].dir != a.repo.Root || first[1].dir != filepath.Join(a.repo.Root, ".git") {
		t.Fatalf("watches = %+v", first)
	}

	a.openRepo(fixtureRepo(t))
	drain(a)
	if first[0].ctx.Err() == nil || first[1].ctx.Err() == nil {
		t.Error("the watches of the project open before still run")
	}
	if len(*watches) != 4 || (*watches)[2].dir != a.repo.Root {
		t.Fatalf("watches after the switch = %+v", *watches)
	}

	a.closeRepo()
	if (*watches)[2].ctx.Err() == nil || (*watches)[3].ctx.Err() == nil {
		t.Error("the watches still run after the project closed")
	}
}
