package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
	"github.com/psd-coder/agentdiff/internal/store"
)

const (
	// commitPage is how many commits the commit list reads at a time.
	commitPage = 50
	// commitsAhead is how close to the last commit read the list scrolls
	// before it reads the next page.
	commitsAhead = 10
	// commitsSearched is how many recent commits hold the changes of an
	// earlier review that moved.
	commitsSearched = 50
)

type fileDiff struct {
	change  gitx.FileChange
	state   *editor.DiffState
	err     string
	loading bool
}

// overviewRun is an overview being written; it belongs to the review it
// was started for, so switching scopes leaves it running.
type overviewRun struct {
	text     string
	activity string
	cancel   context.CancelFunc
}

type app struct {
	win *mygo.Window
	// queue takes the updates of a window-less app when a test wants to
	// run them on its own goroutine.
	queue chan func()
	store store.Store
	cfg   store.Config

	// watchFiles starts the watches that follow the open project, none
	// when nil.
	watchFiles watchFunc
	stopWatch  context.CancelFunc

	repo    *gitx.Repo
	repoErr string
	branch  string
	// head is the branch picked to compare with base; empty means the
	// checked-out one.
	head string
	base string
	// baseSet is true once the user picked the base, which then counts
	// even as the upstream of the branch.
	baseSet bool
	// rng is the custom range under review instead of a branch.
	rng       *gitx.Scope
	branches  []string
	worktrees []gitx.Worktree
	// branchesErr is why git could not list the branches or worktrees.
	branchesErr string
	commits     []gitx.Commit
	commitsErr  string
	commitsGen  int
	// commitLog is where the commit list reads its next pages, and
	// commitsTotal is how many commits the whole list has.
	commitLog    gitx.CommitLog
	commitsTotal int
	// commitsFor is the range the commit list was read for.
	commitsFor string
	// commitsReading is set while git reads the commit list or a page of
	// it. Only one read runs at a time.
	commitsReading bool
	// commitsFailedAt is the last row in view when a page failed. The
	// list reads the page again once it scrolls away from that row.
	commitsFailedAt int
	// commitsShown is the commitsGen of the commits the list of changes
	// showed in the last frame.
	commitsShown int
	// changeList is the place of the list of changes in the sidebar.
	changeList ui.ListState

	scope gitx.Scope
	// chosen is the scope the user opened last, which the list of
	// changes shows chosen while it loads.
	chosen   gitx.Scope
	rev      *review.Review
	files    []gitx.FileChange
	diffstat string
	loading  bool
	loadErr  string
	// shown is set once the window shows the changes of a scope, which
	// stay while the next scope loads, and switching while it does.
	shown        bool
	switching    bool
	loadingScope *scopeLoad
	diffs        map[string]*fileDiff
	// stopLoad cancels the git work of the latest load.
	stopLoad context.CancelFunc
	// folded holds the files the reviewer folded or unfolded in this
	// scope. The others are folded while they are viewed or big.
	folded map[string]bool
	gen    int
	// patch identifies the changes of the scope once patchKnown, see
	// gitx.Repo.PatchID.
	patch      string
	patchKnown bool
	// fileIDs maps the changed files to the IDs of their changes, see
	// gitx.Fingerprint.
	fileIDs map[string]string
	// grouped caches groups, which a frame reads several times.
	grouped groupsCache
	// archived are the archived reviews, the newest first, listed on the
	// overview of the uncommitted changes.
	archived []*earlier
	// kept holds the archive IDs of reviews restored in this session,
	// which stay even though none of their files is changed.
	kept map[string]bool
	// movedTo is the commit that now holds the changes a stale overview
	// of the uncommitted changes was written for.
	movedTo string
	// adoptedFrom is the scope the review was moved or copied from when
	// the scope opened.
	adoptedFrom *gitx.Scope

	reviews   map[string]*review.Review
	runs      map[*review.Review][]*logRun
	logOpen   bool
	logTarget string
	logPct    float32

	page      string
	history   []string
	forward   []string
	scrolls   map[string]*ui.ScrollState
	lists     map[string]*cardPage
	thread    string
	drafts    map[string]string
	overviews map[*review.Review]*overviewRun
	threadRun map[string]context.CancelFunc
	walkRuns  map[string]*walkRun
	ahead     []queuedWalk

	sidebarW    float32
	scopePct    float32
	mainPct     float32
	groupsOpen  map[string]*bool
	md          markdown.Options
	codeKey     codeThemeKey
	codeColors  *editor.Theme
	exportAgent bool
	exportDone  bool
	savedPath   string
	question    string
	asking      *askTarget
	extra       map[string]*extraFile
	reveal      *reveal

	settings     *settingsForm
	compareForm  *compareForm
	projectsOpen bool
	projectNav   pickList
	branchesOpen bool
	branchQuery  string
	branchNav    pickList
	// refocus names the element that takes the keyboard focus in the
	// next frame: a picker button once its list closed after a choice
	// ("project" or "branch"), or a sidebar ("changes" or "files").
	refocus       string
	shortcutsOpen bool

	cmdList      []*command
	cmds         map[string]*command
	inMenu       map[string]bool
	menuDirty    bool
	pending      string // the command the menu bar queued
	paletteOpen  bool
	paletteQuery string
	paletteNav   pickList
	// recording is the command whose shortcut the settings record.
	recording   string
	recordStart bool
	keysFilter  string
	keysNote    string
	// saveErr is why the last write of a review or the settings failed;
	// it clears once a later write works.
	saveErr string
}

