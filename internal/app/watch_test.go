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

func (fakeWatch *fakeWatch) Next(ctx context.Context) ([]mygo.FileEvent, error) {
	if fakeWatch.asked {
		select {
		case fakeWatch.handled <- struct{}{}:
		case <-fakeWatch.ctx.Done():
			return nil, io.EOF
		}
	}
	fakeWatch.asked = true
	select {
	case events := <-fakeWatch.events:
		return events, nil
	case <-fakeWatch.ctx.Done():
		return nil, io.EOF
	}
}

func (fakeWatch *fakeWatch) Close() error { return nil }

// emit reports changes under paths and returns once the app has handled
// them.
func (fakeWatch *fakeWatch) emit(paths ...string) {
	events := make([]mygo.FileEvent, len(paths))
	for i, p := range paths {
		events[i] = mygo.FileEvent{Op: mygo.FileHint, Path: p, IsDir: true}
	}
	fakeWatch.events <- events
	<-fakeWatch.handled
}

// newWatchedApp opens dir with fake watches, the working tree's first,
// then the git directory's.
func newWatchedApp(t *testing.T, dir string) (*app, *[]*fakeWatch) {
	t.Helper()
	watches := &[]*fakeWatch{}
	app := newApp(store.Store{Dir: t.TempDir()})
	app.queue = make(chan func(), 64)
	app.watchFiles = func(ctx context.Context, path string, _ mygo.FileWatchOptions) (watcher, error) {
		watch := &fakeWatch{ctx: ctx, dir: path, events: make(chan []mygo.FileEvent), handled: make(chan struct{})}
		*watches = append(*watches, watch)
		return watch, nil
	}
	app.openRepo(dir)
	if app.repo == nil {
		t.Fatalf("open: %s", app.repoErr)
	}
	t.Cleanup(app.unwatch)
	drain(app)
	return app, watches
}

// drain runs the app's queued updates.
func drain(app *app) {
	for {
		select {
		case fn := <-app.queue:
			fn()
		default:
			return
		}
	}
}

func TestWatchShowsNewCommits(t *testing.T) {
	app, watches := newWatchedApp(t, fixtureRepo(t))
	dir := app.repo.Root
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "Commit the work")
	(*watches)[1].emit("refs/heads")
	drain(app)
	if len(app.commits.rows) != 2 || app.commits.rows[0].Subject != "Commit the work" {
		t.Fatalf("commits = %+v", app.commits.rows)
	}
}

func TestWatchWaitsForIndexLock(t *testing.T) {
	app, watches := newWatchedApp(t, fixtureRepo(t))
	lock := filepath.Join(app.repo.Root, ".git", "index.lock")
	write(t, filepath.Dir(lock), "index.lock", "")
	gen := app.loader.gen
	(*watches)[1].emit(".")
	if len(app.queue) != 0 {
		t.Fatal("refreshed while git held the index lock")
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	(*watches)[1].emit(".")
	drain(app)
	if app.loader.gen == gen {
		t.Error("did not refresh once the lock went")
	}
}

func TestWatchReloadsUncommittedChanges(t *testing.T) {
	dir := fixtureRepo(t)
	write(t, dir, ".gitignore", "dist/\n")
	write(t, dir, "dist/app.js", "x\n")
	app, watches := newWatchedApp(t, dir)
	tree := (*watches)[0]

	gen := app.loader.gen
	tree.emit("dist")
	if len(app.queue) != 0 {
		t.Fatal("reloaded for a change in an ignored directory")
	}
	write(t, dir, "api/routes.go", "package api\n")
	tree.emit("dist", "api")
	drain(app)
	if app.loader.gen == gen || !hasFile(app.files, "api/routes.go") {
		t.Fatalf("files after a change in api: %+v", app.files)
	}

	app.setScope(gitx.Scope{Kind: gitx.ScopeCommit, Commit: "HEAD"})
	drain(app)
	gen = app.loader.gen
	tree.emit("api")
	drain(app)
	if app.loader.gen != gen {
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
	app, watches := newWatchedApp(t, fixtureRepo(t))
	first := *watches
	if len(first) != 2 || first[0].dir != app.repo.Root || first[1].dir != filepath.Join(app.repo.Root, ".git") {
		t.Fatalf("watches = %+v", first)
	}

	app.openRepo(fixtureRepo(t))
	drain(app)
	if first[0].ctx.Err() == nil || first[1].ctx.Err() == nil {
		t.Error("the watches of the project open before still run")
	}
	if len(*watches) != 4 || (*watches)[2].dir != app.repo.Root {
		t.Fatalf("watches after the switch = %+v", *watches)
	}

	app.closeRepo()
	if (*watches)[2].ctx.Err() == nil || (*watches)[3].ctx.Err() == nil {
		t.Error("the watches still run after the project closed")
	}
}
