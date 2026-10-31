package app

import (
	"context"
	"log"
	"slices"

	"github.com/psd-coder/agentdiff/internal/continuity"
	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
)

// scopeLoader reads the changes of a scope on a goroutine and hands them
// to its sink. Only the latest load hands anything over: a new load or a
// reset drops the results of the loads before it. All its methods run on
// the UI thread.
type scopeLoader struct {
	reviews *continuity.Reviews
	async   func(func())
	update  func(func())
	sink    loadSink

	gen     int
	phase   loadPhase
	pending scopeLoad
	cancel  context.CancelFunc
	// shown is set once a load landed, and stays while the next load
	// switches to another scope.
	shown bool
}

// loadPhase is what the window shows while a load runs: nothing yet on a
// first load, or the changes of the scope before while it switches.
type loadPhase uint8

const (
	loadIdle loadPhase = iota
	loadFirst
	loadSwitch
)

// loadSink takes what the latest load read, on the UI thread.
type loadSink interface {
	// scopeLanded takes the changes of the scope and the files of the
	// first page.
	scopeLanded(landing)
	// commitsLocated takes the commit that now holds the changes the
	// review of the scope was written for, or "".
	commitsLocated(movedTo string)
	// fileRead takes a file read after the scope landed.
	fileRead(contents)
}

// scopeLoad is the scope a load reads, and whether it opens anew.
type scopeLoad struct {
	scope gitx.Scope
	fresh bool
}

// loadRequest is a load the app starts.
type loadRequest struct {
	repo *gitx.Repo
	scopeLoad
	// rev is the review of the scope.
	rev *review.Review
	// first are the files the first page shows, read before it lands.
	first []string
}

// landing is what a load hands to the sink once the changes are read.
type landing struct {
	scopeLoad
	rev *review.Review
	// enter is set when the window enters the scope only now, after it
	// showed the scope before while the load ran.
	enter   bool
	perLoad perLoad
	// first holds the files of the first page, by path.
	first map[string]contents
	cont  *continuity.Load
}

// contents is a file read for a diff or a code snippet. A file outside
// the changes has only its path in change.
type contents struct {
	change           gitx.FileChange
	oldText, newText string
	err              error
}

func newScopeLoader(reviews *continuity.Reviews, async, update func(func()), sink loadSink) *scopeLoader {
	return &scopeLoader{reviews: reviews, async: async, update: update, sink: sink}
}

// start reads the changes of req.scope and the files of the first page,
// and hands them to the sink at once, so the page does not grow as they
// come. Then the sink gets the commit that holds the changes of the
// review, and the other files one by one.
func (l *scopeLoader) start(req loadRequest) {
	l.stop()
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	l.gen++
	gen, keep := l.gen, l.shown
	l.pending = req.scopeLoad
	l.phase = loadFirst
	if keep {
		l.phase = loadSwitch
	}
	cont := l.reviews.Begin(req.repo.Root, req.scope, req.rev)
	l.async(func() {
		defer cancel()
		read, err := readScope(ctx, req.repo, req.scope, req.first)
		if err == nil {
			defer func() {
				if err := read.cr.Close(); err != nil && ctx.Err() == nil {
					log.Printf("content reader: %v", err)
				}
			}()
			cont.Read(read.perLoad.fp, read.fpErr)
		}
		l.update(func() {
			if gen != l.gen {
				return
			}
			l.phase = loadIdle
			if err == nil {
				l.shown = true
			}
			l.sink.scopeLanded(landing{
				scopeLoad: req.scopeLoad, rev: req.rev, enter: req.fresh && keep,
				perLoad: read.perLoad, first: read.first, cont: cont,
			})
		})
		if err != nil {
			return
		}
		if located := cont.Locate(ctx, req.repo); located != nil {
			l.update(func() {
				if gen == l.gen {
					l.sink.commitsLocated(located())
				}
			})
		}
		for _, f := range read.perLoad.files {
			if ctx.Err() != nil {
				return
			}
			if _, ok := read.first[f.Path]; ok {
				continue
			}
			oldText, newText, cerr := read.cr.Contents(f)
			l.update(func() {
				if gen == l.gen {
					l.sink.fileRead(contents{f, oldText, newText, cerr})
				}
			})
		}
	})
}