func newApp(s store.Store) *app {
	return &app{
		store:       s,
		cfg:         s.LoadConfig(),
		diffs:       map[string]*fileDiff{},
		folded:      map[string]bool{},
		reviews:     map[string]*review.Review{},
		runs:        map[*review.Review][]*logRun{},
		logPct:      50,
		drafts:      map[string]string{},
		overviews:   map[*review.Review]*overviewRun{},
		threadRun:   map[string]context.CancelFunc{},
		walkRuns:    map[string]*walkRun{},
		extra:       map[string]*extraFile{},
		groupsOpen:  map[string]*bool{},
		kept:        map[string]bool{},
		sidebarW:    280,
		scopePct:    34,
		mainPct:     62,
		page:        "overview",
		scrolls:     map[string]*ui.ScrollState{},
		lists:       map[string]*cardPage{},
		exportAgent: true,
	}
}

// update runs fn on the main thread. Tests have no window and run it at
// once, or put it on queue.
func (a *app) update(fn func()) {
	switch {
	case a.win != nil:
		a.win.Update(fn)
	case a.queue != nil:
		a.queue <- fn
	default:
		fn()
	}
}

func (a *app) async(fn func()) {
	if a.win == nil {
		fn()
		return
	}
	go fn()
}

func (a *app) openRepo(path string) {
	r, err := gitx.Open(path)
	if err != nil {
		a.repoErr = err.Error()
		return
	}
	a.repo, a.repoErr = r, ""
	a.watch(r)
	a.cfg.AddRecent(r.Root)
	a.saveConfig()
	a.branch, a.head, a.base, a.branches, a.commits = "", "", "", nil, nil
	a.commitsTotal, a.commitLog = 0, gitx.CommitLog{}
	a.shown = false
	a.baseSet, a.rng, a.worktrees = false, nil, nil
	if a.win != nil {
		a.win.SetTitle("AgentDiff: " + r.Name())
	}
	a.async(func() {
		ctx := context.Background()
		branch, err := r.CurrentBranch(ctx)
		base := r.DefaultBase(ctx)
		branches, trees, lerr := readBranches(ctx, r)
		err = cmp.Or(err, lerr)
		a.update(func() {
			if a.repo != r {
				return
			}
			a.branch, a.base, a.branches, a.worktrees = branch, base, branches, trees
			a.branchesErr = errText(err)
			a.loadCommits(nil)
		})
	})
	a.setScope(gitx.Scope{Kind: gitx.ScopeUncommitted})
}

// closeRepo goes back to the welcome screen.
func (a *app) closeRepo() {
	a.repo = nil
	a.unwatch()
	a.cancelLoad()
	a.gen++
	a.projectsOpen, a.shown = false, false
	if a.win != nil {
		a.win.SetTitle("AgentDiff")
	}
}

// headBranch is the branch under review: the picked one, or the one
// checked out.
func (a *app) headBranch() string { return cmp.Or(a.head, a.branch) }

// commitBase is the base the commit list is relative to: on a feature
// branch the commits not on the base yet, else the latest commits.
func (a *app) commitBase() string {
	head := a.headBranch()
	if a.base == "" || head == "" || a.base == head || !a.baseSet && strings.HasSuffix(a.base, "/"+head) {
		return ""
	}
	return a.base
}

// listRange is the base and the head of the commit list.
func (a *app) listRange() (string, string) {
	if a.rng != nil {
		return a.rng.From, a.rng.To
	}
	return a.commitBase(), a.head
}

// allScope is every change of the commit list at once: the custom range,
// or the branch since its base.
func (a *app) allScope() (gitx.Scope, bool) {
	if a.rng != nil {
		return *a.rng, true
	}
	return a.branchScope()
}

// uncommittedShown reports whether the sidebar lists the uncommitted
// changes, which belong to the checked-out branch.
func (a *app) uncommittedShown() bool { return a.rng == nil && a.head == "" }

// compare reviews branch against base, as picked in the Compare dialog.
func (a *app) compare(branch, base string) {
	a.base, a.baseSet = base, true
	a.pickBranch(branch)
}

// pickBranch lists the commits of branch and opens all its changes.
// Without a base to compare with, it opens the uncommitted changes of
// the checked-out branch, or the newest commit of another one.
func (a *app) pickBranch(branch string) {
	a.head, a.rng = branch, nil
	if branch == a.branch {
		a.head = ""
	}
	if s, ok := a.allScope(); ok {
		a.loadCommits(nil)
		a.setScope(s)
		return
	}
	if a.head == "" {
		a.loadCommits(nil)
		a.setScope(gitx.Scope{Kind: gitx.ScopeUncommitted})
		return
	}
	a.loadCommits(func() {
		if len(a.commits) > 0 {
			a.setScope(gitx.Scope{Kind: gitx.ScopeCommit, Commit: a.commits[0].SHA})
		}
	})
}

