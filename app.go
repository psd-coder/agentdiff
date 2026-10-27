package main

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

	"github.com/psdcoder/agentdiff/internal/gitx"
	"github.com/psdcoder/agentdiff/internal/review"
	"github.com/psdcoder/agentdiff/internal/store"
)

const commitsShown = 50

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
	rng        *gitx.Scope
	branches   []string
	worktrees  []gitx.Worktree
	commits    []gitx.Commit
	commitsGen int

	scope    gitx.Scope
	scopeID  string
	rev      *review.Review
	files    []gitx.FileChange
	diffstat string
	loading  bool
	loadErr  string
	diffs    map[string]*fileDiff
	gen      int
	// patch identifies the changes of the scope once patchKnown, see
	// gitx.Repo.PatchID.
	patch      string
	patchKnown bool
	// fileIDs maps the changed files to the IDs of their changes, see
	// gitx.Fingerprint.
	fileIDs map[string]string
	// archived is the latest archived review, shown on the uncommitted
	// scope while it has no review of its own.
	archived *review.Review
	// kept holds the archive IDs of reviews restored in this session,
	// which stay even though none of their files is changed.
	kept map[string]bool
	// movedTo is the commit that now holds the changes a stale overview
	// of the uncommitted changes was written for. When none does,
	// probably is a commit that changes all of its files.
	movedTo  string
	probably string
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
	exportAgent bool
	exportDone  bool
	savedPath   string
	askDraft    string
	question    string
	asking      *askTarget
	paneDraft   string
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

	cmds         map[string]*command
	pending      string // the command the menu bar queued
	paletteOpen  bool
	paletteQuery string
	paletteNav   pickList
}

func newApp(s store.Store) *app {
	return &app{
		store:       s,
		cfg:         s.LoadConfig(),
		diffs:       map[string]*fileDiff{},
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
	a.cfg.AddRecent(r.Root)
	a.saveConfig()
	a.branch, a.head, a.base, a.branches, a.commits = "", "", "", nil, nil
	a.baseSet, a.rng, a.worktrees = false, nil, nil
	if a.win != nil {
		a.win.SetTitle("AgentDiff: " + r.Name())
	}
	a.async(func() {
		ctx := context.Background()
		branch, _ := r.CurrentBranch(ctx)
		base := r.DefaultBase(ctx)
		branches, _ := r.Branches(ctx)
		trees, err := r.Worktrees(ctx)
		if err != nil {
			log.Printf("worktrees: %v", err)
		}
		a.update(func() {
			if a.repo != r {
				return
			}
			a.branch, a.base, a.branches, a.worktrees = branch, base, branches, trees
			a.loadCommits(nil)
		})
	})
	a.setScope(gitx.Scope{Kind: gitx.ScopeUncommitted})
}

// closeRepo goes back to the welcome screen.
func (a *app) closeRepo() {
	a.repo = nil
	a.gen++
	a.projectsOpen = false
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
			log.Printf("current branch: %v", err)
			return
		}
		branches, _ := r.Branches(ctx)
		trees, _ := r.Worktrees(ctx)
		a.update(func() {
			if a.repo != r {
				return
			}
			a.worktrees = trees
			if branch != a.branch {
				a.checkedOut(branch, branches)
			} else {
				a.branches = branches
			}
		})
	})
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
	a.syncBranch()
}

// loadCommits reads the commit list again, then runs then, if given.
func (a *app) loadCommits(then func()) {
	a.commitsGen++
	r, gen := a.repo, a.commitsGen
	base, head := a.listRange()
	a.async(func() {
		commits, err := r.Commits(context.Background(), base, head, commitsShown)
		if err != nil {
			log.Printf("commits: %v", err)
		}
		a.update(func() {
			if a.repo != r || gen != a.commitsGen {
				return
			}
			a.commits = commits
			if then != nil {
				then()
			}
		})
	})
}

func scopeID(s gitx.Scope) string { return "scope:" + s.Key() }

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
	a.scope, a.scopeID = s, scopeID(s)
	a.rev = a.reviewOf(s)
	a.rev.Scope = s
	a.resetNav()
	a.thread = ""
	a.exportDone, a.savedPath = false, ""
	a.adoptedFrom = nil
	a.reload()
}

