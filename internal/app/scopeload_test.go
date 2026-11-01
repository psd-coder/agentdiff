package app

import (
	"slices"
	"testing"
	"time"

	"github.com/psd-coder/agentdiff/internal/continuity"
	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
	"github.com/psd-coder/agentdiff/internal/store"
)

// recordingSink keeps what a scope loader hands over. Like the app, it
// decides on the reviews when a load lands.
type recordingSink struct {
	landed  []landing
	located []string
	read    []contents
}

func (sink *recordingSink) scopeLanded(ld landing) {
	sink.landed = append(sink.landed, ld)
	if ld.perLoad.loadErr == "" {
		ld.cont.Decide(ld.rev, func(*review.Review) bool { return false })
	}
}

func (sink *recordingSink) commitsLocated(movedTo string) {
	sink.located = append(sink.located, movedTo)
}
func (sink *recordingSink) fileRead(file contents) { sink.read = append(sink.read, file) }

func (sink *recordingSink) readPaths() []string {
	var out []string
	for _, c := range sink.read {
		out = append(out, c.change.Path)
	}
	slices.Sort(out)
	return out
}

// loaderTest is a scope loader on the fixture repo. Its goroutine work
// runs at once, and its updates wait in queue until the test runs them.
type loaderTest struct {
	loader *scopeLoader
	sink   *recordingSink
	queue  chan func()
	repo   *gitx.Repo
	store  store.Store
	head   gitx.Scope
	uncomm gitx.Scope
}