// setRange reviews the changes between two revisions.
func (a *app) setRange(from, to string) {
	s := gitx.Scope{Kind: gitx.ScopeRange, From: from, To: to}
	a.rng = &s
	a.loadCommits(nil)
	a.setScope(s)
}

// branchScope is the changes of the branch under review since its base.
func (a *app) branchScope() (gitx.Scope, bool) {
	base := a.commitBase()
	if base == "" {
		return gitx.Scope{}, false
	}
	return a.branchScopeOf(a.headBranch(), base), true
}

// branchScopeOf is the changes of a branch since base. The checked-out
// branch is read from HEAD, any other one by its name.
func (a *app) branchScopeOf(branch, base string) gitx.Scope {
	if branch == a.branch {
		return gitx.Scope{Kind: gitx.ScopeBranch, Base: base, Branch: branch}
	}
	return gitx.Scope{Kind: gitx.ScopeBranch, Base: base, Head: branch}
}

// syncBranch reads the checked-out branch again, to follow a checkout
// made outside the app.
func (a *app) syncBranch() {
	r := a.repo
	if r == nil {
		return
	}
	a.async(func() {
		ctx := context.Background()
		branch, err := r.CurrentBranch(ctx)
		if err != nil {
			a.update(func() {
				if a.repo == r {
					a.branchesErr = err.Error()
				}
			})
			return
		}
		branches, trees, err := readBranches(ctx, r)
		a.update(func() {
			if a.repo != r {
				return
			}
			a.branchesErr = errText(err)
			a.worktrees = trees
			if branch != a.branch {
				a.checkedOut(branch, branches)
			} else {
				a.branches = branches
			}
		})
	})
}

// readBranches lists the branches and the worktrees of r, and returns
// the first error.
func readBranches(ctx context.Context, r *gitx.Repo) ([]string, []gitx.Worktree, error) {
	branches, err := r.Branches(ctx)
	trees, terr := r.Worktrees(ctx)
	return branches, trees, cmp.Or(err, terr)
}

// errText is the message of err, or "" when err is nil.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// checkedOut moves the app to a newly checked-out branch. A branch scope
// keeps reviewing the same branch, so its review stays.
func (a *app) checkedOut(branch string, branches []string) {
	a.branch, a.branches = branch, branches
	if s := a.scope; s.Kind == gitx.ScopeBranch && (s.Head != "" || s.Branch != "") {
		name := cmp.Or(s.Head, s.Branch)
		a.head = name
		if name == branch {
			a.head = ""
		}
		a.scope = a.branchScopeOf(name, s.Base)
		a.rev.Scope = a.scope
	} else if a.head == branch {
		a.head = ""
	}
	a.loadCommits(nil)
	a.reload()
}

func (a *app) refresh() {
	if a.repo == nil {
		return
	}
	a.reload()
	a.loadCommits(nil)
	a.syncBranch()
}

// loadCommits reads the commit list again, then runs then, if given.
// For the same range it reads as many commits as the list already has,
// so the list keeps its rows and its place.
func (a *app) loadCommits(then func()) {
	a.commitsGen++
	r, gen := a.repo, a.commitsGen
	base, head := a.listRange()
	key := base + ".." + cmp.Or(head, a.branch)
	fresh := key != a.commitsFor
	limit := commitPage
	if !fresh {
		limit = max(limit, len(a.commits))
	}
	a.commitsFor = key
	a.commitsReading = true
	a.async(func() {
		ctx := context.Background()
		cl, err := r.CommitLog(ctx, base, head)
		total := 0
		var commits []gitx.Commit
		if err == nil {
			total, err = cl.Count(ctx)
		}
		if err == nil {
			commits, err = cl.Page(ctx, 0, limit)
		}
		a.update(func() {
			if a.repo != r || gen != a.commitsGen {
				return
			}
			if err != nil {
				commits, total = nil, 0
			}
			a.commits, a.commitsTotal, a.commitLog, a.commitsErr = commits, total, cl, errText(err)
			a.commitsReading = false
			if fresh {
				a.changeList.ScrollTo(0, ui.Start)
			}
			if then != nil {
				then()
			}
		})
	})
}

// loadMoreCommits reads the next page of the commit list when last, the
// last commit in view, is near the end of the commits read. The rows in
// view count once the list has shown the commits read last.
func (a *app) loadMoreCommits(last int) {
	n := len(a.commits)
	if a.commitsShown != a.commitsGen || n >= a.commitsTotal || last < n-commitsAhead ||
		a.commitsErr != "" && last == a.commitsFailedAt {
		return
	}
	a.readCommitPage(last)
}

