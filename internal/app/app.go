package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/continuity"
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
)

type fileDiff struct {
	change  gitx.FileChange
	state   *editor.DiffState
	err     string
	loading bool
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
	// startAgent starts every agent run.
	startAgent agentFunc
	// askConfirm replaces the confirm dialog in tests, when set.
	askConfirm func(message, detail, button string, fn func())

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
	commits     commitList
	// changeList is the place of the list of changes in the sidebar.
	changeList ui.ListState

	scope gitx.Scope
	// chosen is the scope the user opened last, which the list of
	// changes shows chosen while it loads.
	chosen  gitx.Scope
	rev     *review.Review
	loading bool
	// shown is set once the window shows the changes of a scope, which
	// stay while the next scope loads, and switching while it does.
	shown        bool
	switching    bool
	loadingScope *scopeLoad
	// stopLoad cancels the git work of the latest load.
	stopLoad context.CancelFunc
	gen      int
	perLoad
	// grouped caches groups, which a frame reads several times.
	grouped groupsCache

	reviews   *continuity.Reviews
	logOpen   bool
	logTarget string
	logPct    float32

	perReview
	// drafts holds the unsent text of each thread and question for the
	// session, by questionDraftKey or thread ID.
	drafts map[string]string
	runner *runner
	// streams counts the goroutines that stream agent runs into the store,
	// so a test can wait for them before its store is removed.
	streams sync.WaitGroup
	// logs is the run log of each review, newest first, in memory.
	logs    map[*review.Review][]*logRun
	targets *targets

	sidebarW    float32
	scopePct    float32
	mainPct     float32
	groupsOpen  map[string]*bool
	md          markdown.Options
	codeKey     codeThemeKey
	codeColors  *editor.Theme
	exportAgent bool
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

// perReview is the state of the window for the review it shows. Leaving
// the review replaces all of it, see leaveReview.
type perReview struct {
	nav     navigation
	scrolls map[string]*ui.ScrollState
	lists   map[page]*cardPage
	panel   sidePanel
	// exportDone includes the resolved threads in the feedback, and
	// savedPath is where the feedback was saved last.
	exportDone bool
	savedPath  string
	// folded holds the files the reviewer folded or unfolded. The others
	// are folded while they are viewed or big.
	folded map[string]bool
}

func newPerReview() perReview {
	return perReview{
		scrolls: map[string]*ui.ScrollState{},
		lists:   map[page]*cardPage{},
		folded:  map[string]bool{},
	}
}

// sidePanel is what the side panel shows: a panelThread, a panelQuestion
// or a panelAsk. It is nil while the panel is closed.
type sidePanel interface{ isSidePanel() }

// panelThread is the ID of the thread the panel shows.
type panelThread string

// panelQuestion is the ID of the question walk the panel shows.
type panelQuestion string

// panelAsk is the target of a new question the panel asks.
type panelAsk askTarget

func (panelThread) isSidePanel()   {}
func (panelQuestion) isSidePanel() {}
func (panelAsk) isSidePanel()      {}

// threadID is the thread the side panel shows, or "".
func (a *app) threadID() string {
	id, _ := a.panel.(panelThread)
	return string(id)
}

// questionID is the question walk the side panel shows, or "".
func (a *app) questionID() string {
	id, _ := a.panel.(panelQuestion)
	return string(id)
}

// asking is the target of the new question the side panel asks, or nil.
func (a *app) asking() *askTarget {
	if t, ok := a.panel.(panelAsk); ok {
		return (*askTarget)(&t)
	}
	return nil
}

// perLoad is what a load read about the changes of the scope. A new load
// replaces all of it.
type perLoad struct {
	// snap is the snapshot the load read. Files outside the changes are
	// read from it too.
	snap     *gitx.Snapshot
	files    []gitx.FileChange
	diffstat string
	loadErr  string
	// fp identifies the changes of the scope once patchKnown.
	fp         gitx.Fingerprint
	patchKnown bool
	diffs      map[string]*fileDiff
	// extra holds the files the code snippets show that are not changed.
	extra map[string]*extraFile
	// commit is the commit under review, for a commit scope.
	commit *gitx.Commit
	// movedTo is the commit that now holds the changes a stale overview
	// of the uncommitted changes was written for.
	movedTo string
}

func newPerLoad() perLoad {
	return perLoad{diffs: map[string]*fileDiff{}, extra: map[string]*extraFile{}}
}

func newApp(s store.Store) *app {
	a := &app{
		store:       s,
		cfg:         s.LoadConfig(),
		startAgent:  agent.Run,
		perLoad:     newPerLoad(),
		perReview:   newPerReview(),
		reviews:     continuity.New(s),
		logPct:      50,
		drafts:      map[string]string{},
		logs:        map[*review.Review][]*logRun{},
		groupsOpen:  map[string]*bool{},
		sidebarW:    280,
		scopePct:    34,
		mainPct:     62,
		exportAgent: true,
	}
	a.runner = newRunner(a.stream, a.saveReviewOf)
	a.targets = newTargets(a.runner, a.onScreen)
	return a
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
	a.branch, a.head, a.base, a.branches = "", "", "", nil
	a.commits.reset()
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
			a.loadCommits()
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
	a.loading, a.switching, a.loadingScope = false, false, nil
	a.perLoad = newPerLoad()
	a.projectsOpen, a.shown = false, false
	a.commits.reset()
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
	s, all := a.allScope()
	a.commits.newest = !all && a.head != ""
	a.loadCommits()
	switch {
	case all:
		a.setScope(s)
	case a.head == "":
		a.setScope(gitx.Scope{Kind: gitx.ScopeUncommitted})
	}
}

// setRange reviews the changes between two revisions.
func (a *app) setRange(from, to string) {
	s := gitx.Scope{Kind: gitx.ScopeRange, From: from, To: to}
	a.rng = &s
	a.loadCommits()
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
	a.loadCommits()
	a.reload()
}

func (a *app) refresh() {
	if a.repo == nil {
		return
	}
	a.reload()
	a.loadCommits()
	a.syncBranch()
}

// loadCommits reads the commit list again, for the range in view.
func (a *app) loadCommits() {
	r := a.repo
	base, head := a.listRange()
	gen, limit := a.commits.read(r.Root + "\x00" + base + ".." + cmp.Or(head, a.branch))
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
			if sha := a.commits.listed(gen, cl, commits, total, err); sha != "" {
				a.setScope(gitx.Scope{Kind: gitx.ScopeCommit, Commit: sha})
			}
		})
	})
}