// again is the scope a reload reads, and whether it opens anew: the
// scope of the load that runs, or else current.
func (l *scopeLoader) again(current gitx.Scope) scopeLoad {
	if l.phase != loadIdle {
		return l.pending
	}
	return scopeLoad{scope: current}
}

// readFile reads a file outside the changes from snap and hands it to
// the sink, unless another load started.
func (l *scopeLoader) readFile(snap *gitx.Snapshot, path string) {
	gen := l.gen
	l.async(func() {
		text, err := snap.File(context.Background(), path)
		l.update(func() {
			if gen == l.gen {
				l.sink.fileRead(contents{change: gitx.FileChange{Path: path}, newText: text, err: err})
			}
		})
	})
}

// reset drops the load that runs, for a project that closes or opens.
func (l *scopeLoader) reset() {
	l.stop()
	l.gen++
	l.phase, l.shown = loadIdle, false
}

func (l *scopeLoader) stop() {
	if l.cancel != nil {
		l.cancel()
	}
}

// firstLoad reports whether a load runs while the window shows no
// changes yet.
func (l *scopeLoader) firstLoad() bool { return l.phase == loadFirst }

// switching reports whether a load runs while the window shows the
// changes it showed before.
func (l *scopeLoader) switching() bool { return l.phase == loadSwitch }

// idle reports whether no load runs.
func (l *scopeLoader) idle() bool { return l.phase == loadIdle }

// scopeRead is what readScope read: the changes, and the files of the
// first page. cr reads the other files, and the caller closes it.
type scopeRead struct {
	perLoad perLoad
	fpErr   error
	first   map[string]contents
	cr      *gitx.ContentReader
}

// readScope reads the changes of s and the files in first. On an error,
// perLoad holds only the error.
func readScope(ctx context.Context, r *gitx.Repo, s gitx.Scope, first []string) (scopeRead, error) {
	out := scopeRead{perLoad: newPerLoad()}
	p := &out.perLoad
	snap, err := r.Snapshot(ctx, s)
	// A scope that does not resolve has no fingerprint either.
	out.fpErr = err
	if err == nil {
		p.fp, out.fpErr = snap.Fingerprint(ctx)
		if p.files, err = snap.Changes(ctx); err == nil {
			p.diffstat, err = snap.DiffStat(ctx)
		}
	}
	if out.fpErr != nil {
		log.Printf("fingerprint: %v", out.fpErr)
	}
	if err != nil {
		out.perLoad = newPerLoad()
		out.perLoad.loadErr = err.Error()
		return out, err
	}
	p.snap, p.patchKnown = snap, out.fpErr == nil
	for _, f := range p.files {
		p.diffs[f.Path] = &fileDiff{change: f, loading: true}
	}
	if s.Kind == gitx.ScopeCommit {
		if cm, cerr := r.Commit(ctx, s.Commit); cerr == nil {
			p.commit = &cm
		} else {
			log.Printf("commit message: %v", cerr)
		}
	}
	out.cr = snap.NewContentReader(ctx)
	out.first = map[string]contents{}
	for _, path := range first {
		if i := slices.IndexFunc(p.files, func(f gitx.FileChange) bool { return f.Path == path }); i >= 0 {
			oldText, newText, cerr := out.cr.Contents(p.files[i])
			out.first[path] = contents{p.files[i], oldText, newText, cerr}
		} else {
			text, cerr := out.cr.File(path)
			out.first[path] = contents{change: gitx.FileChange{Path: path}, newText: text, err: cerr}
		}
	}
	return out, nil
}