// readCommitPage reads the page after the commits read, unless a read
// runs. last is the last commit in view.
func (a *app) readCommitPage(last int) {
	if a.commitsReading {
		return
	}
	n := len(a.commits)
	a.commitsReading = true
	r, gen, cl := a.repo, a.commitsGen, a.commitLog
	a.async(func() {
		page, err := cl.Page(context.Background(), n, commitPage)
		a.update(func() {
			if a.repo != r || gen != a.commitsGen {
				return
			}
			a.commitsReading = false
			a.commitsErr, a.commitsFailedAt = errText(err), last
			if err == nil && len(page) < commitPage {
				// A short page ends the log, whatever git counted.
				a.commitsTotal = n + len(page)
			}
			a.commits = append(a.commits, page...)
		})
	})
}

func reviewKey(repo string, s gitx.Scope) string { return repo + "\x00" + s.Key() }

// reviewOf loads a review once and keeps it, so an agent run that finishes
// after a scope switch writes into the review the view shows.
func (a *app) reviewOf(s gitx.Scope) *review.Review {
	key := reviewKey(a.repo.Root, s)
	r := a.reviews[key]
	if r == nil {
		r = a.store.LoadReview(a.repo.Root, s)
		a.reviews[key] = r
	}
	return r
}

func (a *app) setScope(s gitx.Scope) {
	a.chosen = s
	a.load(s, true)
}

// reload reads the changes of the scope again, keeping the review: of
// the scope being loaded, if one is.
func (a *app) reload() {
	if t := a.loadingScope; t != nil {
		a.load(t.scope, t.fresh)
		return
	}
	a.load(a.scope, false)
}

// scopeLoad is a scope load reads, and whether it opens anew.
type scopeLoad struct {
	scope gitx.Scope
	fresh bool
}

// enterScope makes s, with its review rev, the scope the window shows.
func (a *app) enterScope(s gitx.Scope, rev *review.Review) {
	if a.leaveReview() {
		a.saveReview()
	}
	a.scope, a.rev = s, rev
	a.resetNav()
	a.folded = map[string]bool{}
	a.exportDone = false
	a.adoptedFrom = nil
}

// contents is a file read for a diff or a code snippet.
type contents struct {
	change           gitx.FileChange
	oldText, newText string
	err              error
}

// load reads the changes of s, with the files the first page shows, and
// shows them all at once, so the page does not grow as they come. Until
// then the window keeps what it shows, under a spinner. A new scope
// opens with its own review on its overview.
func (a *app) load(s gitx.Scope, fresh bool) {
	a.cancelLoad()
	ctx, cancel := context.WithCancel(context.Background())
	a.stopLoad = cancel
	a.gen++
	gen, r := a.gen, a.repo
	rev, page := a.rev, a.page
	if fresh {
		rev, page = a.reviewOf(s), "overview"
		rev.Scope = s
	}
	a.loadingScope = &scopeLoad{s, fresh}
	entered := !fresh
	if a.shown && a.rev != nil && a.rev.Repo == r.Root {
		a.switching = true
	} else {
		if fresh {
			a.enterScope(s, rev)
			entered = true
		}
		a.clearChanges()
		a.loading = true
	}
	written, empty := "", rev.Empty()
	var writtenFiles []string
	if ov := rev.Overview; ov != nil {
		written, writtenFiles = ov.PatchID, ov.WrittenFiles()
	}
	first := a.firstPaint(rev, page, fresh)
	uncommitted := s.Kind == gitx.ScopeUncommitted
	a.async(func() {
		defer cancel()
		files, err := r.Changes(ctx, s)
		stat := ""
		if err == nil {
			stat, err = r.DiffStat(ctx, s)
		}
		fp, perr := r.Fingerprint(ctx, s)
		if perr != nil {
			log.Printf("fingerprint: %v", perr)
		}
		var archived []*earlier
		if uncommitted {
			for _, arch := range a.store.Archived(r.Root) {
				archived = append(archived, &earlier{rev: arch})
			}
		}
		// live is the review of the scope, for when it goes to the archive.
		live := &earlier{}
		// wanted are the changes to find in the recent commits: those of
		// the archived reviews, and under "" those of a drifted review.
		wanted := map[string]changeID{}
		if uncommitted && perr == nil && written != "" && written != fp.Patch {
			wanted[""] = changeID{written, writtenFiles}
		}
		for _, e := range archived {
			wanted[e.id()] = changeID{e.rev.Overview.PatchID, e.rev.Overview.WrittenFiles()}
		}
		var from *gitx.Scope
		if (empty || uncommitted && written != fp.Patch) && perr == nil && fp.Patch != "" {
			if src, ok := a.store.FindReview(r.Root, s, fp.Patch); ok {
				from = &src
			}
		}
		// readFile reads through one git process for the whole load, or
		// starts git for each file when that process cannot start.
		readFile := func(f gitx.FileChange) (string, string, error) { return r.Contents(ctx, s, f) }
		// The files of the first page, read before it shows.
		read := map[string]contents{}
		if err == nil {
			if cr, cerr := r.NewContentReader(ctx, s); cerr == nil {
				defer func() {
					if err := cr.Close(); err != nil && ctx.Err() == nil {
						log.Printf("content reader: %v", err)
					}
				}()
				readFile = cr.Contents
			} else if ctx.Err() == nil {
				log.Printf("content reader: %v", cerr)
			}
			for _, p := range first {
				f := gitx.FileChange{Path: p, Status: gitx.StatusAdded}
				if i := slices.IndexFunc(files, func(f gitx.FileChange) bool { return f.Path == p }); i >= 0 {
					f = files[i]
				}
				oldText, newText, cerr := readFile(f)
				read[p] = contents{f, oldText, newText, cerr}
			}
		}
		a.update(func() {
			if gen != a.gen {
				return
			}
			if !entered {
				a.enterScope(s, rev)
			}
			a.clearChanges()
			a.loading, a.switching, a.loadingScope = false, false, nil
			if err != nil {
				a.loadErr = err.Error()
				return
			}
			a.shown = true
			a.files, a.diffstat = files, stat
			a.patch, a.patchKnown, a.fileIDs = fp.Patch, perr == nil, fp.Files
			// The list gets its own array: deleting from it in place
			// would clear entries the commit lookup below still reads.
			a.archived = slices.Clone(archived)
			// These run on every load, so a failure goes to the notice,
			// which stays, instead of a dialog each time.
			var serr error
			switch {
			case from != nil && a.rev.Empty():
				serr = a.adopt(*from)
			case from != nil && a.archivable():
				if serr = a.archiveCurrent(live); serr == nil {
					serr = a.adopt(*from)
				}
			case a.archivable() && a.rev.Overview.Drift(a.fileIDs).Disjoint():
				serr = a.archiveCurrent(live)
			}
			if serr != nil {
				a.saveErr = serr.Error()
			}
			if from := a.adoptedFrom; from != nil {
				a.archived = slices.DeleteFunc(a.archived, func(e *earlier) bool { return e.rev.Scope == *from })
			}
			for _, f := range files {
				a.diffs[f.Path] = &fileDiff{change: f, loading: true}
			}
			for p, c := range read {
				if a.diffs[p] != nil {
					a.setContents(c.change, c.oldText, c.newText, c.err)
				} else {
					a.extra[p] = newExtraFile(c.newText, c.err)
				}
			}
			if f, ok := strings.CutPrefix(a.page, "file:"); ok && a.diffs[f] == nil {
				a.page = "overview"
			}
		})
		if err != nil {
			return
		}
		if len(wanted) > 0 && ctx.Err() == nil {
			moved, probable := findCommits(ctx, r, wanted)
			a.update(func() {
				if gen != a.gen {
					return
				}
				live.movedTo, live.probably = moved[""], probable[""]
				if !slices.Contains(a.archived, live) {
					a.movedTo = live.movedTo
				}
				for _, e := range archived {
					e.movedTo, e.probably = moved[e.id()], probable[e.id()]
				}
			})
		}
		for _, f := range files {
			if ctx.Err() != nil {
				return
			}
			if _, ok := read[f.Path]; ok {
				continue
			}
			oldText, newText, cerr := readFile(f)
			a.update(func() {
				if gen != a.gen {
					return
				}
				a.setContents(f, oldText, newText, cerr)
			})
		}
	})
}