// reload reads the changes of the scope again, keeping the review.
func (a *app) reload() {
	a.gen++
	gen, r, s := a.gen, a.repo, a.scope
	a.loading, a.loadErr = true, ""
	a.files, a.diffstat = nil, ""
	a.patch, a.patchKnown, a.movedTo, a.probably, a.fileIDs = "", false, "", "", nil
	a.diffs = map[string]*fileDiff{}
	a.extra = map[string]*extraFile{}
	written, empty := "", a.rev.Empty()
	var writtenFiles []string
	if ov := a.rev.Overview; ov != nil {
		written, writtenFiles = ov.PatchID, ov.WrittenFiles()
	}
	uncommitted := s.Kind == gitx.ScopeUncommitted
	a.async(func() {
		ctx := context.Background()
		files, err := r.Changes(ctx, s)
		stat := ""
		if err == nil {
			stat, err = r.DiffStat(ctx, s)
		}
		fp, perr := r.Fingerprint(ctx, s)
		if perr != nil {
			log.Printf("fingerprint: %v", perr)
		}
		var archived *review.Review
		if uncommitted && empty {
			if arch, ok := a.store.LatestArchive(r.Root); ok && arch.Overview != nil {
				archived, written, writtenFiles = arch, arch.Overview.PatchID, arch.Overview.WrittenFiles()
			}
		}
		movedTo, probably := "", ""
		if uncommitted && perr == nil && written != "" && written != fp.Patch {
			ids, err := r.CommitPatchIDs(ctx, "", "", commitsShown)
			if err != nil {
				log.Printf("commit patch ids: %v", err)
			}
			movedTo = ids[written]
			if movedTo == "" {
				if probably, err = r.CommitWithFiles(ctx, writtenFiles, commitsShown); err != nil {
					log.Printf("commit with files: %v", err)
				}
			}
		}
		var from *gitx.Scope
		if (empty || uncommitted && written != fp.Patch) && perr == nil && fp.Patch != "" {
			if src, ok := a.store.FindReview(r.Root, s, fp.Patch); ok {
				from = &src
			}
		}
		a.update(func() {
			if gen != a.gen {
				return
			}
			a.loading = false
			if err != nil {
				a.loadErr = err.Error()
				return
			}
			a.files, a.diffstat = files, stat
			a.patch, a.patchKnown, a.movedTo, a.probably, a.fileIDs = fp.Patch, perr == nil, movedTo, probably, fp.Files
			a.archived = archived
			switch {
			case from != nil && a.rev.Empty():
				a.adopt(*from)
			case from != nil && a.archivable():
				a.archiveCurrent()
				a.adopt(*from)
			case a.archivable() && a.rev.Overview.Drift(a.fileIDs).Disjoint():
				a.archiveCurrent()
			}
			if !a.rev.Empty() {
				a.archived = nil
			}
			for _, f := range files {
				a.diffs[f.Path] = &fileDiff{change: f, loading: true}
			}
			if f, ok := strings.CutPrefix(a.page, "file:"); ok && a.diffs[f] == nil {
				a.page = "overview"
			}
		})
		if err != nil {
			return
		}
		for _, f := range files {
			oldText, newText, cerr := r.Contents(ctx, s, f)
			a.update(func() {
				if gen != a.gen {
					return
				}
				a.setContents(f, oldText, newText, cerr)
			})
		}
	})
}