func newLoaderTest(t *testing.T) *loaderTest {
	t.Helper()
	repo, err := gitx.Open(fixtureRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	lt := &loaderTest{
		sink: &recordingSink{}, queue: make(chan func(), 64), repo: repo, store: store.Store{Dir: t.TempDir()},
		head: gitx.Scope{Kind: gitx.ScopeCommit, Commit: "HEAD"}, uncomm: gitx.Scope{Kind: gitx.ScopeUncommitted},
	}
	lt.loader = newScopeLoader(continuity.New(lt.store),
		func(fn func()) { fn() }, func(fn func()) { lt.queue <- fn }, lt.sink)
	return lt
}

func (lt *loaderTest) start(scope gitx.Scope, fresh bool, first ...string) {
	lt.loader.start(loadRequest{repo: lt.repo, scopeLoad: scopeLoad{scope, fresh}, rev: review.New(lt.repo.Root, scope), first: first})
}

// run runs the n updates that wait first.
func (lt *loaderTest) run(count int) {
	for range count {
		(<-lt.queue)()
	}
}

func (lt *loaderTest) drain() { lt.run(len(lt.queue)) }

func TestLoaderDropsTheResultsOfAnOlderLoad(t *testing.T) {
	lt := newLoaderTest(t)
	lt.start(lt.uncomm, true)
	lt.start(lt.head, true)
	lt.drain()
	if len(lt.sink.landed) != 1 || lt.sink.landed[0].scope != lt.head {
		t.Fatalf("landed %d loads, want only the commit", len(lt.sink.landed))
	}
	if got := lt.sink.readPaths(); !slices.Equal(got, []string{"README.md", "cache/cache.go"}) {
		t.Errorf("read %q, want the files of the commit once", got)
	}
}

func TestLoaderRunsUntilTheLatestLoadLands(t *testing.T) {
	lt := newLoaderTest(t)
	lt.start(lt.uncomm, true)
	older := len(lt.queue)
	lt.start(lt.head, true)
	lt.run(older)
	if !lt.loader.firstLoad() {
		t.Fatal("the results of the older load ended the latest one")
	}
	lt.drain()
	if !lt.loader.idle() {
		t.Error("the latest load landed and still runs")
	}
}

func TestLoaderResetDropsTheLoad(t *testing.T) {
	lt := newLoaderTest(t)
	lt.start(lt.uncomm, true)
	lt.drain()
	lt.start(lt.head, false)
	if !lt.loader.switching() {
		t.Fatal("a load after a landed one does not switch")
	}
	lt.sink.read = nil
	lt.loader.reset()
	lt.drain()
	if !lt.loader.idle() || len(lt.sink.landed) != 1 || len(lt.sink.read) != 0 {
		t.Fatalf("after reset: idle %v, landed %d, read %d", lt.loader.idle(), len(lt.sink.landed), len(lt.sink.read))
	}
	lt.start(lt.head, false)
	if !lt.loader.firstLoad() {
		t.Error("the load after a reset keeps the changes of the project before")
	}
}

func TestLoaderReloadsThePendingScope(t *testing.T) {
	lt := newLoaderTest(t)
	lt.start(lt.head, true)
	if got := lt.loader.again(lt.uncomm); got != (scopeLoad{lt.head, true}) {
		t.Errorf("reload during a load reads %+v, want the commit anew", got)
	}
	lt.drain()
	if got := lt.loader.again(lt.uncomm); got != (scopeLoad{lt.uncomm, false}) {
		t.Errorf("reload after the load reads %+v, want the current scope", got)
	}
}

func TestLoaderHandsOverAFailedLoad(t *testing.T) {
	lt := newLoaderTest(t)
	lt.start(gitx.Scope{Kind: gitx.ScopeCommit, Commit: "nope"}, true, "README.md")
	lt.drain()
	if len(lt.sink.landed) != 1 {
		t.Fatalf("landed %d loads", len(lt.sink.landed))
	}
	ld := lt.sink.landed[0]
	if loaded := ld.perLoad; loaded.loadErr == "" || loaded.snap != nil || len(loaded.files) != 0 || loaded.diffs == nil || loaded.extra == nil {
		t.Errorf("perLoad %+v, want only the error", loaded)
	}
	if len(ld.first) != 0 || len(lt.sink.read) != 0 || len(lt.sink.located) != 0 {
		t.Errorf("a failed load read files: first %d, read %d, located %d", len(ld.first), len(lt.sink.read), len(lt.sink.located))
	}
	if !lt.loader.idle() || lt.loader.shown {
		t.Errorf("idle %v, shown %v", lt.loader.idle(), lt.loader.shown)
	}
}

func TestLoaderReadsTheFirstPageBeforeItLands(t *testing.T) {
	lt := newLoaderTest(t)
	lt.start(lt.uncomm, true, "api/handler.go", "README.md")
	lt.run(1)
	if len(lt.sink.landed) != 1 || len(lt.sink.read) != 0 {
		t.Fatalf("landed %d, read %d before the first update", len(lt.sink.landed), len(lt.sink.read))
	}
	first := lt.sink.landed[0].first
	if file := first["api/handler.go"]; file.change.Path != "api/handler.go" || file.newText == "" || file.err != nil {
		t.Errorf("changed file of the first page: %+v", file)
	}
	if file := first["README.md"]; file.newText != "# Demo\n" || file.err != nil {
		t.Errorf("unchanged file of the first page: %+v", file)
	}
	lt.drain()
	if got := lt.sink.readPaths(); !slices.Equal(got, []string{"cache/cache.go"}) {
		t.Errorf("read %q after the landing, want only the file not on the first page", got)
	}
}

func TestLoaderDropsAFileReadOfAnOlderLoad(t *testing.T) {
	lt := newLoaderTest(t)
	lt.start(lt.uncomm, true)
	lt.drain()
	snap := lt.sink.landed[0].perLoad.snap
	lt.sink.read = nil
	lt.loader.readFile(snap, "README.md")
	lt.drain()
	if got := lt.sink.readPaths(); !slices.Equal(got, []string{"README.md"}) || lt.sink.read[0].newText != "# Demo\n" {
		t.Fatalf("read %q", got)
	}

	lt.sink.read = nil
	lt.loader.readFile(snap, "README.md")
	lt.start(lt.uncomm, false, "api/handler.go", "cache/cache.go")
	lt.drain()
	if got := lt.sink.readPaths(); len(got) != 0 {
		t.Errorf("read %q after a new load", got)
	}
}

func TestReadScopeReadsACommit(t *testing.T) {
	repo, err := gitx.Open(fixtureRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	read, err := readScope(t.Context(), repo, gitx.Scope{Kind: gitx.ScopeCommit, Commit: "HEAD"}, []string{"cache/cache.go", "api/handler.go"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := read.cr.Close(); err != nil {
			t.Error(err)
		}
	}()
	loaded := read.perLoad
	if got := fileNamesOf(loaded.files); !slices.Equal(got, []string{"README.md", "cache/cache.go"}) {
		t.Errorf("files %q", got)
	}
	if diff := loaded.diffs["README.md"]; diff == nil || !diff.loading {
		t.Errorf("diff of README.md %+v, want one that loads", diff)
	}
	if loaded.commit == nil || loaded.commit.Subject != "Initial commit" || loaded.diffstat == "" || !loaded.patchKnown || loaded.fp.Patch == "" || read.fpErr != nil {
		t.Errorf("commit %+v, diffstat %q, patch known %v, fingerprint %+v", loaded.commit, loaded.diffstat, loaded.patchKnown, loaded.fp)
	}
	if file := read.first["cache/cache.go"]; file.oldText != "" || file.newText == "" || file.err != nil {
		t.Errorf("first-page file in the commit: %+v", file)
	}
	if file := read.first["api/handler.go"]; file.err == nil {
		t.Errorf("first-page file the commit does not have: %+v", file)
	}
}

func fileNamesOf(files []gitx.FileChange) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Path)
	}
	slices.Sort(out)
	return out
}

// TestLoaderOnGoroutines runs loads on goroutines, so that the race
// detector sees the load goroutine and the updates of the UI thread. An
// earlier review makes the load look for its commit.
func TestLoaderOnGoroutines(t *testing.T) {
	lt := newLoaderTest(t)
	earlier := review.New(lt.repo.Root, gitx.ArchivedScope("pz"))
	earlier.Overview = &review.Overview{Markdown: "earlier", PatchID: "pz", Files: map[string]string{"README.md": "x"}, Generated: time.Unix(1, 0)}
	if err := lt.store.SaveReview(earlier); err != nil {
		t.Fatal(err)
	}
	lt.loader.async = func(fn func()) { go fn() }
	lt.start(lt.uncomm, true, "README.md")
	lt.start(lt.uncomm, true, "README.md")
	deadline := time.After(5 * time.Second)
	for len(lt.sink.landed) == 0 || len(lt.sink.located) == 0 || len(lt.sink.read) < 2 {
		select {
		case fn := <-lt.queue:
			fn()
		case <-deadline:
			t.Fatalf("landed %d, located %d, read %d", len(lt.sink.landed), len(lt.sink.located), len(lt.sink.read))
		}
	}
	if len(lt.sink.landed) != 1 || !lt.loader.idle() {
		t.Errorf("landed %d, idle %v", len(lt.sink.landed), lt.loader.idle())
	}
}