// cancelLoad stops the git work of the latest load, whose results are
// stale once another load starts or the project closes.
func (a *app) cancelLoad() {
	if a.stopLoad != nil {
		a.stopLoad()
	}
}

// clearChanges forgets the changes of the scope, which load reads.
func (a *app) clearChanges() {
	a.loadErr = ""
	a.files, a.diffstat = nil, ""
	a.patch, a.patchKnown, a.movedTo, a.fileIDs = "", false, "", nil
	a.diffs = map[string]*fileDiff{}
	a.extra = map[string]*extraFile{}
}

// firstPaint are the files the page shows first: the code snippets of
// an overview or a walkthrough, the file of a file page, the files of a
// group page.
func (a *app) firstPaint(rev *review.Review, page string, fresh bool) []string {
	var paths []string
	walk := func(w *review.Walkthrough) {
		if w == nil {
			return
		}
		for _, s := range w.Steps {
			for _, r := range s.Refs {
				paths = append(paths, r.File)
			}
		}
		for _, c := range w.Checklist {
			for _, r := range c.Refs {
				paths = append(paths, r.File)
			}
		}
	}
	switch {
	case page == "overview" && rev.Overview != nil:
		walk(rev.Overview.Walk)
	case strings.HasPrefix(page, "walk:"):
		if w := rev.Walk(strings.TrimPrefix(page, "walk:")); w != nil {
			walk(w.Content)
		}
	case strings.HasPrefix(page, "file:"):
		paths = append(paths, strings.TrimPrefix(page, "file:"))
	case strings.HasPrefix(page, "group:") && !fresh:
		paths = append(paths, a.groupFiles(strings.TrimPrefix(page, "group:"))...)
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

// adopt takes over the saved review of src, which was written for the
// same changes as the current scope has. A review of uncommitted changes
// moves, because those changes are gone from the working tree; any other
// one is copied.
func (a *app) adopt(src gitx.Scope) error {
	if !a.rev.Empty() {
		return nil
	}
	srcKey := reviewKey(a.repo.Root, src)
	from := a.reviews[srcKey]
	if from != nil && a.reviewBusy(from) {
		return nil
	}
	move := src.Kind == gitx.ScopeUncommitted || src.Kind == gitx.ScopeArchived
	var err error
	if move {
		err = a.store.MoveReview(a.repo.Root, src, a.scope)
	} else {
		err = a.store.CopyReview(a.repo.Root, src, a.scope)
	}
	if err != nil {
		return fmt.Errorf("adopt review: %w", err)
	}
	if move && from != nil {
		delete(a.reviews, srcKey)
		delete(a.runs, from)
	}
	delete(a.runs, a.rev)
	a.rev = a.store.LoadReview(a.repo.Root, a.scope)
	a.reviews[reviewKey(a.repo.Root, a.scope)] = a.rev
	a.adoptedFrom = &src
	return nil
}

// archivable reports whether the review of the uncommitted changes can
// go to the archive: it has an overview, no agent writes into it, and the
// user did not restore it in this session.
func (a *app) archivable() bool {
	ov := a.rev.Overview
	return a.scope.Kind == gitx.ScopeUncommitted && ov.Done() && a.patchKnown && !a.kept[archiveID(ov)] && !a.reviewBusy(a.rev)
}

// archiveID names an archived review: the patch ID of its overview, or
// the time it was written for an overview from before patch IDs.
func archiveID(ov *review.Overview) string {
	return cmp.Or(ov.PatchID, fmt.Sprint(ov.Generated.Unix()))
}

// changeID identifies the changes a review was written for.
type changeID struct {
	patch string
	files []string
}

// findCommits looks for the changes in the recent commits. moved holds
// the commit with the same changes, probable a commit that changes all
// the files of changes no commit holds as they are.
func findCommits(ctx context.Context, r *gitx.Repo, wanted map[string]changeID) (moved, probable map[string]string) {
	ids, err := r.CommitPatchIDs(ctx, "", "", commitsSearched)
	if err != nil {
		log.Printf("commit patch ids: %v", err)
	}
	moved, sets := map[string]string{}, map[string][]string{}
	for key, w := range wanted {
		if sha := ids[w.patch]; sha != "" && w.patch != "" {
			moved[key] = sha
		} else {
			sets[key] = w.files
		}
	}
	probable, err = r.CommitsWithFiles(ctx, sets, commitsSearched)
	if err != nil {
		log.Printf("commits with files: %v", err)
	}
	return moved, probable
}

// earlier is an archived review, with the commit that now holds the
// changes it was written for. When none does, probably is a commit that
// changes all of its files.
type earlier struct {
	rev      *review.Review
	movedTo  string
	probably string
}

// id names the archived review in the page that shows it.
func (e *earlier) id() string { return e.rev.Scope.From }

func (a *app) earlierReview(id string) *earlier {
	for _, e := range a.archived {
		if e.id() == id {
			return e
		}
	}
	return nil
}

// dropEarlier takes an archived review off the list, and leaves its page
// when it is open.
func (a *app) dropEarlier(e *earlier) {
	a.archived = slices.DeleteFunc(a.archived, func(x *earlier) bool { return x == e })
	if a.page == "archived:"+e.id() {
		a.goBack()
	}
	if a.page == "archived:"+e.id() {
		a.page = "overview"
	}
}

// archiveCurrent moves the review of the uncommitted changes into the
// archive and starts an empty one. e holds the commits found for the
// review's changes.
func (a *app) archiveCurrent(e *earlier) error {
	old := a.rev
	to, err := a.store.ArchiveReview(a.repo.Root, archiveID(old.Overview))
	if err != nil {
		return fmt.Errorf("archive review: %w", err)
	}
	delete(a.runs, old)
	old.Scope = to
	if a.leaveReview() {
		a.saved(a.store.SaveReview(old))
	}
	e.rev = old
	a.archived = append([]*earlier{e}, a.archived...)
	a.movedTo, a.adoptedFrom = "", nil
	a.rev = review.New(a.repo.Root, a.scope)
	a.reviews[reviewKey(a.repo.Root, a.scope)] = a.rev
	a.resetNav()
	return nil
}

// restoreArchived brings an archived review back to the uncommitted
// scope, which keeps it from then on.
func (a *app) restoreArchived(e *earlier) {
	if !a.rev.Empty() {
		return
	}
	if err := a.adopt(e.rev.Scope); err != nil {
		a.showError("Could not restore the review", err)
		return
	}
	a.kept[archiveID(e.rev.Overview)] = true
	a.adoptedFrom = nil
	a.dropEarlier(e)
	a.openPage("overview")
}

// moveArchived moves an archived review to the commit that probably
// holds its changes, when that commit has no review of its own, and
// opens the commit.
func (a *app) moveArchived(e *earlier) {
	to := gitx.Scope{Kind: gitx.ScopeCommit, Commit: e.probably}
	if a.store.LoadReview(a.repo.Root, to).Empty() {
		if err := a.store.MoveReview(a.repo.Root, e.rev.Scope, to); err != nil {
			a.showError("Could not move the review", err)
			return
		}
		delete(a.reviews, reviewKey(a.repo.Root, to))
		a.dropEarlier(e)
	}
	a.setScope(to)
}

func (a *app) deleteArchived(e *earlier) {
	if err := a.store.DeleteReview(a.repo.Root, e.rev.Scope); err != nil {
		a.showError("Could not delete the review", err)
		return
	}
	a.dropEarlier(e)
}

// reviewBusy reports whether an agent run writes into rev.
func (a *app) reviewBusy(rev *review.Review) bool {
	if a.overviews[rev] != nil || slices.ContainsFunc(a.ahead, func(q queuedWalk) bool { return q.rev == rev }) {
		return true
	}
	for _, w := range rev.Walks {
		if a.walkRuns[w.ID] != nil {
			return true
		}
	}
	for _, t := range rev.Threads {
		if a.threadBusy(t.ID) {
			return true
		}
	}
	return false
}

// stale reports whether the overview was written for other changes than
// the scope has now.
func (a *app) stale() bool {
	ov := a.rev.Overview
	if !ov.Done() || a.loading || (ov.PatchID != "" && !a.patchKnown) || a.overviewRunning() != nil {
		return false
	}
	return ov.Stale(a.patch, a.fileNames())
}

func (a *app) setContents(f gitx.FileChange, oldText, newText string, err error) {
	d := a.diffs[f.Path]
	if d == nil {
		return
	}
	d.loading = false
	switch {
	case errors.Is(err, gitx.ErrBinary):
		d.err = "Binary file"
		return
	case errors.Is(err, gitx.ErrTooLarge):
		d.err = "File too large to show"
		return
	case err != nil:
		d.err = err.Error()
		return
	}
	lang := editor.LanguageFor(f.Path)
	before, after := editor.NewBuffer(oldText), editor.NewBuffer(newText)
	before.SetLanguage(lang)
	after.SetLanguage(lang)
	d.state = &editor.DiffState{Old: before, New: after, LineSelection: true, Wrap: false}
	if a.keepSnippets() {
		a.saveReview()
	}
}

// keepSnippets saves the code of the snippets of the review into it,
// while the changes are the ones the review was written for. An archived
// review shows this code after the changes are gone. It reports whether
// it saved any.
func (a *app) keepSnippets() bool {
	ov := a.rev.Overview
	if a.loading || !ov.Done() || ov.PatchID != "" && !a.patchKnown || a.overviewRunning() != nil || a.stale() {
		return false
	}
	kept := false
	a.rev.EachRef(func(r *review.Ref) {
		if len(r.Code) > 0 {
			return
		}
		s, ok := a.loadedSnippet(*r)
		if !ok || s.loading || s.missing != "" || len(s.lines) == 0 {
			return
		}
		for i, line := range s.lines {
			r.Code = append(r.Code, review.CodeLine{Text: line, Changed: s.changed[i]})
		}
		kept = true
	})
	return kept
}

func (a *app) fileNames() []string {
	out := make([]string, len(a.files))
	for i, f := range a.files {
		out[i] = f.Path
	}
	return out
}

// fileOrder is the order the sidebar lists the files in.
func (a *app) fileOrder() []string {
	if a.cfg.FlatFiles {
		return a.fileNames()
	}
	var files []string
	for _, g := range a.groups() {
		files = append(files, g.Files...)
	}
	return files
}

// listed tells whether the sidebar lists the file.
func (a *app) listed(file string) bool {
	return !a.cfg.HideViewed || !a.rev.Viewed[file]
}

// setViewed marks the files viewed and folds them, or marks them not
// viewed and unfolds them, except big files, which stay folded.
func (a *app) setViewed(files []string, viewed bool) {
	for _, f := range files {
		delete(a.folded, f)
		a.rev.Viewed[f] = viewed
	}
	a.saveReview()
}

// isFolded reports whether the card of the file shows only its header.
func (a *app) isFolded(file string) bool {
	if f, ok := a.folded[file]; ok {
		return f
	}
	if d := a.diffs[file]; d != nil && d.change.Additions+d.change.Deletions > bigFileLines {
		return true
	}
	return a.rev.Viewed[file]
}

// groups are the groups the sidebar lists the files in. Callers must
// not change the slice they get.
func (a *app) groups() []review.Group {
	var from []review.Group
	if a.rev.Overview != nil {
		from = a.rev.Overview.Groups
	}
	if g := &a.grouped; sameSlice(g.from, from) && sameSlice(g.files, a.files) {
		return g.groups
	}
	var groups []review.Group
	if len(from) > 0 {
		groups = review.NormalizeGroups(from, a.fileNames())
	} else {
		groups = review.DirectoryGroups(a.fileNames())
	}
	a.grouped = groupsCache{groups: groups, from: from, files: a.files}
	return groups
}

// groupsCache holds the groups made from the groups of an overview and
// the changed files. Every change of the files or the overview groups
// puts a new slice in place, so the same slices give the same groups.
// The cache keeps the slices it was made from, so a new slice cannot
// get the address of an old one. The zero cache holds the groups of no
// files: none.
type groupsCache struct {
	groups []review.Group
	from   []review.Group
	files  []gitx.FileChange
}

// sameSlice reports whether x and y are the same elements of the same
// array.
func sameSlice[T any](x, y []T) bool {
	return len(x) == len(y) && (len(x) == 0 || &x[0] == &y[0])
}

// startOver forgets the review of the current scope: its overview,
// threads and viewed files.
func (a *app) startOver() {
	if err := a.store.DeleteReview(a.repo.Root, a.scope); err != nil {
		a.showError("Could not start the review over", err)
		return
	}
	a.stopOverview()
	for _, t := range a.rev.Threads {
		a.stopThread(t)
	}
	for _, w := range a.rev.Walks {
		a.stopWalk(w.ID)
	}
	delete(a.runs, a.rev)
	a.leaveReview()
	a.rev = review.New(a.repo.Root, a.scope)
	a.reviews[reviewKey(a.repo.Root, a.scope)] = a.rev
	a.resetNav()
	a.drafts = map[string]string{}
}

func (a *app) confirmStartOver() {
	a.confirm("Start this review over?", "The overview, the deeper walkthroughs, all comments and threads, and the viewed marks of this scope are deleted.", "Start Over", a.startOver)
}

// confirm runs fn after the user agrees in a warning dialog. Without a
// window, in tests, it runs fn at once.
func (a *app) confirm(message, detail, button string, fn func()) {
	if a.win == nil {
		fn()
		return
	}
	go func() {
		res, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent: a.win, Type: mygo.MessageWarning,
			Message: message, Detail: detail,
			Buttons: []string{button, "Cancel"},
		})
		if err == nil && res.Button == 0 {
			a.update(fn)
		}
	}()
}