// adopt takes over the saved review of src, which was written for the
// same changes as the current scope has. A review of uncommitted changes
// moves, because those changes are gone from the working tree; any other
// one is copied.
func (a *app) adopt(src gitx.Scope) {
	if !a.rev.Empty() {
		return
	}
	srcKey := reviewKey(a.repo.Root, src)
	from := a.reviews[srcKey]
	if from != nil && a.reviewBusy(from) {
		return
	}
	move := src.Kind == gitx.ScopeUncommitted || src.Kind == gitx.ScopeArchived
	var err error
	if move {
		err = a.store.MoveReview(a.repo.Root, src, a.scope)
	} else {
		err = a.store.CopyReview(a.repo.Root, src, a.scope)
	}
	if err != nil {
		log.Printf("adopt review: %v", err)
		return
	}
	if move && from != nil {
		delete(a.reviews, srcKey)
		delete(a.runs, from)
	}
	delete(a.runs, a.rev)
	a.rev = a.store.LoadReview(a.repo.Root, a.scope)
	a.reviews[reviewKey(a.repo.Root, a.scope)] = a.rev
	a.adoptedFrom = &src
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

// archiveCurrent moves the review of the uncommitted changes into the
// archive and starts an empty one.
func (a *app) archiveCurrent() {
	old := a.rev
	to, err := a.store.ArchiveReview(a.repo.Root, archiveID(old.Overview))
	if err != nil {
		log.Printf("archive review: %v", err)
		return
	}
	delete(a.runs, old)
	old.Scope = to
	a.archived = old
	a.rev = review.New(a.repo.Root, a.scope)
	a.reviews[reviewKey(a.repo.Root, a.scope)] = a.rev
	a.thread = ""
	a.resetNav()
}

// restoreArchived brings the shown archived review back to the
// uncommitted scope, which keeps it from then on.
func (a *app) restoreArchived() {
	arch := a.archived
	if arch == nil || !a.rev.Empty() {
		return
	}
	a.kept[archiveID(arch.Overview)] = true
	a.adopt(arch.Scope)
	a.adoptedFrom, a.archived = nil, nil
}

// moveArchived moves the shown archived review to a commit that has no
// review of its own, and opens it.
func (a *app) moveArchived(sha string) {
	arch := a.archived
	to := gitx.Scope{Kind: gitx.ScopeCommit, Commit: sha}
	if arch == nil || !a.store.LoadReview(a.repo.Root, to).Empty() {
		a.setScope(to)
		return
	}
	if err := a.store.MoveReview(a.repo.Root, arch.Scope, to); err != nil {
		log.Printf("move archived review: %v", err)
		return
	}
	a.archived = nil
	delete(a.reviews, reviewKey(a.repo.Root, to))
	a.setScope(to)
}

func (a *app) deleteArchived() {
	arch := a.archived
	if arch == nil {
		return
	}
	if err := a.store.DeleteReview(a.repo.Root, arch.Scope); err != nil {
		log.Printf("delete archived review: %v", err)
	}
	a.archived = nil
	a.reload()
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
		if a.threadRun[t.ID] != nil {
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

// setViewed marks the files viewed, or not viewed.
func (a *app) setViewed(files []string, viewed bool) {
	for _, f := range files {
		a.rev.Viewed[f] = viewed
	}
	a.saveReview()
}

func (a *app) groups() []review.Group {
	if a.rev.Overview != nil && len(a.rev.Overview.Groups) > 0 {
		return review.NormalizeGroups(a.rev.Overview.Groups, a.fileNames())
	}
	return review.DirectoryGroups(a.fileNames())
}

// startOver forgets the review of the current scope: its overview,
// threads and viewed files.
func (a *app) startOver() {
	a.stopOverview()
	for _, t := range a.rev.Threads {
		a.stopThread(t)
	}
	for _, w := range a.rev.Walks {
		a.stopWalk(w.ID)
	}
	if err := a.store.DeleteReview(a.repo.Root, a.scope); err != nil {
		log.Printf("delete review: %v", err)
	}
	delete(a.runs, a.rev)
	a.rev = review.New(a.repo.Root, a.scope)
	a.reviews[reviewKey(a.repo.Root, a.scope)] = a.rev
	a.thread, a.savedPath = "", ""
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

func (a *app) saveReview() {
	if a.rev == nil {
		return
	}
	if err := a.store.SaveReview(a.rev); err != nil {
		log.Printf("save review: %v", err)
	}
}

func (a *app) saveReviewOf(r *review.Review) {
	if err := a.store.SaveReview(r); err != nil {
		log.Printf("save review: %v", err)
	}
}

func (a *app) saveConfig() {
	if err := a.store.SaveConfig(a.cfg); err != nil {
		log.Printf("save config: %v", err)
	}
}

func (a *app) scopeLabel() string {
	switch a.scope.Kind {
	case gitx.ScopeCommit:
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
// writing anything, unless it is the one being opened.
func (a *app) closeEmptyThread(keep string) {
	if a.thread == "" || a.thread == keep || a.rev == nil {
		return
	}
	if t := a.rev.Thread(a.thread); t != nil && len(t.Messages) == 0 {
		a.rev.DeleteThread(t.ID)
		delete(a.drafts, t.ID)
	}
}

func (a *app) newThread(anchor review.Anchor) {
	t := a.rev.AddThread(anchor)
	a.openThread(t.ID)
}

func (a *app) diffOf(path string) *fileDiff { return a.diffs[path] }

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