// readCommitPage reads the page after the commits read, unless a read
// runs. last is the last commit in view.
func (a *app) readCommitPage(last int) {
	gen, from, ok := a.commits.readPage(last)
	if !ok {
		return
	}
	cl := a.commits.log
	a.async(func() {
		page, err := cl.Page(context.Background(), from, commitPage)
		a.update(func() { a.commits.paged(gen, page, err) })
	})
}

func (a *app) setScope(s gitx.Scope) {
	a.commits.newest = false
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
	a.reviews.ClearAdopted()
	a.scope, a.rev = s, rev
	a.targets.seen(rev, review.TargetOverview)
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
	rev, page := a.rev, a.nav.page
	if fresh {
		rev, page = a.reviews.Of(r.Root, s), overview
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
		a.perLoad = newPerLoad()
		a.loading = true
	}
	before := continuity.BeforeLoad(rev)
	first := a.firstPaint(rev, page, fresh)
	a.async(func() {
		defer cancel()
		var files []gitx.FileChange
		var stat string
		var fp gitx.Fingerprint
		snap, err := r.Snapshot(ctx, s)
		// A scope that does not resolve has no fingerprint either.
		perr := err
		if err == nil {
			fp, perr = snap.Fingerprint(ctx)
			if files, err = snap.Changes(ctx); err == nil {
				stat, err = snap.DiffStat(ctx)
			}
		}
		if perr != nil {
			log.Printf("fingerprint: %v", perr)
		}
		var commit *gitx.Commit
		if err == nil && s.Kind == gitx.ScopeCommit {
			if cm, cerr := r.Commit(ctx, s.Commit); cerr == nil {
				commit = &cm
			} else {
				log.Printf("commit message: %v", cerr)
			}
		}
		found := a.reviews.Read(r.Root, s, before, fp, perr)
		var cr *gitx.ContentReader
		// The files of the first page, read before it shows.
		read := map[string]contents{}
		if err == nil {
			cr = snap.NewContentReader(ctx)
			defer func() {
				if err := cr.Close(); err != nil && ctx.Err() == nil {
					log.Printf("content reader: %v", err)
				}
			}()
			for _, p := range first {
				if i := slices.IndexFunc(files, func(f gitx.FileChange) bool { return f.Path == p }); i >= 0 {
					oldText, newText, cerr := cr.Contents(files[i])
					read[p] = contents{files[i], oldText, newText, cerr}
				} else {
					text, cerr := cr.File(p)
					read[p] = contents{newText: text, err: cerr}
				}
			}
		}
		a.update(func() {
			if gen != a.gen {
				return
			}
			if !entered {
				a.enterScope(s, rev)
			}
			a.loading, a.switching, a.loadingScope = false, false, nil
			if err != nil {
				a.perLoad = perLoad{loadErr: err.Error(), diffs: map[string]*fileDiff{}, extra: map[string]*extraFile{}}
				return
			}
			diffs := map[string]*fileDiff{}
			for _, f := range files {
				diffs[f.Path] = &fileDiff{change: f, loading: true}
			}
			a.perLoad = perLoad{
				snap: snap, files: files, diffstat: stat, fp: fp, patchKnown: perr == nil,
				diffs: diffs, extra: map[string]*extraFile{}, commit: commit,
			}
			a.shown = true
			a.settle(a.reviews.Decide(found, a.rev, a.runner.reviewBusy))
			for p, c := range read {
				if a.diffs[p] != nil {
					a.setContents(c.change, c.oldText, c.newText, c.err)
				} else {
					a.extra[p] = newExtraFile(c.newText, c.err)
				}
			}
			a.leaveGone()
		})
		if err != nil {
			return
		}
		if found.Searches() && ctx.Err() == nil {
			moved, probable := found.FindCommits(ctx, r)
			a.update(func() {
				if gen != a.gen {
					return
				}
				a.movedTo = a.reviews.Located(found, moved, probable)
			})
		}
		for _, f := range files {
			if ctx.Err() != nil {
				return
			}
			if _, ok := read[f.Path]; ok {
				continue
			}
			oldText, newText, cerr := cr.Contents(f)
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

// firstPaint are the files the page shows first: the code snippets of
// an overview or a walkthrough, the file of a file page, the files of a
// group page.
func (a *app) firstPaint(rev *review.Review, page page, fresh bool) []string {
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
	case page.kind == kindOverview && rev.Overview != nil:
		walk(rev.Overview.Walk)
	case page.kind == kindWalk:
		if w := rev.Walk(page.key); w != nil {
			walk(w.Content)
		}
	case page.kind == kindFile:
		paths = append(paths, page.key)
	case page.kind == kindGroup && !fresh:
		paths = append(paths, a.groupFiles(page.key)...)
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

// settle applies a decision on the review of the scope. It runs while
// a.rev is still the review the decision started from. A failure goes to
// the notice, which stays, because a load runs it each time.
func (a *app) settle(out continuity.Outcome) {
	for _, r := range out.Dropped {
		a.release(r)
	}
	if old := out.Archived; old != nil {
		if a.leaveReview() {
			a.saved(a.store.SaveReview(old))
		}
		a.movedTo = ""
	}
	a.rev = out.Rev
	if out.Err != nil {
		a.saveErr = out.Err.Error()
	}
}

// restoreArchived brings an archived review back to the uncommitted
// scope, which keeps it from then on.
func (a *app) restoreArchived(e *continuity.Earlier) {
	a.showNext(a.reviews.Restore(e, a.rev, a.runner.reviewBusy), "Could not restore the review")
}

// moveArchived moves an archived review to the commit that probably
// holds its changes, when that commit has no review of its own, and
// opens the commit.
func (a *app) moveArchived(e *continuity.Earlier) {
	to := gitx.Scope{Kind: gitx.ScopeCommit, Commit: e.Probably}
	a.showNext(a.reviews.MoveTo(a.repo.Root, e, to, a.runner.reviewBusy), "Could not move the review")
}

func (a *app) deleteArchived(e *continuity.Earlier) {
	a.showNext(a.reviews.Delete(a.repo.Root, e), "Could not delete the review")
}

// showNext shows what an action on an archived review did. When the
// action replaced the review of the scope, its error goes to the notice
// with the other save errors. Any other error goes to a dialog that says
// failed.
func (a *app) showNext(next continuity.Next, failed string) {
	if next.Rev != nil {
		a.settle(next.Outcome)
	} else if next.Err != nil {
		a.showError(failed, next.Err)
	}
	if next.Left {
		a.leaveGone()
	}
	if next.Open == nil {
		return
	}
	if *next.Open == a.scope {
		a.openPage(overview)
	} else {
		a.setScope(*next.Open)
	}
}

// stale reports whether the overview was written for other changes than
// the scope has now.
func (a *app) stale() bool {
	ov := a.rev.Overview
	if !ov.Done() || a.loading || (ov.PatchID != "" && !a.patchKnown) || a.busy(store.TargetOverview) {
		return false
	}
	return ov.Stale(a.fp.Patch, a.fileNames())
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
	if a.loading || !ov.Done() || ov.PatchID != "" && !a.patchKnown || a.busy(store.TargetOverview) || a.stale() {
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
	a.grouped = groupsCache{groups: groups, ids: groupIDs(groups), from: from, files: a.files}
	return groups
}

// groupIDs are the ids of a group page, its sidebar item and its open
// state, in the order of groups. The id is the title, so that a page
// keeps its group when the groups change. A title that an earlier group
// has already taken gets a number, such as "Tests #2".
func groupIDs(groups []review.Group) []string {
	ids := make([]string, len(groups))
	taken := map[string]bool{}
	for i, g := range groups {
		id := g.Title
		for n := 2; taken[id]; n++ {
			id = fmt.Sprintf("%s #%d", g.Title, n)
		}
		taken[id] = true
		ids[i] = id
	}
	return ids
}

// groupID is the id of the group at index i of groups().
func (a *app) groupID(i int) string {
	a.groups()
	return a.grouped.ids[i]
}

// groupsCache holds the groups made from the groups of an overview and
// the changed files. Every change of the files or the overview groups
// puts a new slice in place, so the same slices give the same groups.
// The cache keeps the slices it was made from, so a new slice cannot
// get the address of an old one. The zero cache holds the groups of no
// files: none.
type groupsCache struct {
	groups []review.Group
	ids    []string
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
	rev, err := a.reviews.StartOver(a.repo.Root, a.scope)
	if err != nil {
		a.showError("Could not start the review over", err)
		return
	}
	a.release(a.rev)
	a.leaveReview()
	a.rev = rev
	a.drafts = map[string]string{}
}

func (a *app) confirmStartOver() {
	a.confirm("Start this review over?", "The overview, the deeper walkthroughs, all comments and threads, and the viewed marks of this scope are deleted.", "Start Over", a.startOver)
}

// confirm runs fn after the user agrees in a warning dialog. A test can
// set askConfirm in place of the dialog. Without both, fn runs at once.
func (a *app) confirm(message, detail, button string, fn func()) {
	if a.askConfirm != nil {
		a.askConfirm(message, detail, button, fn)
		return
	}
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
	if a.reviews.Obsolete(r) {
		return
	}
	if r == a.rev {
		a.keepSnippets()
	}
	a.saved(a.store.SaveReview(r))
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
		for _, c := range a.commits.rows {
			if c.SHA == a.scope.Commit {
				return fmt.Sprintf("%s %s", c.Short, c.Subject)
			}
		}
	}
	return a.scope.Label()
}

func (a *app) openThread(id string) {
	a.closeEmptyThread(id)
	a.panel = panelThread(id)
}

// closeEmptyThread drops a thread the user opened and left without
// writing anything, unless it is the one being opened. It reports whether
// it dropped one.
func (a *app) closeEmptyThread(keep string) bool {
	id := a.threadID()
	if id == "" || id == keep || a.rev == nil {
		return false
	}
	t := a.rev.Thread(id)
	if t == nil || len(t.Messages) > 0 {
		return false
	}
	a.rev.DeleteThread(t.ID)
	delete(a.drafts, t.ID)
	return true
}

// leaveReview resets perReview before another review takes the place of
// the one the window shows. It reports whether it dropped an empty thread.
// Then the caller saves the review.
func (a *app) leaveReview() bool {
	dropped := a.closeEmptyThread("")
	a.perReview = newPerReview()
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