// showError tells the user that an action failed, in an error dialog.
// Without a window, in tests, it logs the error.
func (a *app) showError(message string, err error) {
	if a.win == nil {
		log.Printf("%s: %v", message, err)
		return
	}
	go func() {
		_, _ = mygo.Dialog.Message(mygo.MessageOptions{
			Parent: a.win, Type: mygo.MessageError,
			Message: message, Detail: err.Error(),
		})
	}()
}

func (a *app) saveReview() {
	if a.rev != nil {
		a.saveReviewOf(a.rev)
	}
}

// saveReviewOf saves r while it is the review of its scope. A review that
// was started over or replaced is obsolete, and a late agent answer must
// not write it back.
func (a *app) saveReviewOf(r *review.Review) {
	if a.obsolete(r) {
		return
	}
	if r == a.rev {
		a.keepSnippets()
	}
	a.saved(a.store.SaveReview(r))
}

// obsolete reports whether r is no longer the review the app keeps for
// its scope.
func (a *app) obsolete(r *review.Review) bool {
	return a.reviews[reviewKey(r.Repo, r.Scope)] != r
}

func (a *app) saveConfig() {
	a.saved(a.store.SaveConfig(a.cfg))
}

// saved keeps the error of a write for the notice, or clears the notice
// when the write worked.
func (a *app) saved(err error) {
	a.saveErr = errText(err)
}

