package app

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/psd-coder/agentdiff/internal/gitx"
)

// watcher is a watch mygo.WatchFiles started.
type watcher interface {
	Next(ctx context.Context) ([]mygo.FileEvent, error)
	Close() error
}

type watchFunc func(ctx context.Context, path string, opts mygo.FileWatchOptions) (watcher, error)

func watchFiles(ctx context.Context, path string, opts mygo.FileWatchOptions) (watcher, error) {
	w, err := mygo.WatchFiles(ctx, path, opts)
	if err != nil {
		return nil, err
	}
	return w, nil
}

// The watches wait for a burst of changes, such as an agent writing
// several files or a commit, to end before the app reads them again.
const (
	watchDebounce = 300 * time.Millisecond
	watchMaxDelay = 2 * time.Second
)

// gitDirSkipped are the directories of the git directory whose changes
// leave the commits, branches and index as they are.
var gitDirSkipped = map[string]bool{"objects": true, "lfs": true, "logs": true, "fsmonitor--daemon": true}

// watch follows the working tree and the git directory of r, so the
// window shows new commits, branches and changes without a refresh. It
// stops the watch of the project open before.
func (a *app) watch(r *gitx.Repo) {
	a.unwatch()
	if a.watchFiles == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.stopWatch = cancel
	a.async(func() {
		gitDir, err := r.GitDir(ctx)
		if err != nil {
			log.Printf("watch: %v", err)
			return
		}
		common, err := r.CommonDir(ctx)
		if err != nil {
			log.Printf("watch: %v", err)
			return
		}
		a.follow(ctx, r.Root, mygo.FileWatchOptions{
			Ignore: func(p string, isDir bool) bool { return p == ".git" },
		}, func(events []mygo.FileEvent) { a.worktreeChanged(ctx, r, events) })
		a.follow(ctx, common, mygo.FileWatchOptions{
			Ignore: func(p string, isDir bool) bool { return gitDirSkipped[p] },
		}, func([]mygo.FileEvent) { a.gitChanged(r, gitDir) })
	})
}

func (a *app) unwatch() {
	if a.stopWatch != nil {
		a.stopWatch()
		a.stopWatch = nil
	}
}

// follow watches dir and calls changed with each batch of changes, on a
// goroutine of its own, until ctx ends.
func (a *app) follow(ctx context.Context, dir string, opts mygo.FileWatchOptions, changed func([]mygo.FileEvent)) {
	opts.Recursive, opts.HintsOnly = true, true
	opts.Debounce, opts.MaxDelay = watchDebounce, watchMaxDelay
	w, err := a.watchFiles(ctx, dir, opts)
	if err != nil {
		log.Printf("watch %s: %v", dir, err)
		return
	}
	go func() {
		defer w.Close()
		for {
			events, err := w.Next(ctx)
			if err != nil {
				if !errors.Is(err, io.EOF) && ctx.Err() == nil {
					log.Printf("watch %s: %v", dir, err)
				}
				return
			}
			changed(events)
		}
	}()
}

// gitChanged reads the commits, branches and changes again. While git
// holds the index lock, as during a commit or each step of a rebase, it
// waits for the lock to go, which the watch reports too.
func (a *app) gitChanged(r *gitx.Repo, gitDir string) {
	if _, err := os.Stat(filepath.Join(gitDir, "index.lock")); err == nil {
		return
	}
	a.update(func() {
		if a.repo == r {
			a.refresh()
		}
	})
}

// worktreeChanged reads the uncommitted changes again when they are under
// review and a changed directory is one git does not ignore, so a build
// or a package install does not reload the window.
func (a *app) worktreeChanged(ctx context.Context, r *gitx.Repo, events []mygo.FileEvent) {
	dirs := make([]string, len(events))
	for i, e := range events {
		dirs[i] = e.Path
	}
	ignored, err := r.Ignored(ctx, dirs)
	if err != nil {
		log.Printf("watch: %v", err)
	}
	if !slices.ContainsFunc(dirs, func(d string) bool { return !ignored[d] }) {
		return
	}
	a.update(func() {
		if a.repo == r && a.scope.Kind == gitx.ScopeUncommitted {
			a.reload()
		}
	})
}