func (a *app) scopeLabel() string {
	if a.scope.Kind == gitx.ScopeCommit {
		for _, c := range a.commits {
			if c.SHA == a.scope.Commit {
				return fmt.Sprintf("%s %s", c.Short, c.Subject)
			}
		}
	}
	return a.scope.Label()
}

func (a *app) openThread(id string) {
	a.closeEmptyThread(id)
	a.thread, a.question, a.asking = id, "", nil
}

// closeEmptyThread drops a thread the user opened and left without
// writing anything, unless it is the one being opened. It reports whether
// it dropped one.
func (a *app) closeEmptyThread(keep string) bool {
	if a.thread == "" || a.thread == keep || a.rev == nil {
		return false
	}
	t := a.rev.Thread(a.thread)
	if t == nil || len(t.Messages) > 0 {
		return false
	}
	a.rev.DeleteThread(t.ID)
	delete(a.drafts, t.ID)
	return true
}

// leaveReview closes the side panels and the export note of the review
// the window shows, before another review takes its place. It reports
// whether it dropped an empty thread. Then the caller saves the review.
func (a *app) leaveReview() bool {
	dropped := a.closeEmptyThread("")
	a.thread = ""
	a.closeQuestion()
	a.savedPath = ""
	return dropped
}

func (a *app) newThread(anchor review.Anchor) {
	t := a.rev.AddThread(anchor)
	a.openThread(t.ID)
}

// quoteLines returns lines start..end (1-based) of a side of a file.
func (a *app) quoteLines(path string, side review.Side, start, end int) string {
	d := a.diffs[path]
	if d == nil || d.state == nil {
		return ""
	}
	buf := d.state.New
	if side == review.SideOld {
		buf = d.state.Old
	}
	var lines []string
	for i := start - 1; i < end && i < buf.LineCount(); i++ {
		if i >= 0 {
			lines = append(lines, buf.Line(i))
		}
	}
	return strings.Join(lines, "\n")
}

func sideOf(s editor.DiffSide) review.Side {
	if s == editor.DiffOld {
		return review.SideOld
	}
	return review.SideNew
}

func diffSide(s review.Side) editor.DiffSide {
	if s == review.SideOld {
		return editor.DiffOld
	}
	return editor.DiffNew
}
